package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"tofuhut/internal/files"
)

var printCmd = &cobra.Command{
	Use:   "print",
	Short: "Print embedded templates",
}

var printSystemdServiceCmd = &cobra.Command{
	Use:   "systemd-service",
	Short: "Print the systemd service template",
	RunE: func(cmd *cobra.Command, args []string) error {
		return printEmbedded(cmd.OutOrStdout(), "tofuhut-workload@.service")
	},
}

var printSystemdTimerCmd = &cobra.Command{
	Use:   "systemd-timer",
	Short: "Print the systemd timer template",
	RunE: func(cmd *cobra.Command, args []string) error {
		return printEmbedded(cmd.OutOrStdout(), "tofuhut-workload@.timer")
	},
}

var printWorkloadEnvCmd = &cobra.Command{
	Use:   "workload-env",
	Short: "Print the workload env template",
	RunE: func(cmd *cobra.Command, args []string) error {
		return printEmbedded(cmd.OutOrStdout(), "tofuhut-workload.env")
	},
}

func printEmbedded(w io.Writer, name string) error {
	content, err := files.Read(name)
	if err != nil {
		return fmt.Errorf("read embedded file %s: %w", name, err)
	}
	_, err = io.WriteString(w, content)
	return err
}

func init() {
	printCmd.AddCommand(printSystemdServiceCmd)
	printCmd.AddCommand(printSystemdTimerCmd)
	printCmd.AddCommand(printWorkloadEnvCmd)
	rootCmd.AddCommand(printCmd)
}
