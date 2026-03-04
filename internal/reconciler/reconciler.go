package reconciler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

const (
	defaultWorkDirBase = "/var/lib/tofuhut/workloads"
	defaultEnvDir      = "/etc/tofuhut/workloads"
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

// Run executes the OpenTofu reconciler flow for the given workload name.
func Run(workload string, cfg Config) error {
	workdir := filepath.Join(defaultWorkDirBase, workload)
	envFile := filepath.Join(defaultEnvDir, workload+".env")

	if _, err := os.Stat(workdir); err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("workload directory %s not found", workdir)}
	}

	envFromFile, err := loadEnvFile(envFile)
	if err != nil {
		return &ExitCodeError{Code: 1, Err: err}
	}

	cmdEnv := mergeEnv(filterEnv(os.Environ()), envFromFile)
	cmdEnv = setDefaultEnvValue(cmdEnv, "TF_IN_AUTOMATION", "1")
	cmdEnv = setEnvValue(cmdEnv, "TF_INPUT", "0")

	handler := newGatusHandler(workload, envFile, cfg)
	var exitCode int
	defer func() {
		if exitCode != 0 {
			handler.NotifyFailure(fmt.Sprintf("reconciler failure for %s", workload))
			return
		}
		handler.NotifySuccess()
	}()

	initArgs := []string{"init", "-input=false"}
	if cfg.Upgrade {
		initArgs = append(initArgs, "-upgrade")
	}
	if cfg.Reconfigure {
		initArgs = append(initArgs, "-reconfigure")
	}

	logrus.Infof("[tofu] Initializing workload %s", workload)
	if code, err := runCommand(cmdEnv, "tofu", initArgs...); err != nil {
		exitCode = 1
		return &ExitCodeError{Code: 1, Err: err}
	} else if code != 0 {
		exitCode = code
		return &ExitCodeError{Code: code, Err: fmt.Errorf("tofu init failed (rc=%d)", code)}
	}

	logrus.Infof("[tofu] Planning workload %s", workload)
	planArgs := []string{"plan", "-input=false", "-no-color", "-detailed-exitcode"}
	planCode, err := runCommand(cmdEnv, "tofu", planArgs...)
	if err != nil {
		exitCode = 1
		return &ExitCodeError{Code: 1, Err: err}
	}

	switch planCode {
	case 0:
		logrus.Infof("[tofu] No changes for %s", workload)
		return nil
	case 2:
		logrus.Infof("[tofu] Changes detected for %s", workload)
	default:
		exitCode = planCode
		return &ExitCodeError{Code: planCode, Err: fmt.Errorf("tofu plan failed (rc=%d)", planCode)}
	}

	if cfg.Mode == "apply" {
		logrus.Infof("[tofu] Applying workload %s", workload)
		applyArgs := []string{"apply", "-input=false", "-auto-approve"}
		if code, err := runCommand(cmdEnv, "tofu", applyArgs...); err != nil {
			exitCode = 1
			return &ExitCodeError{Code: 1, Err: err}
		} else if code != 0 {
			exitCode = code
			return &ExitCodeError{Code: code, Err: fmt.Errorf("tofu apply failed (rc=%d)", code)}
		}
	}

	return nil
}

func runCommand(env []string, name string, args ...string) (int, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

func loadEnvFile(path string) (map[string]string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("unable to read env file %s: %w", path, err)
	}

	cmd := exec.Command("bash", "-lc", fmt.Sprintf("set -a; source %q; env -0", path))
	cmd.Env = os.Environ()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to source env file %s: %w", path, err)
	}

	result := make(map[string]string)
	for _, entry := range bytes.Split(out.Bytes(), []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		kv := strings.SplitN(string(entry), "=", 2)
		if len(kv) != 2 {
			continue
		}
		result[kv[0]] = kv[1]
	}
	return result, nil
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
