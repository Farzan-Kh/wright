// Package cli implements the wright command-line interface using cobra.
package cli

import (
	"context"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
)

// defaultConfigPath is used when --config is not given.
const defaultConfigPath = "wright.yaml"

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
	root := &cobra.Command{
		Use:   "wright",
		Short: "Wright resolves labeled issues with an LLM agent and opens PRs.",
		Long: "Wright is a self-hosted daemon that resolves labeled, well-scoped GitHub and\n" +
			"GitLab issues with an LLM agent and opens pull requests. This is the Phase 0\n" +
			"foundation: config, the provider abstraction, and run-once commands.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.PersistentFlags().String("config", defaultConfigPath, "path to the wright config file")

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
