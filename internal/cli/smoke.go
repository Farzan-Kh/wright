package cli

import (
	"github.com/spf13/cobra"
)

// NOTE: implementation finalized in Step 7 (pending a decision on the comment
// step). This stub keeps the command registered so the CLI builds.
func newSmokeCmd() *cobra.Command {
	var repoFlag string
	var keep bool
	cmd := &cobra.Command{
		Use:    "smoke",
		Short:  "Exercise the full write path against a scratch repo",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "REQUIRED: full path of a scratch repo to write to")
	cmd.Flags().BoolVar(&keep, "keep", false, "skip cleanup (leave the branch and PR for inspection)")
	return cmd
}
