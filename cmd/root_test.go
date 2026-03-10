package cmd

import (
	"testing"

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
