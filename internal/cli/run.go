// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"io"
	"log/slog"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/farzan-kh/wright/internal/agent/llm"
	"github.com/farzan-kh/wright/internal/agent/llm/claude"
	llmlogging "github.com/farzan-kh/wright/internal/agent/llm/logging"
	"github.com/farzan-kh/wright/internal/agent/llm/openrouter"
	"github.com/farzan-kh/wright/internal/agent/llm/retrying"
	"github.com/farzan-kh/wright/internal/cache"
	"github.com/farzan-kh/wright/internal/config"
	"github.com/farzan-kh/wright/internal/gate"
	"github.com/farzan-kh/wright/internal/logging"
	"github.com/farzan-kh/wright/internal/pipeline"
	"github.com/farzan-kh/wright/internal/poller"
	"github.com/farzan-kh/wright/internal/sandbox"
)

func newRunCmd() *cobra.Command {
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run one full Phase-1 pipeline pass over one repo",
		Long: "Load config, poll issues with the trigger label, run the triage gate, and\n" +
			"process each issue sequentially through sandbox, agent, verifier, and git PR flow.",
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
			p, repo, providerToken, err := buildProvider(rc, logging.FromContext(cmd.Context()))
			if err != nil {
				return err
			}
			llmProvider, err := buildLLM(rc, logging.FromContext(cmd.Context()))
			if err != nil {
				return err
			}
			sb, err := sandbox.NewDocker(rc.Retry.ToRetryConfig(), logging.FromContext(cmd.Context()))
			if err != nil {
				return err
			}
			exec := &issueExecutor{
				Provider:      p,
				Repo:          repo,
				RepoConfig:    rc,
				ProviderToken: providerToken,
				LLM:           llmProvider,
				Sandbox:       sb,
				Cache:         &cache.FileStore{Dir: cfg.Cache.Dir},
			}

			pl := &pipeline.Pipeline{
				Provider:        p,
				Repo:            repo,
				TriggerLabel:    rc.TriggerLabel,
				NeedsHumanLabel: "needs-human",
				Poller:          &poller.Poller{Provider: p, Repo: repo, Label: rc.TriggerLabel},
				Gate: &gate.Gate{
					LLM: llmProvider, Model: rc.LLM.GateModel, MaxTokens: 512,
					Provider: p, Repo: repo, MaxToolTurns: gate.DefaultMaxToolTurns,
				},
				OnReady: exec.Handle,
			}
			reports, err := pl.RunOnce(cmd.Context())
			if err != nil {
				return err
			}
			printRunReports(cmd.OutOrStdout(), rc, reports)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "full path of the repo to run against (required if the config has more than one)")
	return cmd
}

// buildLLM constructs the appropriate llm.LLMProvider from the repo's LLM
// config. log receives structured logging of every LLM call; pass
// logging.FromContext(cmd.Context()) to honor --verbose.
func buildLLM(rc *config.RepoConfig, log *slog.Logger) (llm.LLMProvider, error) {
	switch rc.LLM.Provider {
	case config.LLMProviderOpenRouter:
		return buildOpenRouterLLM(rc, log)
	default: // "claude" and any unrecognised value are routed to Claude.
		return buildClaudeLLM(rc, log)
	}
}

func buildClaudeLLM(rc *config.RepoConfig, log *slog.Logger) (llm.LLMProvider, error) {
	if rc.LLM.Provider != config.LLMProviderClaude && rc.LLM.Provider != "" {
		return nil, fmt.Errorf("unsupported llm provider %q", rc.LLM.Provider)
	}
	switch rc.LLM.Auth {
	case "oauth":
		// The config schema and the Claude adapter both carry OAuth/subscription
		// support, but it is not activated in Phase 1: subscription tokens require
		// presenting the Claude Code identity to the API, which is a usage-terms
		// decision deferred to Phase 2. Fail fast with a clear message rather than
		// hitting an opaque API rejection mid-run.
		return nil, fmt.Errorf("llm.auth \"oauth\" (Claude subscription) is not supported in Phase 1; use auth: api_key (OAuth support is deferred to Phase 2)")
	default:
		key, _, ok := rc.LLM.ResolveAPIKey()
		if !ok {
			return nil, fmt.Errorf("no llm api key: set one of %v", rc.LLM.APIKeyEnvCandidates())
		}
		c, err := claude.New(claude.Config{AuthMode: claude.AuthModeAPIKey, APIKey: key})
		if err != nil {
			return nil, err
		}
		return retrying.New(llmlogging.New(c, log, "claude"), rc.Retry.ToRetryConfig()), nil
	}
}

func buildOpenRouterLLM(rc *config.RepoConfig, log *slog.Logger) (llm.LLMProvider, error) {
	key, _, ok := rc.LLM.ResolveAPIKey()
	if !ok {
		return nil, fmt.Errorf("no openrouter api key: set one of %v", rc.LLM.APIKeyEnvCandidates())
	}
	c, err := openrouter.New(openrouter.Config{APIKey: key})
	if err != nil {
		return nil, err
	}
	return retrying.New(llmlogging.New(c, log, "openrouter"), rc.Retry.ToRetryConfig()), nil
}

func printRunReports(w io.Writer, rc *config.RepoConfig, reports []pipeline.IssueReport) {
	fmt.Fprintf(w, "%s %s | trigger=%q\n", rc.Provider, rc.Repo, rc.TriggerLabel)
	if len(reports) == 0 {
		fmt.Fprintln(w, "  (no matching issues)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tSTATUS\tDETAIL\tTURNS\tTOKENS(in/out)")
	for _, r := range reports {
		tokens := fmt.Sprintf("%d/%d", r.Cost.Usage.InputTokens, r.Cost.Usage.OutputTokens)
		fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%s\n", r.IssueNumber, r.Status, r.Detail, r.Cost.Turns, tokens)
	}
	_ = tw.Flush()
}
