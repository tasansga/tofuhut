package cmd

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"tofuhut/internal/reconciler"
)

var workloadCmd = &cobra.Command{
	Use:   "workload",
	Short: "Manage OpenTofu workloads",
}

var workloadRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Run the OpenTofu reconciler for a workload",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if name == "" {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("workload name is required")}
		}

		envFile := reconciler.EnvFilePath(name)
		envFromFile, err := reconciler.LoadEnvFile(envFile)
		if err != nil {
			return &ExitCodeError{Code: 1, Err: err}
		}

		mergedConfig, err := reconciler.MergeConfig(resolvedConfig, resolvedConfigLocks, envFromFile)
		if err != nil {
			return &ExitCodeError{Code: 2, Err: err}
		}

		logrus.WithFields(logrus.Fields{
			"workload":        name,
			"mode":            mergedConfig.Mode,
			"upgrade":         mergedConfig.Upgrade,
			"reconfigure":     mergedConfig.Reconfigure,
			"gatus_cli_url":   mergedConfig.GatusURL,
			"gatus_has_token": mergedConfig.GatusToken != "",
		}).Debug("starting workload run")

		if err := reconciler.Run(name, mergedConfig, envFile, envFromFile); err != nil {
			logrus.Error(err)
			if exitErr, ok := err.(*reconciler.ExitCodeError); ok {
				return &ExitCodeError{Code: exitErr.Code, Err: exitErr}
			}
			return &ExitCodeError{Code: 1, Err: err}
		}
		logrus.WithField("workload", name).Info("workload run completed successfully")
		return nil
	},
}

func init() {
	workloadCmd.AddCommand(workloadRunCmd)
}
