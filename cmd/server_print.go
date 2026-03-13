package cmd

import "github.com/spf13/cobra"

var serverPrintCmd = &cobra.Command{
	Use:   "print",
	Short: "Print server templates",
}

var serverPrintSystemdServiceCmd = &cobra.Command{
	Use:   "systemd-service",
	Short: "Print the systemd service template",
	RunE: func(cmd *cobra.Command, args []string) error {
		return printEmbedded(cmd.OutOrStdout(), "tofuhut-server.service")
	},
}

func init() {
	serverPrintCmd.AddCommand(serverPrintSystemdServiceCmd)
	serverCmd.AddCommand(serverPrintCmd)
}
