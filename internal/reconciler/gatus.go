package reconciler

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

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
		group:    "tofuhut-reconciler",
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
			logrus.Warnf("gatus token lookup failed: %v", err)
		} else if value != "" {
			token = value
		}
	}

	if url == "" || token == "" {
		logrus.Info("gatus not configured - not notifying Gatus")
		return
	}

	if err := pushGatus(url, token, key, success, message); err != nil {
		logrus.Warnf("gatus push failed: %v", err)
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

func pushGatus(baseURL, token, key string, success bool, message string) error {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid gatus url %q: %w", baseURL, err)
	}
	basePath := strings.TrimSuffix(parsed.Path, "/")
	apiPrefix := "/api/v1"
	var path string
	if strings.HasSuffix(basePath, apiPrefix) {
		path = fmt.Sprintf("%s/endpoints/%s/external", basePath, url.PathEscape(key))
	} else {
		path = fmt.Sprintf("%s/api/v1/endpoints/%s/external", basePath, url.PathEscape(key))
	}
	parsed = parsed.ResolveReference(&url.URL{Path: path})

	q := parsed.Query()
	q.Set("success", fmt.Sprintf("%t", success))
	if !success && message != "" {
		q.Set("error", message)
	}
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodPost, parsed.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if closeErr := resp.Body.Close(); closeErr != nil {
			return fmt.Errorf("unexpected gatus status: %s (close error: %w)", resp.Status, closeErr)
		}
		return fmt.Errorf("unexpected gatus status: %s", resp.Status)
	}

	if err := resp.Body.Close(); err != nil {
		return err
	}
	return nil
}
