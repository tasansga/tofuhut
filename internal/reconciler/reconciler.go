package reconciler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// ExitCodeError is returned when a command should exit with a specific code.
type ExitCodeError struct {
	Code int
	Err  error
}

func (e *ExitCodeError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *ExitCodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Run executes the reconciler flow for the given workload name.
func Run(workload string, cfg Config, envFile string, envFromFile map[string]string, paths Paths) error {
	return RunWithContext(context.Background(), workload, cfg, envFile, envFromFile, paths)
}

// RunWithContext executes the reconciler flow for the given workload name.
func RunWithContext(ctx context.Context, workload string, cfg Config, envFile string, envFromFile map[string]string, paths Paths) (runErr error) {
	finishMetric := startRunMetric(ctx, workload, cfg.WorkloadType)
	metricDone := false
	recordMetric := func(result string) {
		if metricDone {
			return
		}
		metricDone = true
		finishMetric(result)
	}
	defer func() {
		if metricDone {
			return
		}
		if runErr != nil {
			if ctx.Err() != nil {
				recordMetric("canceled")
				return
			}
			recordMetric("error")
			return
		}
		if ctx.Err() != nil {
			recordMetric("canceled")
			return
		}
		recordMetric("success")
	}()

	workdir := paths.WorkDirPath(workload)
	planTextPath := filepath.Join(workdir, fmt.Sprintf("%s-plan.txt", workload))
	planFilePath := filepath.Join(workdir, "plan.tfplan")
	approvePath := filepath.Join(workdir, "approve")
	approvePendingPath := approvePath + ".pending"
	playbookPath := filepath.Join(workdir, "playbook.yml")
	dnsConfigPath := filepath.Join(workdir, "dnsconfig.js")
	dnsPreviewTextPath := filepath.Join(workdir, fmt.Sprintf("%s-preview.txt", workload))
	dnsPreviewReportPath := filepath.Join(workdir, "preview-report.json")

	if _, err := os.Stat(workdir); err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("workload directory %s not found", workdir)}
	}
	if cfg.Mode == "apply" && cfg.WorkloadToken == "" {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("MODE=apply requires WORKLOAD_TOKEN to be set for workload %s", workload)}
	}
	if err := ensureCommandAvailable(cfg.WorkloadType); err != nil {
		return err
	}

	cmdEnv := mergeEnv(filterEnv(os.Environ()), envFromFile)
	cmdEnv = setDefaultEnvValue(cmdEnv, "TF_IN_AUTOMATION", "1")
	cmdEnv = setEnvValue(cmdEnv, "TF_INPUT", "0")

	handler := newGatusHandler(workload, envFile, cfg)
	var exitCode int
	defer func() {
		if ctx.Err() != nil {
			logrus.WithFields(withRequestID(ctx, logrus.Fields{
				"workload": workload,
				"error":    ctx.Err().Error(),
			})).Info("reconciler canceled; skipping notifications")
			return
		}
		if exitCode != 0 {
			handler.NotifyFailure(fmt.Sprintf("reconciler failure for %s", workload))
			return
		}
		handler.NotifySuccess()
	}()

	defer func() {
		if cfg.PostReconcileHook == "" {
			return
		}
		postResult := reconcileResultForHook(runErr, ctx)
		hookEnv := hookEnv(cmdEnv, workload, workdir, postResult, ctx)
		if err := runReconcileHook(ctx, "post", cfg.PostReconcileHook, cfg.PostHookTimeout, hookEnv, workdir); err != nil {
			if runErr == nil {
				exitCode = 1
				runErr = &ExitCodeError{Code: 1, Err: err}
				return
			}
			logrus.WithError(err).WithFields(withRequestID(ctx, logrus.Fields{
				"component": "reconciler",
				"workload":  workload,
			})).Warn("post-reconcile hook failed after workload failure")
		}
	}()

	if err := runReconcileHook(ctx, "pre", cfg.PreReconcileHook, cfg.PreHookTimeout, hookEnv(cmdEnv, workload, workdir, "running", ctx), workdir); err != nil {
		exitCode = 1
		return &ExitCodeError{Code: 1, Err: err}
	}

	if cfg.WorkloadType == "ansible" {
		if err := runAnsible(ctx, workload, cfg, approvePath, approvePendingPath, playbookPath, cmdEnv, workdir); err != nil {
			if exitErr, ok := err.(*ExitCodeError); ok {
				exitCode = exitErr.Code
			} else {
				exitCode = 1
			}
			return err
		}
		return nil
	}
	if cfg.WorkloadType == "dnscontrol" {
		if err := runDNSControl(ctx, workload, cfg, approvePath, approvePendingPath, dnsConfigPath, dnsPreviewTextPath, dnsPreviewReportPath, cmdEnv, workdir); err != nil {
			if exitErr, ok := err.(*ExitCodeError); ok {
				exitCode = exitErr.Code
			} else {
				exitCode = 1
			}
			return err
		}
		return nil
	}

	initArgs := []string{"init", "-input=false"}
	if cfg.Upgrade {
		initArgs = append(initArgs, "-upgrade")
	}
	if cfg.Reconfigure {
		initArgs = append(initArgs, "-reconfigure")
	}

	logrus.WithFields(withRequestID(ctx, logrus.Fields{
		"component":     "reconciler",
		"workload":      workload,
		"workload_type": "tofu",
		"mode":          cfg.Mode,
	})).Info("initializing workload")
	if code, err := runCommand(ctx, commandOptions{Env: cmdEnv, Dir: workdir}, "tofu", initArgs...); err != nil {
		exitCode = 1
		return &ExitCodeError{Code: 1, Err: err}
	} else if code != 0 {
		exitCode = code
		return &ExitCodeError{Code: code, Err: fmt.Errorf("tofu init failed (rc=%d)", code)}
	}

	planExists := fileExists(planFilePath)
	approveExists := fileExists(approvePath)
	if cfg.Mode == "apply" {
		if planExists {
			if !approveExists {
				logrus.WithFields(withRequestID(ctx, logrus.Fields{
					"component":     "reconciler",
					"workload":      workload,
					"workload_type": "tofu",
					"mode":          cfg.Mode,
				})).Info("plan pending approval")
				return nil
			}
			logrus.WithFields(withRequestID(ctx, logrus.Fields{
				"component":     "reconciler",
				"workload":      workload,
				"workload_type": "tofu",
				"mode":          cfg.Mode,
			})).Info("approval found; applying stored plan")
			applyArgs := []string{"apply", "-input=false", "-auto-approve", planFilePath}
			if code, err := runCommand(ctx, commandOptions{Env: cmdEnv, Dir: workdir}, "tofu", applyArgs...); err != nil {
				exitCode = 1
				return &ExitCodeError{Code: 1, Err: err}
			} else if code != 0 {
				exitCode = code
				return &ExitCodeError{Code: code, Err: fmt.Errorf("tofu apply failed (rc=%d)", code)}
			}
			_ = os.Remove(planFilePath)
			_ = os.Remove(planTextPath)
			_ = os.Remove(approvePath)
			return nil
		}
		if approveExists {
			logrus.WithFields(withRequestID(ctx, logrus.Fields{
				"component":     "reconciler",
				"workload":      workload,
				"workload_type": "tofu",
				"mode":          cfg.Mode,
			})).Warn("approval file exists without plan; removing approval and replanning")
			_ = os.Remove(approvePath)
		}
	}

	logrus.WithFields(withRequestID(ctx, logrus.Fields{
		"component":     "reconciler",
		"workload":      workload,
		"workload_type": "tofu",
		"mode":          cfg.Mode,
	})).Info("planning workload")
	planArgs := []string{"plan", "-input=false", "-no-color", "-detailed-exitcode"}
	if cfg.Mode == "apply" {
		planArgs = append(planArgs, "-out", planFilePath)
	}
	var planOut bytes.Buffer
	planCode, err := runCommand(ctx, commandOptions{
		Env:    cmdEnv,
		Dir:    workdir,
		Stdout: io.MultiWriter(os.Stdout, &planOut),
	}, "tofu", planArgs...)
	if err != nil {
		exitCode = 1
		return &ExitCodeError{Code: 1, Err: err}
	}

	switch planCode {
	case 0:
		logrus.WithFields(withRequestID(ctx, logrus.Fields{
			"component":     "reconciler",
			"workload":      workload,
			"workload_type": "tofu",
			"mode":          cfg.Mode,
		})).Info("no changes")
		if cfg.Mode == "apply" {
			_ = os.Remove(planFilePath)
			_ = os.Remove(planTextPath)
		}
		return nil
	case 2:
		logrus.WithFields(withRequestID(ctx, logrus.Fields{
			"component":     "reconciler",
			"workload":      workload,
			"workload_type": "tofu",
			"mode":          cfg.Mode,
		})).Info("changes detected")
		if cfg.Mode == "apply" {
			if err := os.WriteFile(planTextPath, planOut.Bytes(), 0600); err != nil {
				exitCode = 1
				return &ExitCodeError{Code: 1, Err: fmt.Errorf("failed to write plan output: %w", err)}
			}
			if fileExists(planFilePath) {
				_ = os.Chmod(planFilePath, 0600)
			}
			logrus.WithFields(withRequestID(ctx, logrus.Fields{
				"component":      "reconciler",
				"workload":       workload,
				"workload_type":  "tofu",
				"mode":           cfg.Mode,
				"plan_text_path": planTextPath,
			})).Info("plan written; approval required")
			notifyNtfy(cfg, workload, planTextPath)
		}
		if cfg.Mode == "auto-apply" {
			logrus.WithFields(withRequestID(ctx, logrus.Fields{
				"component":     "reconciler",
				"workload":      workload,
				"workload_type": "tofu",
				"mode":          cfg.Mode,
			})).Info("auto-apply enabled")
			applyArgs := []string{"apply", "-input=false", "-auto-approve"}
			if code, err := runCommand(ctx, commandOptions{Env: cmdEnv, Dir: workdir}, "tofu", applyArgs...); err != nil {
				exitCode = 1
				return &ExitCodeError{Code: 1, Err: err}
			} else if code != 0 {
				exitCode = code
				return &ExitCodeError{Code: code, Err: fmt.Errorf("tofu apply failed (rc=%d)", code)}
			}
		}
		return nil
	default:
		exitCode = planCode
		return &ExitCodeError{Code: planCode, Err: fmt.Errorf("tofu plan failed (rc=%d)", planCode)}
	}
}

