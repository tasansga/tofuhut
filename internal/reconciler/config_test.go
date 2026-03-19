package reconciler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeConfigDefaults(t *testing.T) {
	cfg, err := MergeConfig(Config{}, ConfigLocks{}, map[string]string{})
	assert.Error(t, err)
	assert.Equal(t, "", cfg.WorkloadType)
}

func TestMergeConfigEnvOverridesWhenUnlocked(t *testing.T) {
	cfg := Config{Mode: "plan"}
	env := map[string]string{
		"WORKLOAD_TYPE": "ansible",
		"MODE":          "apply",
		"UPGRADE":       "true",
		"RECONFIGURE":   "true",
		"GATUS_CLI_URL": "http://example",
	}
	merged, err := MergeConfig(cfg, ConfigLocks{}, env)
	assert.NoError(t, err)
	assert.Equal(t, "ansible", merged.WorkloadType)
	assert.Equal(t, "apply", merged.Mode)
	assert.True(t, merged.Upgrade)
	assert.True(t, merged.Reconfigure)
	assert.Equal(t, "http://example", merged.GatusURL)
}

func TestMergeConfigEnvIgnoredWhenLocked(t *testing.T) {
	cfg := Config{WorkloadType: "tofu", Mode: "plan", Upgrade: false}
	locks := ConfigLocks{WorkloadType: true, Mode: true, Upgrade: true}
	env := map[string]string{"MODE": "apply", "UPGRADE": "true"}
	merged, err := MergeConfig(cfg, locks, env)
	assert.NoError(t, err)
	assert.Equal(t, "plan", merged.Mode)
	assert.False(t, merged.Upgrade)
}

func TestMergeConfigInvalidMode(t *testing.T) {
	env := map[string]string{"WORKLOAD_TYPE": "tofu", "MODE": "invalid"}
	_, err := MergeConfig(Config{}, ConfigLocks{}, env)
	assert.Error(t, err)
}

func TestMergeConfigInvalidWorkloadType(t *testing.T) {
	env := map[string]string{"WORKLOAD_TYPE": "invalid"}
	_, err := MergeConfig(Config{}, ConfigLocks{}, env)
	assert.Error(t, err)
}

func TestMergeConfigAutoApply(t *testing.T) {
	env := map[string]string{"WORKLOAD_TYPE": "tofu", "MODE": "auto-apply"}
	merged, err := MergeConfig(Config{}, ConfigLocks{}, env)
	assert.NoError(t, err)
	assert.Equal(t, "auto-apply", merged.Mode)
}

func TestMergeConfigDNSControlWorkloadType(t *testing.T) {
	env := map[string]string{"WORKLOAD_TYPE": "dnscontrol", "MODE": "plan"}
	merged, err := MergeConfig(Config{}, ConfigLocks{}, env)
	assert.NoError(t, err)
	assert.Equal(t, "dnscontrol", merged.WorkloadType)
}
