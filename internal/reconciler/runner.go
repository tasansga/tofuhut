package reconciler

import (
	"context"

	"github.com/sirupsen/logrus"
)

// Runner executes reconciliation for a workload.
type Runner interface {
	Run(ctx context.Context, workload string) error
}

// DefaultRunner runs the built-in reconciler flow.
type DefaultRunner struct {
	BaseConfig Config
	Locks      ConfigLocks
}

// NewDefaultRunner builds a runner using resolved config and locks.
func NewDefaultRunner(cfg Config, locks ConfigLocks) *DefaultRunner {
	return &DefaultRunner{BaseConfig: cfg, Locks: locks}
}

// Run executes a single reconciliation run for the workload.
func (r *DefaultRunner) Run(ctx context.Context, workload string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	envFile := EnvFilePath(workload)
	envFromFile, err := LoadEnvFile(envFile)
	if err != nil {
		return err
	}

	mergedConfig, err := MergeConfig(r.BaseConfig, r.Locks, envFromFile)
	if err != nil {
		return err
	}

	logrus.WithFields(logrus.Fields{
		"component":       "runner",
		"workload":        workload,
		"mode":            mergedConfig.Mode,
		"workload_type":   mergedConfig.WorkloadType,
		"upgrade":         mergedConfig.Upgrade,
		"reconfigure":     mergedConfig.Reconfigure,
		"gatus_cli_url":   mergedConfig.GatusURL,
		"gatus_has_token": mergedConfig.GatusToken != "",
	}).Debug("starting workload run")

	if err := RunWithContext(ctx, workload, mergedConfig, envFile, envFromFile); err != nil {
		return err
	}

	logrus.WithFields(logrus.Fields{
		"component": "runner",
		"workload":  workload,
	}).Info("workload run completed successfully")
	return nil
}

// Ensure DefaultRunner implements Runner.
var _ Runner = (*DefaultRunner)(nil)
