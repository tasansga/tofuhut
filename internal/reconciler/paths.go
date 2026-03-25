package reconciler

import (
	"fmt"
	"path/filepath"
)

const (
	DefaultWorkloadConfigDir  = "/etc/tofuhut/workloads"
	DefaultWorkloadRuntimeDir = "/var/lib/tofuhut/workloads"
)

// Paths holds filesystem roots for workload env/config and runtime state.
type Paths struct {
	ConfigDir  string
	RuntimeDir string
}

// Validate ensures all configured paths are absolute and normalized.
func (p Paths) Validate() error {
	if p.ConfigDir == "" {
		return fmt.Errorf("workload config dir is required")
	}
	if p.RuntimeDir == "" {
		return fmt.Errorf("workload runtime dir is required")
	}
	if !filepath.IsAbs(p.ConfigDir) {
		return fmt.Errorf("workload config dir must be absolute: %s", p.ConfigDir)
	}
	if !filepath.IsAbs(p.RuntimeDir) {
		return fmt.Errorf("workload runtime dir must be absolute: %s", p.RuntimeDir)
	}
	if filepath.Clean(p.ConfigDir) != p.ConfigDir {
		return fmt.Errorf("workload config dir must be normalized: %s", p.ConfigDir)
	}
	if filepath.Clean(p.RuntimeDir) != p.RuntimeDir {
		return fmt.Errorf("workload runtime dir must be normalized: %s", p.RuntimeDir)
	}
	return nil
}

// EnvFilePath returns the workload env file path.
func (p Paths) EnvFilePath(workload string) string {
	return filepath.Join(p.ConfigDir, workload+".env")
}

// WorkDirPath returns the workload working directory path.
func (p Paths) WorkDirPath(workload string) string {
	return filepath.Join(p.RuntimeDir, workload)
}
