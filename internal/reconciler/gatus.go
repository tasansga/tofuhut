package reconciler

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"
)

type gatusHandler struct {
	workload string
	envFile  string
	group    string
	cfg      Config
}

func newGatusHandler(workload, envFile string, cfg Config) *gatusHandler {
	return &gatusHandler{
		workload: workload,
		envFile:  envFile,
		group:    "opentofu-reconciler",
		cfg:      cfg,
	}
}

func (g *gatusHandler) NotifySuccess() {
	g.notify(true, "")
}

func (g *gatusHandler) NotifyFailure(message string) {
	g.notify(false, message)
}

func (g *gatusHandler) notify(success bool, message string) {
	name := fmt.Sprintf("%s@%s", g.workload, hostname())
	key := fmt.Sprintf("%s_%s", sanitizeForGatus(g.group), sanitizeForGatus(name))

	url := g.cfg.GatusURL
	token := g.cfg.GatusToken
	if token == "" {
		if value, err := g.tokenFromFunction(name); err != nil {
			logrus.Warnf("gatus-cli token lookup failed: %v", err)
		} else if value != "" {
			token = value
		}
	}

	if url == "" || token == "" {
		logrus.Info("gatus-cli not configured - not notifying Gatus")
		return
	}

	if _, err := exec.LookPath("gatus-cli"); err != nil {
		logrus.Info("gatus-cli not in PATH - not notifying Gatus")
		return
	}

	args := []string{"external-endpoint", "push", "--url=" + url, "--token=" + token, "--key=" + key}
	if success {
		args = append(args, "--success=true")
	} else {
		args = append(args, "--success=false", "--error="+message)
	}

	if code, err := runCommand(os.Environ(), "gatus-cli", args...); err != nil {
		logrus.Warnf("gatus-cli failed: %v", err)
	} else if code != 0 {
		logrus.Warnf("gatus-cli exited with rc=%d", code)
	}
}

func (g *gatusHandler) tokenFromFunction(name string) (string, error) {
	if g.envFile == "" {
		return "", nil
	}

	cmd := exec.Command("bash", "-lc", fmt.Sprintf("source %q; if declare -F gatus_cli_token_for_name >/dev/null; then gatus_cli_token_for_name %q; fi", g.envFile, name))
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("gatus_cli_token_for_name failed: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

func hostname() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}

func sanitizeForGatus(value string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '`', '/', '_', ',', '.', '#', '+', '&':
			return '-'
		default:
			return r
		}
	}, value)
}
