package cmd

import "github.com/spf13/cobra"

var workloadPrintCmd = &cobra.Command{
	Use:   "print",
	Short: "Print workload templates",
}

var workloadPrintSystemdServiceCmd = &cobra.Command{
	Use:   "systemd-service",
	Short: "Print the systemd service template",
	RunE: func(cmd *cobra.Command, args []string) error {
		return printEmbedded(cmd.OutOrStdout(), "tofuhut-workload@.service")
	},
}

var workloadPrintSystemdTimerCmd = &cobra.Command{
	Use:   "systemd-timer",
	Short: "Print the systemd timer template",
	RunE: func(cmd *cobra.Command, args []string) error {
		return printEmbedded(cmd.OutOrStdout(), "tofuhut-workload@.timer")
	},
}

var workloadPrintEnvCmd = &cobra.Command{
	Use:   "env",
	Short: "Print the workload env template",
	RunE: func(cmd *cobra.Command, args []string) error {
		return printEmbedded(cmd.OutOrStdout(), "tofuhut-workload.env")
	},
}

func init() {
	workloadPrintCmd.AddCommand(workloadPrintSystemdServiceCmd)
	workloadPrintCmd.AddCommand(workloadPrintSystemdTimerCmd)
	workloadPrintCmd.AddCommand(workloadPrintEnvCmd)
}
