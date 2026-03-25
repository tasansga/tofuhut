package reconciler

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"
)

var workloadNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// WorkloadSpec describes a workload for scheduling.
type WorkloadSpec struct {
	Name     string
	Interval time.Duration
	Enabled  bool
}

// LoadWorkloadSpecs discovers workloads and applies scheduling defaults.
func LoadWorkloadSpecs(defaultInterval time.Duration, paths Paths) ([]WorkloadSpec, error) {
	entries, err := os.ReadDir(paths.RuntimeDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list workloads in %s: %w", paths.RuntimeDir, err)
	}

	specs := make([]WorkloadSpec, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if err := validateWorkloadName(name); err != nil {
			continue
		}

		envFile := paths.EnvFilePath(name)
		envFromFile, err := LoadEnvFile(envFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load env for %s: %w", name, err)
		}

		enabled := true
		if value, ok := envFromFile["RECONCILE_ENABLED"]; ok {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return nil, fmt.Errorf("invalid RECONCILE_ENABLED for %s: %w", name, err)
			}
			enabled = parsed
		}

		interval := defaultInterval
		if value, ok := envFromFile["RECONCILE_INTERVAL"]; ok && value != "" {
			parsed, err := time.ParseDuration(value)
			if err != nil {
				return nil, fmt.Errorf("invalid RECONCILE_INTERVAL for %s: %w", name, err)
			}
			interval = parsed
		}

		if interval <= 0 {
			enabled = false
		}

		specs = append(specs, WorkloadSpec{
			Name:     name,
			Interval: interval,
			Enabled:  enabled,
		})
	}

	return specs, nil
}

func validateWorkloadName(name string) error {
	if name == "" {
		return fmt.Errorf("empty workload")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("invalid workload name")
	}
	if !workloadNamePattern.MatchString(name) {
		return fmt.Errorf("invalid workload name")
	}
	return nil
}
