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
	BaseURL string
	Topic   string
	Token   string
}

func notifyNtfy(cfg Config, workload, planTextPath string) {
	if cfg.NtfyURL == "" || cfg.NtfyTopic == "" {
		return
	}

	ncfg := ntfyConfig{BaseURL: cfg.NtfyURL, Topic: cfg.NtfyTopic, Token: cfg.NtfyToken}
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
	_, _ = payload.Write([]byte(fmt.Sprintf("OpenTofu changes detected for %s. Approval required.", workload)))

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

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected ntfy status: %s", resp.Status)
	}
	return nil
}