func ensureCommandAvailable(workloadType string) error {
	switch workloadType {
	case "ansible":
		if _, err := exec.LookPath("ansible-playbook"); err != nil {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("ansible-playbook not found in PATH")}
		}
	case "dnscontrol":
		if _, err := exec.LookPath("dnscontrol"); err != nil {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("dnscontrol not found in PATH")}
		}
	case "tofu":
		if _, err := exec.LookPath("tofu"); err != nil {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("tofu not found in PATH")}
		}
	}
	return nil
}

// ValidateRuntime checks that required executables are available for the workload type.
func ValidateRuntime(cfg Config) error {
	return ensureCommandAvailable(cfg.WorkloadType)
}

func runAnsible(ctx context.Context, workload string, cfg Config, approvePath, approvePendingPath, playbookPath string, cmdEnv []string, workdir string) error {
	if _, err := os.Stat(playbookPath); err != nil {
		if os.IsNotExist(err) {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("playbook %s not found for workload %s", playbookPath, workload)}
		}
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("unable to stat playbook %s: %w", playbookPath, err)}
	}

	gate, err := newReconcileChangedGate(workload, "ansible", workdir, playbookPath, cfg.ReconcileChangedOnly)
	if err != nil {
		return &ExitCodeError{Code: 1, Err: err}
	}

	approveExists := false
	pendingExists := false
	if cfg.Mode == "apply" {
		approveExists = fileExists(approvePath)
		pendingExists = fileExists(approvePendingPath)
		if approveExists && !pendingExists {
			logrus.WithFields(withRequestID(ctx, logrus.Fields{
				"component":     "reconciler",
				"workload":      workload,
				"workload_type": "ansible",
				"mode":          cfg.Mode,
			})).Warn("stale approval found; removing approval and waiting for approval request")
			_ = os.Remove(approvePath)
			approveExists = false
		}
	}
	if gate.shouldSkip(ctx, cfg.Mode == "apply" && approveExists) {
		return nil
	}
	if cfg.Mode == "apply" {
		if !approveExists {
			if !pendingExists {
				if err := os.WriteFile(approvePendingPath, []byte("pending"), 0600); err != nil {
					return &ExitCodeError{Code: 1, Err: fmt.Errorf("failed to write approval pending file: %w", err)}
				}
				notifyNtfy(cfg, workload, "")
			}
			if err := gate.markReconciled(); err != nil {
				return &ExitCodeError{Code: 1, Err: err}
			}
			logrus.WithFields(withRequestID(ctx, logrus.Fields{
				"component":     "reconciler",
				"workload":      workload,
				"workload_type": "ansible",
				"mode":          cfg.Mode,
			})).Info("approval required")
			return nil
		}
	}

	args := []string{"-v", "-c", "local"}
	if cfg.Mode == "plan" {
		args = append(args, "--check")
		logrus.WithFields(withRequestID(ctx, logrus.Fields{
			"component":     "reconciler",
			"workload":      workload,
			"workload_type": "ansible",
			"mode":          cfg.Mode,
		})).Info("planning workload")
	} else {
		logrus.WithFields(withRequestID(ctx, logrus.Fields{
			"component":     "reconciler",
			"workload":      workload,
			"workload_type": "ansible",
			"mode":          cfg.Mode,
		})).Info("running workload")
	}
	args = append(args, playbookPath)

	code, err := runCommand(ctx, commandOptions{Env: cmdEnv, Dir: workdir}, "ansible-playbook", args...)
	if err != nil {
		return &ExitCodeError{Code: 1, Err: err}
	}
	if code != 0 {
		return &ExitCodeError{Code: code, Err: fmt.Errorf("ansible-playbook failed (rc=%d)", code)}
	}

	if cfg.Mode == "apply" {
		_ = os.Remove(approvePath)
		_ = os.Remove(approvePendingPath)
	}
	if err := gate.markReconciled(); err != nil {
		return &ExitCodeError{Code: 1, Err: err}
	}
	return nil
}

