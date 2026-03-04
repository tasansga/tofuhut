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

		logrus.WithFields(logrus.Fields{
			"workload":        name,
			"mode":            resolvedConfig.Mode,
			"upgrade":         resolvedConfig.Upgrade,
			"reconfigure":     resolvedConfig.Reconfigure,
			"gatus_cli_url":   resolvedConfig.GatusURL,
			"gatus_has_token": resolvedConfig.GatusToken != "",
		}).Debug("starting workload run")

		if err := reconciler.Run(name, resolvedConfig); err != nil {
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
