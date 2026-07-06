package cli

import (
	"fmt"
	"io"
	"log/slog"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/farzan-kh/wright/internal/config"
	"github.com/farzan-kh/wright/internal/logging"
	"github.com/farzan-kh/wright/internal/provider"
	"github.com/farzan-kh/wright/internal/provider/factory"
)

func newOnceCmd() *cobra.Command {
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "once",
		Short: "Run once against one repo: prove access and list its labeled issues",
		Long: "Load the config, select a repo (via --repo or the sole entry), construct its\n" +
			"provider, confirm access by fetching the default branch, and list the open\n" +
			"issues carrying the trigger label. The gate and agent run in Phase 1.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath(cmd))
			if err != nil {
				return err
			}
			rc, err := cfg.SelectRepo(repoFlag)
			if err != nil {
				return err
			}

			p, repo, _, err := buildProvider(rc, logging.FromContext(cmd.Context()))
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			base, err := p.DefaultBranch(ctx, repo)
			if err != nil {
				return fmt.Errorf("accessing %s: %w", rc.Repo, err)
			}
			issues, err := p.ListLabeledIssues(ctx, repo, rc.TriggerLabel)
			if err != nil {
				return err
			}

			printIssues(cmd.OutOrStdout(), rc, base, issues)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "full path of the repo to run against (required if the config has more than one)")
	return cmd
}

// buildProvider resolves a repo's token and constructs its provider, returning
// the provider, the domain Repo to address it with, and the resolved token
// (so callers that also need it, e.g. for git credential injection, don't
// have to re-resolve it themselves). log receives structured logging of every
// provider call; pass logging.FromContext(cmd.Context()) to honor --verbose.
func buildProvider(rc *config.RepoConfig, log *slog.Logger) (provider.Provider, provider.Repo, string, error) {
	token, _, ok := rc.ResolveToken()
	if !ok {
		return nil, provider.Repo{}, "", fmt.Errorf("no token for %s: set one of %v", rc.Repo, rc.TokenEnvCandidates())
	}
	p, err := factory.New(*rc, token, log)
	if err != nil {
		return nil, provider.Repo{}, "", err
	}
	return p, provider.Repo{FullPath: rc.Repo}, token, nil
}

func printIssues(w io.Writer, rc *config.RepoConfig, baseBranch string, issues []provider.Issue) {
	fmt.Fprintf(w, "%s %s (default branch: %s)\n", rc.Provider, rc.Repo, baseBranch)
	fmt.Fprintf(w, "open issues labeled %q:\n\n", rc.TriggerLabel)

	if len(issues) == 0 {
		fmt.Fprintln(w, "  (none)")
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  #\tTITLE\tLABELS\tUPDATED\tURL")
		for _, iss := range issues {
			fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\t%s\n",
				iss.Number,
				truncate(iss.Title, 50),
				joinLabels(iss.Labels),
				iss.UpdatedAt.Format(time.RFC3339),
				iss.URL,
			)
		}
		_ = tw.Flush()
	}

	fmt.Fprintf(w, "\n%d issue(s). Triage gate and agent run in Phase 1.\n", len(issues))
}

func joinLabels(labels []string) string {
	if len(labels) == 0 {
		return "-"
	}
	s := labels[0]
	for _, l := range labels[1:] {
		s += "," + l
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return safeCut(s, n)
	}
	return safeCut(s, n-1) + "…"
}

// safeCut returns the first n bytes of s, pulled back to the nearest earlier
// rune boundary so a multi-byte UTF-8 character is never split in half.
func safeCut(s string, n int) string {
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
