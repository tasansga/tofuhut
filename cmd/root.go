package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"tofuhut/internal/reconciler"
)

var rootCmd = &cobra.Command{
	Use:           "tofuhut",
	Short:         "Tofuhut infrastructure reconciler",
	Version:       buildVersion(),
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		level, format, err := resolveLoggingConfig(cmd)
		if err != nil {
			return err
		}
		return configureLogging(level, format)
	},
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

// ExitCodeError wraps an error with a specific exit code.
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

func init() {
	rootCmd.SetVersionTemplate("{{.Version}}\n")
	rootCmd.PersistentFlags().String("log-level", "", "Log level (debug, info, warn, error, fatal, panic) (env LOG_LEVEL; default info)")
	rootCmd.PersistentFlags().String("log-format", "", "Log format (text or json) (env LOG_FORMAT; default text)")

	rootCmd.AddCommand(workloadCmd)
}

func resolveLoggingConfig(cmd *cobra.Command) (string, string, error) {
	level, _ := resolveString(cmd, "log-level", "LOG_LEVEL")
	format, _ := resolveString(cmd, "log-format", "LOG_FORMAT")

	if level == "" {
		level = "info"
	}
	if format == "" {
		format = "text"
	}

	level = strings.ToLower(strings.TrimSpace(level))
	format = strings.ToLower(strings.TrimSpace(format))

	if _, err := logrus.ParseLevel(level); err != nil {
		return "", "", fmt.Errorf("invalid log level %q", level)
	}
	if format != "text" && format != "json" {
		return "", "", fmt.Errorf("invalid log format %q: must be text or json", format)
	}

	return level, format, nil
}

func configureLogging(level, format string) error {
	parsed, err := logrus.ParseLevel(level)
	if err != nil {
		return fmt.Errorf("invalid log level %q", level)
	}
	logrus.SetLevel(parsed)

	if format == "json" {
		logrus.SetFormatter(&logrus.JSONFormatter{})
		return nil
	}
	logrus.SetFormatter(&logrus.TextFormatter{})
	return nil
}

func resolveConfig(cmd *cobra.Command) (reconciler.Config, reconciler.ConfigLocks, error) {
	mode, modeLocked := resolveString(cmd, "mode", "MODE")
	upgrade, upgradeLocked := resolveBool(cmd, "upgrade", "UPGRADE")
	reconfigure, reconfigureLocked := resolveBool(cmd, "reconfigure", "RECONFIGURE")
	gatusURL, gatusURLLocked := resolveString(cmd, "gatus-cli-url", "GATUS_CLI_URL")
	gatusToken, gatusTokenLocked := resolveString(cmd, "gatus-cli-token", "GATUS_CLI_TOKEN")
	ntfyURL, ntfyURLLocked := resolveString(cmd, "ntfy-url", "NTFY_URL")
	ntfyTopic, ntfyTopicLocked := resolveString(cmd, "ntfy-topic", "NTFY_TOPIC")
	ntfyToken, ntfyTokenLocked := resolveString(cmd, "ntfy-token", "NTFY_TOKEN")
	approveURL, approveURLLocked := resolveString(cmd, "approve-url", "APPROVE_URL")
	workloadToken, workloadTokenLocked := resolveString(cmd, "workload-token", "WORKLOAD_TOKEN")

	cfg := reconciler.Config{
		Mode:          mode,
		Upgrade:       upgrade,
		Reconfigure:   reconfigure,
		GatusURL:      gatusURL,
		GatusToken:    gatusToken,
		NtfyURL:       ntfyURL,
		NtfyTopic:     ntfyTopic,
		NtfyToken:     ntfyToken,
		ApproveURL:    approveURL,
		WorkloadToken: workloadToken,
	}
	locks := reconciler.ConfigLocks{
		Mode:          modeLocked,
		Upgrade:       upgradeLocked,
		Reconfigure:   reconfigureLocked,
		GatusURL:      gatusURLLocked,
		GatusToken:    gatusTokenLocked,
		NtfyURL:       ntfyURLLocked,
		NtfyTopic:     ntfyTopicLocked,
		NtfyToken:     ntfyTokenLocked,
		ApproveURL:    approveURLLocked,
		WorkloadToken: workloadTokenLocked,
	}
	return cfg, locks, nil
}

func resolveString(cmd *cobra.Command, flagName, envName string) (string, bool) {
	flag := cmd.Flags().Lookup(flagName)
	if flag != nil && flag.Changed {
		if value, err := cmd.Flags().GetString(flagName); err == nil {
			return value, true
		}
	}
	if value, ok := os.LookupEnv(envName); ok {
		return value, false
	}
	return "", false
}

func resolveBool(cmd *cobra.Command, flagName, envName string) (bool, bool) {
	flag := cmd.Flags().Lookup(flagName)
	if flag != nil && flag.Changed {
		if value, err := cmd.Flags().GetBool(flagName); err == nil {
			return value, true
		}
	}
	if value, ok := os.LookupEnv(envName); ok {
		parsed, err := strconv.ParseBool(value)
		if err == nil {
			return parsed, false
		}
	}
	return false, false
}
