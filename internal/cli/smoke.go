package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/farzan-kh/patchr/internal/config"
	"github.com/farzan-kh/patchr/internal/provider"
)

func newSmokeCmd() *cobra.Command {
	var repoFlag string
	var keep bool
	cmd := &cobra.Command{
		Use:   "smoke",
		Short: "Exercise the full write path against a scratch repo",
		Long: "Run the whole write path against a scratch repo you designate: create a\n" +
			"branch, push a commit, open a draft PR, comment on it, then clean up (close\n" +
			"the PR and delete the branch, or merge if the repo's config sets auto_merge).\n\n" +
			"This WRITES to the target repo. It refuses to run without an explicit --repo,\n" +
			"so point it only at a throwaway repo. Use --keep to leave the artifacts for\n" +
			"inspection instead of cleaning up.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if repoFlag == "" {
				return fmt.Errorf("smoke: --repo is required (it writes to the repo; use a scratch repo)")
			}
			cfg, err := config.Load(configPath(cmd))
			if err != nil {
				return err
			}
			rc, err := cfg.SelectRepo(repoFlag)
			if err != nil {
				return err
			}
			p, repo, err := buildProvider(rc)
			if err != nil {
				return err
			}
			return runSmoke(cmd.Context(), cmd.OutOrStdout(), p, rc, repo, keep)
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "REQUIRED: full path of a scratch repo to write to")
	cmd.Flags().BoolVar(&keep, "keep", false, "skip cleanup (leave the branch and PR for inspection)")
	return cmd
}

// runSmoke performs the branch -> commit -> draft PR -> comment -> cleanup
// sequence, printing each step. If a step fails after the branch is created and
// --keep is not set, it best-effort deletes the branch so nothing is left
// behind.
func runSmoke(ctx context.Context, out io.Writer, p provider.Provider, rc *config.RepoConfig, repo provider.Repo, keep bool) (err error) {
	ts := time.Now().UTC().Format("20060102-150405")
	branch := "patchr/smoke-" + ts

	base := rc.BaseBranch
	if base == "" {
		base, err = p.DefaultBranch(ctx, repo)
		if err != nil {
			return fmt.Errorf("resolve base branch: %w", err)
		}
	}
	fmt.Fprintf(out, "smoke on %s %s (base: %s)\n", rc.Provider, rc.Repo, base)

	fmt.Fprintf(out, "  1/5 create branch %s\n", branch)
	if err = p.CreateBranch(ctx, repo, branch, base); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}

	// From here on, clean the branch up on failure unless the user asked to keep.
	branchCreated := true
	defer func() {
		if err != nil && branchCreated && !keep {
			fmt.Fprintf(out, "  cleanup: deleting branch %s after failure\n", branch)
			if delErr := p.DeleteBranch(ctx, repo, branch); delErr != nil {
				fmt.Fprintf(out, "  cleanup: delete branch failed: %v\n", delErr)
			}
		}
	}()

	fmt.Fprintf(out, "  2/5 push commit\n")
	commits := []provider.Commit{{
		Message: "Patchr smoke test " + ts,
		Files: []provider.CommitFile{{
			Path:    "patchr-smoke/" + ts + ".md",
			Content: smokeFileContent(ts),
		}},
	}}
	if _, err = p.PushCommits(ctx, repo, branch, commits); err != nil {
		return fmt.Errorf("push commit: %w", err)
	}

	fmt.Fprintf(out, "  3/5 open draft PR\n")
	pr, err := p.OpenPullRequest(ctx, repo, provider.PullRequestSpec{
		Title:      "Patchr smoke test " + ts,
		Body:       "Automated smoke test from `patchr smoke`. Safe to close/delete.",
		HeadBranch: branch,
		BaseBranch: base,
		Draft:      true,
	})
	if err != nil {
		return fmt.Errorf("open pull request: %w", err)
	}
	fmt.Fprintf(out, "      -> #%d %s\n", pr.Number, pr.URL)

	fmt.Fprintf(out, "  4/5 comment on PR\n")
	if err = p.CommentOnPullRequest(ctx, repo, pr.Number, "Patchr smoke test comment. This PR was opened automatically and will be cleaned up."); err != nil {
		return fmt.Errorf("comment on pull request: %w", err)
	}

	if keep {
		fmt.Fprintf(out, "  5/5 --keep set: leaving branch %s and PR #%d for inspection\n", branch, pr.Number)
		fmt.Fprintln(out, "smoke OK (artifacts kept)")
		return nil
	}

	fmt.Fprintf(out, "  5/5 cleanup\n")
	if rc.AutoMerge {
		fmt.Fprintf(out, "      auto_merge is set: merging PR #%d and deleting branch\n", pr.Number)
		if err = p.MergePullRequest(ctx, repo, pr.Number, provider.MergeOptions{DeleteBranch: true}); err != nil {
			return fmt.Errorf("merge pull request: %w", err)
		}
	} else {
		fmt.Fprintf(out, "      closing PR #%d and deleting branch\n", pr.Number)
		if err = p.ClosePullRequest(ctx, repo, pr.Number); err != nil {
			return fmt.Errorf("close pull request: %w", err)
		}
		if err = p.DeleteBranch(ctx, repo, branch); err != nil {
			return fmt.Errorf("delete branch: %w", err)
		}
	}
	branchCreated = false // cleaned up successfully; nothing for the deferred handler to do

	fmt.Fprintln(out, "smoke OK")
	return nil
}

func smokeFileContent(ts string) string {
	return "# Patchr smoke test\n\n" +
		"This file was created by `patchr smoke` at " + ts + " (UTC).\n" +
		"It exercises the provider write path and is safe to delete.\n"
}
