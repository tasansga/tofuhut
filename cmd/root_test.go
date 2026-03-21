package cmd

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func newTestCmd() *cobra.Command {
	c := &cobra.Command{Use: "tofuhut"}
	c.Flags().String("gatus-cli-url", "", "")
	c.Flags().String("gatus-cli-token", "", "")
	c.Flags().String("mode", "", "")
	c.Flags().Bool("upgrade", false, "")
	c.Flags().Bool("reconfigure", false, "")
	return c
}

func TestResolveConfigDefaults(t *testing.T) {
	cmd := newTestCmd()

	cfg, _, err := resolveConfig(cmd)
	assert.NoError(t, err)
	assert.Equal(t, "", cfg.Mode)
	assert.False(t, cfg.Upgrade)
	assert.False(t, cfg.Reconfigure)
	assert.Equal(t, "", cfg.GatusURL)
	assert.Equal(t, "", cfg.GatusToken)
}

func TestResolveConfigFlagOverridesEnv(t *testing.T) {
	t.Setenv("GATUS_CLI_URL", "env-url")
	t.Setenv("MODE", "apply")
	t.Setenv("UPGRADE", "true")

	cmd := newTestCmd()
	assert.NoError(t, cmd.Flags().Set("gatus-cli-url", "flag-url"))
	assert.NoError(t, cmd.Flags().Set("mode", "plan"))
	assert.NoError(t, cmd.Flags().Set("upgrade", "false"))

	cfg, _, err := resolveConfig(cmd)
	assert.NoError(t, err)
	assert.Equal(t, "flag-url", cfg.GatusURL)
	assert.Equal(t, "plan", cfg.Mode)
	assert.False(t, cfg.Upgrade)
}

func TestResolveConfigInvalidMode(t *testing.T) {
	t.Setenv("MODE", "invalid")
	cmd := newTestCmd()

	cfg, _, err := resolveConfig(cmd)
	assert.NoError(t, err)
	assert.Equal(t, "invalid", cfg.Mode)
}

func TestResolveBoolEnvParsing(t *testing.T) {
	t.Setenv("UPGRADE", "true")
	cmd := newTestCmd()

	cfg, _, err := resolveConfig(cmd)
	assert.NoError(t, err)
	assert.True(t, cfg.Upgrade)
}

func newTestLoggingCmd() *cobra.Command {
	c := &cobra.Command{Use: "tofuhut"}
	c.Flags().String("log-level", "", "")
	c.Flags().String("log-format", "", "")
	return c
}

func TestResolveLoggingConfigDefaults(t *testing.T) {
	cmd := newTestLoggingCmd()
	level, format, err := resolveLoggingConfig(cmd)
	assert.NoError(t, err)
	assert.Equal(t, "info", level)
	assert.Equal(t, "text", format)
}

func TestResolveLoggingConfigFlagOverridesEnv(t *testing.T) {
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("LOG_FORMAT", "text")

	cmd := newTestLoggingCmd()
	assert.NoError(t, cmd.Flags().Set("log-level", "debug"))
	assert.NoError(t, cmd.Flags().Set("log-format", "json"))

	level, format, err := resolveLoggingConfig(cmd)
	assert.NoError(t, err)
	assert.Equal(t, "debug", level)
	assert.Equal(t, "json", format)
}

func TestResolveLoggingConfigInvalidLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "invalid")
	cmd := newTestLoggingCmd()

	_, _, err := resolveLoggingConfig(cmd)
	assert.Error(t, err)
}

func TestResolveLoggingConfigInvalidFormat(t *testing.T) {
	t.Setenv("LOG_FORMAT", "yaml")
	cmd := newTestLoggingCmd()

	_, _, err := resolveLoggingConfig(cmd)
	assert.Error(t, err)
}

func TestConfigureLogging(t *testing.T) {
	oldLevel := logrus.GetLevel()
	oldFormatter := logrus.StandardLogger().Formatter
	t.Cleanup(func() {
		logrus.SetLevel(oldLevel)
		logrus.SetFormatter(oldFormatter)
	})

	assert.NoError(t, configureLogging("debug", "json"))
	assert.Equal(t, logrus.DebugLevel, logrus.GetLevel())
	_, ok := logrus.StandardLogger().Formatter.(*logrus.JSONFormatter)
	assert.True(t, ok)

	assert.NoError(t, configureLogging("info", "text"))
	assert.Equal(t, logrus.InfoLevel, logrus.GetLevel())
	_, ok = logrus.StandardLogger().Formatter.(*logrus.TextFormatter)
	assert.True(t, ok)
}
