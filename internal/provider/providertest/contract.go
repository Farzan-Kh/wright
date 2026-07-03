// Package providertest holds shared, provider-agnostic assertions that both the
// GitHub and GitLab adapter test suites run against their own httptest fakes.
// It deliberately does not stand up a fake itself — each adapter speaks a
// different wire format — but it pins down the semantics the domain interface
// promises, so the two adapters stay honest against the same contract.
package providertest

import (
	"errors"
	"slices"
	"testing"

	"github.com/farzan-kh/patchr/internal/provider"
)

// StandardCommits returns a fixed commit fixture used by both adapter suites.
// Feeding the identical input to each PushCommits lets every suite assert that
// its adapter produces the correct wire representation — including the file
// deletion, which the two providers encode very differently.
func StandardCommits() []provider.Commit {
	return []provider.Commit{{
		Message: "patchr: standard test commit",
		Files: []provider.CommitFile{
			{Path: "docs/added.md", Content: "hello from patchr\n"},
			{Path: "obsolete.txt", Delete: true},
		},
	}}
}

// AssertIssuesPopulated fails if any issue is missing a core field, catching an
// adapter that forgets to map one.
func AssertIssuesPopulated(t testing.TB, issues []provider.Issue) {
	t.Helper()
	if len(issues) == 0 {
		t.Fatal("expected at least one issue, got none")
	}
	for i, iss := range issues {
		switch {
		case iss.Number == 0:
			t.Errorf("issue[%d]: Number is zero", i)
		case iss.Title == "":
			t.Errorf("issue[%d] (#%d): Title is empty", i, iss.Number)
		case iss.URL == "":
			t.Errorf("issue[%d] (#%d): URL is empty", i, iss.Number)
		case iss.Author == "":
			t.Errorf("issue[%d] (#%d): Author is empty", i, iss.Number)
		case iss.CreatedAt.IsZero():
			t.Errorf("issue[%d] (#%d): CreatedAt is zero", i, iss.Number)
		case iss.UpdatedAt.IsZero():
			t.Errorf("issue[%d] (#%d): UpdatedAt is zero", i, iss.Number)
		}
	}
}

// AssertNoIssueNumbers fails if any returned issue carries an excluded number.
// Adapters use it to prove pull/merge requests are filtered out of issue lists.
func AssertNoIssueNumbers(t testing.TB, issues []provider.Issue, excluded ...int) {
	t.Helper()
	for _, iss := range issues {
		if slices.Contains(excluded, iss.Number) {
			t.Errorf("issue #%d should have been excluded (it is a pull/merge request)", iss.Number)
		}
	}
}

// AssertEveryIssueHasLabel fails if any returned issue is missing label.
func AssertEveryIssueHasLabel(t testing.TB, issues []provider.Issue, label string) {
	t.Helper()
	for _, iss := range issues {
		if !slices.Contains(iss.Labels, label) {
			t.Errorf("issue #%d missing expected label %q; has %v", iss.Number, label, iss.Labels)
		}
	}
}

// AssertErrorIs is errors.Is with a readable failure message.
func AssertErrorIs(t testing.TB, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want errors.Is(..., %v)", got, want)
	}
}
