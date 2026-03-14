package reconciler

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

type ntfyConfig struct {
	BaseURL       string
	Topic         string
	Token         string
	ApproveURL    string
	WorkloadToken string
}

func notifyNtfy(cfg Config, workload, planTextPath string) {
	if cfg.NtfyURL == "" || cfg.NtfyTopic == "" {
		return
	}

	ncfg := ntfyConfig{
		BaseURL:       cfg.NtfyURL,
		Topic:         cfg.NtfyTopic,
		Token:         cfg.NtfyToken,
		ApproveURL:    cfg.ApproveURL,
		WorkloadToken: cfg.WorkloadToken,
	}
	if err := postNtfy(ncfg, workload, planTextPath); err != nil {
		logrus.Warnf("ntfy notification failed: %v", err)
	}
}

func postNtfy(cfg ntfyConfig, workload, planTextPath string) error {
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return fmt.Errorf("invalid ntfy url %q: %w", cfg.BaseURL, err)
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/" + cfg.Topic

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	payload, err := writer.CreateFormField("message")
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(payload, "OpenTofu changes detected for %s. Approval required.", workload)

	if planTextPath != "" {
		if data, err := os.ReadFile(planTextPath); err == nil {
			part, err := writer.CreateFormFile("file", filepath.Base(planTextPath))
			if err != nil {
				return err
			}
			_, _ = part.Write(data)
		}
	}

	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, parsed.String(), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	if cfg.ApproveURL != "" {
		if action := buildNtfyApproveAction(cfg.ApproveURL, cfg.WorkloadToken, workload); action != "" {
			req.Header.Set("Actions", action)
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if closeErr := resp.Body.Close(); closeErr != nil {
			return fmt.Errorf("unexpected ntfy status: %s (close error: %w)", resp.Status, closeErr)
		}
		return fmt.Errorf("unexpected ntfy status: %s", resp.Status)
	}

	if err := resp.Body.Close(); err != nil {
		return err
	}
	return nil
}

func buildNtfyApproveAction(approveURL, token, workload string) string {
	if approveURL == "" {
		return ""
	}
	parsed, err := url.Parse(approveURL)
	if err != nil {
		return ""
	}
	path := strings.TrimSuffix(parsed.Path, "/") + "/approve/" + url.PathEscape(workload)
	parsed = parsed.ResolveReference(&url.URL{Path: path})

	parts := []string{
		"http",
		"Approve",
		parsed.String(),
		"method=POST",
		"clear=true",
	}
	if token != "" {
		header := "Bearer " + token
		if strings.ContainsAny(header, ",; ") {
			header = `"` + header + `"`
		}
		parts = append(parts, "headers.Authorization="+header)
	}
	return strings.Join(parts, ", ")
}
