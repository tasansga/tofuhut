package reconciler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

const (
	defaultWorkDirBase = "/var/lib/tofuhut/workloads"
	defaultEnvDir      = "/etc/tofuhut/workloads"
)

var (
	workDirBase = defaultWorkDirBase
	envDir      = defaultEnvDir
)

// SetWorkDirBaseForTests overrides the work dir base and returns a restore func.
// Intended for tests only.
func SetWorkDirBaseForTests(path string) func() {
	old := workDirBase
	workDirBase = path
	return func() {
		workDirBase = old
	}
}

// SetEnvDirForTests overrides the env dir and returns a restore func.
// Intended for tests only.
func SetEnvDirForTests(path string) func() {
	old := envDir
	envDir = path
	return func() {
		envDir = old
	}
}

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

// EnvFilePath returns the workload env file path.
func EnvFilePath(workload string) string {
	return filepath.Join(envDir, workload+".env")
}

// WorkDirPath returns the workload working directory path.
func WorkDirPath(workload string) string {
	return filepath.Join(workDirBase, workload)
}

// Run executes the OpenTofu reconciler flow for the given workload name.
func Run(workload string, cfg Config, envFile string, envFromFile map[string]string) error {
	return RunWithContext(context.Background(), workload, cfg, envFile, envFromFile)
}

// RunWithContext executes the OpenTofu reconciler flow for the given workload name.
func RunWithContext(ctx context.Context, workload string, cfg Config, envFile string, envFromFile map[string]string) error {
	workdir := filepath.Join(workDirBase, workload)
	planTextPath := filepath.Join(workdir, fmt.Sprintf("%s-plan.txt", workload))
	planFilePath := filepath.Join(workdir, "plan.tfplan")
	approvePath := filepath.Join(workdir, "approve")
	approvePendingPath := approvePath + ".pending"
	playbookPath := filepath.Join(workdir, "playbook.yml")

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
			logrus.WithFields(logrus.Fields{
				"workload": workload,
				"error":    ctx.Err().Error(),
			}).Info("reconciler canceled; skipping notifications")
			return
		}
		if exitCode != 0 {
			handler.NotifyFailure(fmt.Sprintf("reconciler failure for %s", workload))
			return
		}
		handler.NotifySuccess()
	}()

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

	initArgs := []string{"init", "-input=false"}
	if cfg.Upgrade {
		initArgs = append(initArgs, "-upgrade")
	}
	if cfg.Reconfigure {
		initArgs = append(initArgs, "-reconfigure")
	}

	logrus.Infof("[tofu] Initializing workload %s", workload)
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
				logrus.Infof("[tofu] Plan pending approval for %s", workload)
				return nil
			}
			logrus.Infof("[tofu] Approval found for %s, applying stored plan", workload)
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
			logrus.Warnf("[tofu] Approval file exists without a plan for %s; removing approval and replanning", workload)
			_ = os.Remove(approvePath)
		}
	}

	logrus.Infof("[tofu] Planning workload %s", workload)
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
		logrus.Infof("[tofu] No changes for %s", workload)
		if cfg.Mode == "apply" {
			_ = os.Remove(planFilePath)
			_ = os.Remove(planTextPath)
		}
		return nil
	case 2:
		logrus.Infof("[tofu] Changes detected for %s", workload)
		if cfg.Mode == "apply" {
			if err := os.WriteFile(planTextPath, planOut.Bytes(), 0600); err != nil {
				exitCode = 1
				return &ExitCodeError{Code: 1, Err: fmt.Errorf("failed to write plan output: %w", err)}
			}
			if fileExists(planFilePath) {
				_ = os.Chmod(planFilePath, 0600)
			}
			logrus.Infof("[tofu] Plan written to %s; approval required", planTextPath)
			notifyNtfy(cfg, workload, planTextPath)
		}
		if cfg.Mode == "auto-apply" {
			logrus.Infof("[tofu] Auto-apply enabled for %s", workload)
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

	if cfg.Mode == "apply" {
		approveExists := fileExists(approvePath)
		pendingExists := fileExists(approvePendingPath)
		if approveExists && !pendingExists {
			logrus.Warnf("[ansible] Stale approval found for %s; removing approval and waiting for approval request", workload)
			_ = os.Remove(approvePath)
			approveExists = false
		}
		if !approveExists {
			if !pendingExists {
				if err := os.WriteFile(approvePendingPath, []byte("pending"), 0600); err != nil {
					return &ExitCodeError{Code: 1, Err: fmt.Errorf("failed to write approval pending file: %w", err)}
				}
				notifyNtfy(cfg, workload, "")
			}
			logrus.Infof("[ansible] Approval required for %s", workload)
			return nil
		}
	}

	args := []string{"-v", "-c", "local"}
	if cfg.Mode == "plan" {
		args = append(args, "--check")
		logrus.Infof("[ansible] Planning workload %s", workload)
	} else {
		logrus.Infof("[ansible] Running workload %s", workload)
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
	return nil
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

// MergeConfig applies env file values to the config unless locked by CLI flags.
func MergeConfig(cfg Config, locks ConfigLocks, env map[string]string) (Config, error) {
	if !locks.WorkloadType {
		if value, ok := env["WORKLOAD_TYPE"]; ok {
			cfg.WorkloadType = value
		}
	}
	if cfg.WorkloadType == "" {
		return cfg, &ExitCodeError{Code: 2, Err: fmt.Errorf("WORKLOAD_TYPE is required (tofu or ansible)")}
	}
	if cfg.WorkloadType != "tofu" && cfg.WorkloadType != "ansible" {
		return cfg, &ExitCodeError{Code: 2, Err: fmt.Errorf("invalid WORKLOAD_TYPE %q: must be tofu or ansible", cfg.WorkloadType)}
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
