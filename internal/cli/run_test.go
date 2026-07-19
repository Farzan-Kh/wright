// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/farzan-kh/wright/internal/config"
	"github.com/farzan-kh/wright/internal/logging"
)

func TestBuildLLMRejectsOAuthInPhase1(t *testing.T) {
	rc := &config.RepoConfig{
		LLM: config.LLMConfig{Provider: config.LLMProviderClaude, Auth: "oauth"},
	}
	_, err := buildLLM(rc, logging.FromContext(context.Background()))
	if err == nil {
		t.Fatal("buildLLM(oauth) = nil error, want a Phase 1 not-supported error")
	}
	if !strings.Contains(err.Error(), "not supported in Phase 1") || !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("error = %q, want it to mention Phase 1 and api_key", err)
	}
}