type dnscontrolReport struct {
	Corrections int `json:"corrections"`
}

func runDNSControl(ctx context.Context, workload string, cfg Config, approvePath, approvePendingPath, dnsConfigPath, previewTextPath, previewReportPath string, cmdEnv []string, workdir string) error {
	if _, err := os.Stat(dnsConfigPath); err != nil {
		if os.IsNotExist(err) {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("dnsconfig %s not found for workload %s", dnsConfigPath, workload)}
		}
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("unable to stat dnsconfig %s: %w", dnsConfigPath, err)}
	}

	gate, err := newReconcileChangedGate(workload, "dnscontrol", workdir, dnsConfigPath, cfg.ReconcileChangedOnly)
	if err != nil {
		return &ExitCodeError{Code: 1, Err: err}
	}

	approveExists := false
	pendingExists := false
	if cfg.Mode == "apply" {
		approveExists = fileExists(approvePath)
		pendingExists = fileExists(approvePendingPath)
		if approveExists && !pendingExists {
			logrus.WithFields(withRequestID(ctx, logrus.Fields{
				"component":     "reconciler",
				"workload":      workload,
				"workload_type": "dnscontrol",
				"mode":          cfg.Mode,
			})).Warn("stale approval found; removing approval and waiting for approval request")
			_ = os.Remove(approvePath)
			approveExists = false
		}
	}

	if gate.shouldSkip(ctx, cfg.Mode == "apply" && approveExists) {
		return nil
	}

	if cfg.Mode == "apply" {
		if approveExists {
			logrus.WithFields(withRequestID(ctx, logrus.Fields{
				"component":     "reconciler",
				"workload":      workload,
				"workload_type": "dnscontrol",
				"mode":          cfg.Mode,
			})).Info("approval found; applying changes")
			code, err := runCommand(ctx, commandOptions{Env: cmdEnv, Dir: workdir}, "dnscontrol", "push")
			if err != nil {
				return &ExitCodeError{Code: 1, Err: err}
			}
			if code != 0 {
				return &ExitCodeError{Code: code, Err: fmt.Errorf("dnscontrol push failed (rc=%d)", code)}
			}
			_ = os.Remove(approvePath)
			_ = os.Remove(approvePendingPath)
			_ = os.Remove(previewTextPath)
			_ = os.Remove(previewReportPath)
			if err := gate.markReconciled(); err != nil {
				return &ExitCodeError{Code: 1, Err: err}
			}
			return nil
		}
	}

	logrus.WithFields(withRequestID(ctx, logrus.Fields{
		"component":     "reconciler",
		"workload":      workload,
		"workload_type": "dnscontrol",
		"mode":          cfg.Mode,
	})).Info("previewing workload")
	var previewOut bytes.Buffer
	code, err := runCommand(ctx, commandOptions{
		Env:    cmdEnv,
		Dir:    workdir,
		Stdout: io.MultiWriter(os.Stdout, &previewOut),
	}, "dnscontrol", "preview", "--report", previewReportPath)
	if err != nil {
		return &ExitCodeError{Code: 1, Err: err}
	}
	if code != 0 {
		return &ExitCodeError{Code: code, Err: fmt.Errorf("dnscontrol preview failed (rc=%d)", code)}
	}

	changed, err := dnscontrolPreviewHasChanges(previewReportPath)
	if err != nil {
		return &ExitCodeError{Code: 1, Err: err}
	}
	if !changed {
		logrus.WithFields(withRequestID(ctx, logrus.Fields{
			"component":     "reconciler",
			"workload":      workload,
			"workload_type": "dnscontrol",
			"mode":          cfg.Mode,
		})).Info("no changes")
		if cfg.Mode == "apply" {
			_ = os.Remove(previewTextPath)
			_ = os.Remove(previewReportPath)
			_ = os.Remove(approvePendingPath)
		}
		if err := gate.markReconciled(); err != nil {
			return &ExitCodeError{Code: 1, Err: err}
		}
		return nil
	}

	logrus.WithFields(withRequestID(ctx, logrus.Fields{
		"component":     "reconciler",
		"workload":      workload,
		"workload_type": "dnscontrol",
		"mode":          cfg.Mode,
	})).Info("changes detected")
	if cfg.Mode == "auto-apply" {
		logrus.WithFields(withRequestID(ctx, logrus.Fields{
			"component":     "reconciler",
			"workload":      workload,
			"workload_type": "dnscontrol",
			"mode":          cfg.Mode,
		})).Info("auto-apply enabled")
		code, err := runCommand(ctx, commandOptions{Env: cmdEnv, Dir: workdir}, "dnscontrol", "push")
		if err != nil {
			return &ExitCodeError{Code: 1, Err: err}
		}
		if code != 0 {
			return &ExitCodeError{Code: code, Err: fmt.Errorf("dnscontrol push failed (rc=%d)", code)}
		}
		_ = os.Remove(previewTextPath)
		_ = os.Remove(previewReportPath)
		if err := gate.markReconciled(); err != nil {
			return &ExitCodeError{Code: 1, Err: err}
		}
		return nil
	}
	if cfg.Mode == "apply" {
		if err := os.WriteFile(previewTextPath, previewOut.Bytes(), 0600); err != nil {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("failed to write preview output: %w", err)}
		}
		if fileExists(previewReportPath) {
			_ = os.Chmod(previewReportPath, 0600)
		}
		if !pendingExists {
			if err := os.WriteFile(approvePendingPath, []byte("pending"), 0600); err != nil {
				return &ExitCodeError{Code: 1, Err: fmt.Errorf("failed to write approval pending file: %w", err)}
			}
			notifyNtfy(cfg, workload, previewTextPath)
		}
		logrus.WithFields(withRequestID(ctx, logrus.Fields{
			"component":        "reconciler",
			"workload":         workload,
			"workload_type":    "dnscontrol",
			"mode":             cfg.Mode,
			"preview_txt_path": previewTextPath,
		})).Info("preview written; approval required")
	}
	if err := gate.markReconciled(); err != nil {
		return &ExitCodeError{Code: 1, Err: err}
	}

	return nil
}

