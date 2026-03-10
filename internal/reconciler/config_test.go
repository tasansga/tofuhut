package reconciler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeConfigDefaults(t *testing.T) {
	cfg, err := MergeConfig(Config{}, ConfigLocks{}, map[string]string{})
	assert.NoError(t, err)
	assert.Equal(t, "plan", cfg.Mode)
	assert.False(t, cfg.Upgrade)
	assert.False(t, cfg.Reconfigure)
}

func TestMergeConfigEnvOverridesWhenUnlocked(t *testing.T) {
	cfg := Config{Mode: "plan"}
	env := map[string]string{
		"MODE":          "apply",
		"UPGRADE":       "true",
		"RECONFIGURE":   "true",
		"GATUS_CLI_URL": "http://example",
	}
	merged, err := MergeConfig(cfg, ConfigLocks{}, env)
	assert.NoError(t, err)
	assert.Equal(t, "apply", merged.Mode)
	assert.True(t, merged.Upgrade)
	assert.True(t, merged.Reconfigure)
	assert.Equal(t, "http://example", merged.GatusURL)
}

func TestMergeConfigEnvIgnoredWhenLocked(t *testing.T) {
	cfg := Config{Mode: "plan", Upgrade: false}
	locks := ConfigLocks{Mode: true, Upgrade: true}
	env := map[string]string{"MODE": "apply", "UPGRADE": "true"}
	merged, err := MergeConfig(cfg, locks, env)
	assert.NoError(t, err)
	assert.Equal(t, "plan", merged.Mode)
	assert.False(t, merged.Upgrade)
}

func TestMergeConfigInvalidMode(t *testing.T) {
	env := map[string]string{"MODE": "invalid"}
	_, err := MergeConfig(Config{}, ConfigLocks{}, env)
	assert.Error(t, err)
}

func TestMergeConfigAutoApply(t *testing.T) {
	env := map[string]string{"MODE": "auto-apply"}
	merged, err := MergeConfig(Config{}, ConfigLocks{}, env)
	assert.NoError(t, err)
	assert.Equal(t, "auto-apply", merged.Mode)
}
