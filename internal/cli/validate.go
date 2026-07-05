package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/farzan-kh/wright/internal/config"
)

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate the config and check that resolved token env vars are set",
		Long: "Load and validate the config file, then confirm that each repo can resolve a\n" +
			"token from its environment variables. Fully offline; makes no API calls.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := configPath(cmd)
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			var missing []string
			for i := range cfg.Repos {
				rc := &cfg.Repos[i]
				if _, name, ok := rc.ResolveToken(); ok {
					fmt.Fprintf(out, "ok   %-8s %-40s provider token from %s\n", rc.Provider, rc.Repo, name)
				} else {
					fmt.Fprintf(out, "MISS %-8s %-40s no provider token set\n", rc.Provider, rc.Repo)
					missing = append(missing, fmt.Sprintf("%s provider token: set one of %s", rc.Repo, strings.Join(rc.TokenEnvCandidates(), ", ")))
				}

				switch rc.LLM.Auth {
				case "oauth":
					fmt.Fprintf(out, "MISS %-8s %-40s llm.auth oauth is not supported in Phase 1\n", rc.Provider, rc.Repo)
					missing = append(missing, fmt.Sprintf("%s llm.auth \"oauth\" (Claude subscription) is not supported in Phase 1; use auth: api_key (OAuth support is deferred to Phase 2)", rc.Repo))
				default:
					if _, name, ok := rc.LLM.ResolveAPIKey(); ok {
						fmt.Fprintf(out, "ok   %-8s %-40s llm api key from %s\n", rc.Provider, rc.Repo, name)
					} else {
						fmt.Fprintf(out, "MISS %-8s %-40s no llm api key set\n", rc.Provider, rc.Repo)
						missing = append(missing, fmt.Sprintf("%s llm api key: set one of %s", rc.Repo, strings.Join(rc.LLM.APIKeyEnvCandidates(), ", ")))
					}
				}
			}

			if len(missing) > 0 {
				return fmt.Errorf("missing token(s) for %d repo(s):\n  %s", len(missing), strings.Join(missing, "\n  "))
			}
			fmt.Fprintf(out, "\nconfig %s is valid; %d repo(s) configured\n", path, len(cfg.Repos))
			return nil
		},
	}
}