func dnscontrolPreviewHasChanges(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("failed to read dnscontrol preview report: %w", err)
	}

	var report []dnscontrolReport
	if err := json.Unmarshal(data, &report); err != nil {
		return false, fmt.Errorf("failed to parse dnscontrol preview report: %w", err)
	}
	for _, row := range report {
		if row.Corrections > 0 {
			return true, nil
		}
	}
	return false, nil
}

type commandOptions struct {
	Env    []string
	Dir    string
	Stdout io.Writer
	Stderr io.Writer
}

func runCommand(ctx context.Context, opts commandOptions, name string, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if opts.Stdout != nil {
		cmd.Stdout = opts.Stdout
	} else {
		cmd.Stdout = os.Stdout
	}
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	} else {
		cmd.Stderr = os.Stderr
	}
	if opts.Env != nil {
		cmd.Env = opts.Env
	} else {
		cmd.Env = os.Environ()
	}
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

func runReconcileHook(ctx context.Context, phase, hookPath string, timeout time.Duration, env []string, workdir string) error {
	if hookPath == "" {
		return nil
	}
	if !filepath.IsAbs(hookPath) {
		return fmt.Errorf("%s-reconcile hook must be an absolute path: %s", phase, hookPath)
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	logrus.WithFields(withRequestID(ctx, logrus.Fields{
		"component": "reconciler",
		"phase":     phase,
		"hook_path": hookPath,
	})).Info("running reconcile hook")

	code, err := runCommand(runCtx, commandOptions{Env: env, Dir: workdir}, hookPath)
	if err != nil {
		return fmt.Errorf("%s-reconcile hook failed: %w", phase, err)
	}
	if code != 0 {
		return fmt.Errorf("%s-reconcile hook failed (rc=%d)", phase, code)
	}
	return nil
}

func hookEnv(baseEnv []string, workload, workdir, result string, ctx context.Context) []string {
	env := append([]string{}, baseEnv...)
	env = setEnvValue(env, "TOFUHUT_WORKLOAD", workload)
	env = setEnvValue(env, "TOFUHUT_WORKDIR", workdir)
	env = setEnvValue(env, "TOFUHUT_RESULT", result)
	if requestID, ok := RequestIDFromContext(ctx); ok {
		env = setEnvValue(env, "TOFUHUT_REQUEST_ID", requestID)
	}
	if trigger := TriggerSourceFromContext(ctx); trigger != "" {
		env = setEnvValue(env, "TOFUHUT_TRIGGER", trigger)
	}
	return env
}

func reconcileResultForHook(runErr error, ctx context.Context) string {
	if runErr != nil {
		if ctx.Err() != nil {
			return "canceled"
		}
		return "error"
	}
	if ctx.Err() != nil {
		return "canceled"
	}
	return "success"
}

// LoadEnvFile sources the env file and returns only the variables set/changed by the file.
func LoadEnvFile(path string) (map[string]string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("unable to read env file %s: %w", path, err)
	}

	cmd := exec.Command("bash", "-lc", fmt.Sprintf("env -0; printf '__TOFUHUT_ENV_SPLIT__\\0'; set -a; source %q; env -0", path))
	cmd.Env = os.Environ()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to source env file %s: %w", path, err)
	}

	marker := []byte("__TOFUHUT_ENV_SPLIT__\x00")
	data := out.Bytes()
	idx := bytes.Index(data, marker)
	if idx == -1 {
		return nil, fmt.Errorf("failed to parse env file %s", path)
	}
	before := parseEnvBlob(data[:idx])
	after := parseEnvBlob(data[idx+len(marker):])
	return diffEnv(before, after), nil
}

