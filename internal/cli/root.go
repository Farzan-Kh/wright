// SPDX-License-Identifier: Apache-2.0

// Package cli implements the wright command-line interface using cobra.
package cli

import (
	"context"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"github.com/farzan-kh/wright/internal/logging"
)

// defaultConfigPath is used when --config is not given.
const defaultConfigPath = "wright.yaml"

// defaultLogPath is where diagnostic logging is written when --verbose is set.
const defaultLogPath = "wright.log"

// Execute builds and runs the root command, returning a process exit code.
// Errors are printed by cobra; this only maps success/failure to 0/1.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		return 1
	}
	return 0
}

func newRootCmd() *cobra.Command {
	var closeLog func() error

	root := &cobra.Command{
		Use:   "wright",
		Short: "Wright resolves labeled issues with an LLM agent and opens PRs.",
		Long: "Wright is a self-hosted daemon that resolves labeled, well-scoped GitHub and\n" +
			"GitLab issues with an LLM agent and opens pull requests. This is the Phase 0\n" +
			"foundation: config, the provider abstraction, and run-once commands.",
		SilenceUsage:  true,
		SilenceErrors: false,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			verbose, _ := cmd.Flags().GetBool("verbose")
			logPath, _ := cmd.Flags().GetString("log-file")
			logger, closer, err := logging.New(verbose, logPath)
			if err != nil {
				return err
			}
			closeLog = closer
			cmd.SetContext(logging.WithLogger(cmd.Context(), logger))
			return nil
		},
		PersistentPostRunE: func(*cobra.Command, []string) error {
			if closeLog != nil {
				return closeLog()
			}
			return nil
		},
	}
	root.PersistentFlags().String("config", defaultConfigPath, "path to the wright config file")
	root.PersistentFlags().BoolP("verbose", "v", false, "log detailed provider/GitHub/GitLab call activity to --log-file")
	root.PersistentFlags().String("log-file", defaultLogPath, "file to write verbose logs to (only used with --verbose)")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newValidateCmd())
	root.AddCommand(newOnceCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newSmokeCmd())
	return root
}

// configPath reads the inherited --config flag.
func configPath(cmd *cobra.Command) string {
	path, _ := cmd.Flags().GetString("config")
	if path == "" {
		return defaultConfigPath
	}
	return path
}
