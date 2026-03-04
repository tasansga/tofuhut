package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"tofuhut/internal/reconciler"
)

var rootCmd = &cobra.Command{
	Use:           "tofuhut",
	Short:         "Tofuhut OpenTofu reconciler",
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if isPrintCommand(cmd) {
			return nil
		}
		cfg, err := resolveConfig(cmd)
		if err != nil {
			return err
		}
		resolvedConfig = cfg
		return nil
	},
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

// ExitCodeError wraps an error with a specific exit code.
type ExitCodeError struct {
	Code int
	Err  error
}

func (e *ExitCodeError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *ExitCodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func init() {
	rootCmd.PersistentFlags().String("gatus-cli-url", "", "Gatus CLI URL (env GATUS_CLI_URL)")
	rootCmd.PersistentFlags().String("gatus-cli-token", "", "Gatus CLI token (env GATUS_CLI_TOKEN)")
	rootCmd.PersistentFlags().String("mode", "", "Run mode: plan or apply (env MODE)")
	rootCmd.PersistentFlags().Bool("upgrade", false, "Pass -upgrade to tofu init (env UPGRADE)")
	rootCmd.PersistentFlags().Bool("reconfigure", false, "Pass -reconfigure to tofu init (env RECONFIGURE)")

	rootCmd.AddCommand(workloadCmd)
}

var resolvedConfig reconciler.Config

func isPrintCommand(cmd *cobra.Command) bool {
	path := cmd.CommandPath()
	return path == "tofuhut print" || strings.HasPrefix(path, "tofuhut print ")
}

func resolveConfig(cmd *cobra.Command) (reconciler.Config, error) {
	mode := resolveString(cmd, "mode", "MODE")
	if mode == "" {
		mode = "plan"
	}
	if mode != "plan" && mode != "apply" {
		return reconciler.Config{}, &ExitCodeError{Code: 2, Err: fmt.Errorf("invalid MODE %q: must be plan or apply", mode)}
	}

	return reconciler.Config{
		Mode:        mode,
		Upgrade:     resolveBool(cmd, "upgrade", "UPGRADE"),
		Reconfigure: resolveBool(cmd, "reconfigure", "RECONFIGURE"),
		GatusURL:    resolveString(cmd, "gatus-cli-url", "GATUS_CLI_URL"),
		GatusToken:  resolveString(cmd, "gatus-cli-token", "GATUS_CLI_TOKEN"),
	}, nil
}

func resolveString(cmd *cobra.Command, flagName, envName string) string {
	flag := cmd.Flags().Lookup(flagName)
	if flag != nil && flag.Changed {
		if value, err := cmd.Flags().GetString(flagName); err == nil {
			return value
		}
	}
	if value, ok := os.LookupEnv(envName); ok {
		return value
	}
	return ""
}

func resolveBool(cmd *cobra.Command, flagName, envName string) bool {
	flag := cmd.Flags().Lookup(flagName)
	if flag != nil && flag.Changed {
		if value, err := cmd.Flags().GetBool(flagName); err == nil {
			return value
		}
	}
	if value, ok := os.LookupEnv(envName); ok {
		parsed, err := strconv.ParseBool(value)
		if err == nil {
			return parsed
		}
	}
	return false
}