func mergeEnv(base []string, extra map[string]string) []string {
	env := make([]string, 0, len(base)+len(extra))
	env = append(env, base...)
	for k, v := range extra {
		env = setEnvValue(env, k, v)
	}
	return env
}

func filterEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if envAllowed(key) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func envAllowed(key string) bool {
	switch key {
	case "PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "LC_MESSAGES", "TZ":
		return true
	case "TMPDIR", "TEMP", "TMP":
		return true
	case "SSL_CERT_DIR", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE":
		return true
	case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy":
		return true
	}

	prefixes := []string{
		"LC_",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func setDefaultEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return env
		}
	}
	return append(env, prefix+value)
}

func withRequestID(ctx context.Context, fields logrus.Fields) logrus.Fields {
	if requestID, ok := RequestIDFromContext(ctx); ok {
		fields["request_id"] = requestID
	}
	return fields
}

func parseEnvBlob(blob []byte) map[string]string {
	result := make(map[string]string)
	for _, entry := range bytes.Split(blob, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		kv := strings.SplitN(string(entry), "=", 2)
		if len(kv) != 2 {
			continue
		}
		result[kv[0]] = kv[1]
	}
	return result
}

func diffEnv(before, after map[string]string) map[string]string {
	result := make(map[string]string)
	for key, value := range after {
		if beforeValue, ok := before[key]; !ok || beforeValue != value {
			result[key] = value
		}
	}
	return result
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type reconcileChangedGate struct {
	enabled      bool
	workload     string
	workloadType string
	watchedPath  string
	hashPath     string
	currentHash  string
	changed      bool
}

func newReconcileChangedGate(workload, workloadType, workdir, watchedPath string, enabled bool) (*reconcileChangedGate, error) {
	gate := &reconcileChangedGate{
		enabled:      enabled,
		workload:     workload,
		workloadType: workloadType,
		watchedPath:  watchedPath,
		hashPath:     filepath.Join(workdir, ".reconcile-input.sha256"),
	}
	if !enabled {
		return gate, nil
	}

	currentHash, err := fileSHA256(watchedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to hash %s: %w", watchedPath, err)
	}
	gate.currentHash = currentHash

	previousHashBytes, err := os.ReadFile(gate.hashPath)
	if err != nil {
		if os.IsNotExist(err) {
			gate.changed = true
			return gate, nil
		}
		return nil, fmt.Errorf("failed to read reconcile hash file %s: %w", gate.hashPath, err)
	}
	previousHash := strings.TrimSpace(string(previousHashBytes))
	gate.changed = previousHash != gate.currentHash
	return gate, nil
}

func (g *reconcileChangedGate) shouldSkip(ctx context.Context, hasApproval bool) bool {
	if g == nil || !g.enabled {
		return false
	}
	if ForceReconcileFromContext(ctx) {
		logrus.WithFields(withRequestID(ctx, logrus.Fields{
			"component":     "reconciler",
			"workload":      g.workload,
			"workload_type": g.workloadType,
			"path":          g.watchedPath,
		})).Info("change-gated reconcile bypassed due to manual force trigger")
		return false
	}
	if hasApproval {
		logrus.WithFields(withRequestID(ctx, logrus.Fields{
			"component":     "reconciler",
			"workload":      g.workload,
			"workload_type": g.workloadType,
			"path":          g.watchedPath,
		})).Info("change-gated reconcile bypassed due to approval")
		return false
	}
	if g.changed {
		return false
	}
	logrus.WithFields(withRequestID(ctx, logrus.Fields{
		"component":     "reconciler",
		"workload":      g.workload,
		"workload_type": g.workloadType,
		"path":          g.watchedPath,
	})).Info("skipping reconcile; watched file unchanged")
	return true
}

func (g *reconcileChangedGate) markReconciled() error {
	if g == nil || !g.enabled {
		return nil
	}
	if err := os.WriteFile(g.hashPath, []byte(g.currentHash+"\n"), 0600); err != nil {
		return fmt.Errorf("failed to persist reconcile hash %s: %w", g.hashPath, err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// MergeConfig applies env file values to the config unless locked by CLI flags.
func MergeConfig(cfg Config, locks ConfigLocks, env map[string]string) (Config, error) {
	if !locks.WorkloadType {
		if value, ok := env["WORKLOAD_TYPE"]; ok {
			cfg.WorkloadType = value
		}
	}
	if cfg.WorkloadType == "" {
		return cfg, &ExitCodeError{Code: 2, Err: fmt.Errorf("WORKLOAD_TYPE is required (tofu, ansible, or dnscontrol)")}
	}
	if cfg.WorkloadType != "tofu" && cfg.WorkloadType != "ansible" && cfg.WorkloadType != "dnscontrol" {
		return cfg, &ExitCodeError{Code: 2, Err: fmt.Errorf("invalid WORKLOAD_TYPE %q: must be tofu, ansible, or dnscontrol", cfg.WorkloadType)}
	}
	if !locks.Mode {
		if value, ok := env["MODE"]; ok {
			cfg.Mode = value
		}
	}
	if cfg.Mode == "" {
		cfg.Mode = "plan"
	}
	if cfg.Mode != "plan" && cfg.Mode != "apply" && cfg.Mode != "auto-apply" {
		return cfg, &ExitCodeError{Code: 2, Err: fmt.Errorf("invalid MODE %q: must be plan, apply, or auto-apply", cfg.Mode)}
	}
	if !locks.PreReconcileHook {
		if value, ok := env["PRE_RECONCILE_HOOK"]; ok {
			cfg.PreReconcileHook = value
		}
	}
	if !locks.PostReconcileHook {
		if value, ok := env["POST_RECONCILE_HOOK"]; ok {
			cfg.PostReconcileHook = value
		}
	}
	if !locks.PreHookTimeout {
		if value, ok := env["PRE_RECONCILE_TIMEOUT"]; ok && value != "" {
			parsed, err := time.ParseDuration(value)
			if err != nil {
				return cfg, &ExitCodeError{Code: 2, Err: fmt.Errorf("invalid PRE_RECONCILE_TIMEOUT %q: %w", value, err)}
			}
			cfg.PreHookTimeout = parsed
		}
	}
	if !locks.PostHookTimeout {
		if value, ok := env["POST_RECONCILE_TIMEOUT"]; ok && value != "" {
			parsed, err := time.ParseDuration(value)
			if err != nil {
				return cfg, &ExitCodeError{Code: 2, Err: fmt.Errorf("invalid POST_RECONCILE_TIMEOUT %q: %w", value, err)}
			}
			cfg.PostHookTimeout = parsed
		}
	}
	if !locks.ReconcileChangedOnly {
		if value, ok := env["RECONCILE_CHANGED_ONLY"]; ok {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return cfg, &ExitCodeError{Code: 2, Err: fmt.Errorf("invalid RECONCILE_CHANGED_ONLY %q: %w", value, err)}
			}
			cfg.ReconcileChangedOnly = parsed
		}
	}

	if !locks.Upgrade {
		if value, ok := env["UPGRADE"]; ok {
			if parsed, err := strconv.ParseBool(value); err == nil {
				cfg.Upgrade = parsed
			}
		}
	}
	if !locks.Reconfigure {
		if value, ok := env["RECONFIGURE"]; ok {
			if parsed, err := strconv.ParseBool(value); err == nil {
				cfg.Reconfigure = parsed
			}
		}
	}
	if !locks.GatusURL {
		if value, ok := env["GATUS_CLI_URL"]; ok {
			cfg.GatusURL = value
		}
	}
	if !locks.GatusToken {
		if value, ok := env["GATUS_CLI_TOKEN"]; ok {
			cfg.GatusToken = value
		}
	}
	if !locks.NtfyURL {
		if value, ok := env["NTFY_URL"]; ok {
			cfg.NtfyURL = value
		}
	}
	if !locks.NtfyTopic {
		if value, ok := env["NTFY_TOPIC"]; ok {
			cfg.NtfyTopic = value
		}
	}
	if !locks.NtfyToken {
		if value, ok := env["NTFY_TOKEN"]; ok {
			cfg.NtfyToken = value
		}
	}
	if !locks.ApproveURL {
		if value, ok := env["APPROVE_URL"]; ok {
			cfg.ApproveURL = value
		}
	}
	if !locks.WorkloadToken {
		if value, ok := env["WORKLOAD_TOKEN"]; ok {
			cfg.WorkloadToken = value
		}
	}

	return cfg, nil
}
