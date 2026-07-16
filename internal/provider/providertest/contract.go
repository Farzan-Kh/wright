// SPDX-License-Identifier: Apache-2.0

// Package providertest holds shared, provider-agnostic assertions that both the
// GitHub and GitLab adapter test suites run against their own httptest fakes.
// It deliberately does not stand up a fake itself — each adapter speaks a
// different wire format — but it pins down the semantics the domain interface
// promises, so the two adapters stay honest against the same contract.
package providertest

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/farzan-kh/wright/internal/provider"
)

// StandardCommits returns a fixed commit fixture used by both adapter suites.
// Feeding the identical input to each PushCommits lets every suite assert that
// its adapter produces the correct wire representation — including the file
// deletion, which the two providers encode very differently.
func StandardCommits() []provider.Commit {
	return []provider.Commit{{
		Message: "wright: standard test commit",
		Files: []provider.CommitFile{
			{Path: "docs/added.md", Content: "hello from wright\n"},
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

// AssertIssueLabelRoundTrip verifies add/remove label behavior through the
// provider contract by mutating one issue and observing labels through
// ListLabeledIssues.
func AssertIssueLabelRoundTrip(t testing.TB, p provider.Provider, repo provider.Repo, issueNumber int, triggerLabel, label string) {
	t.Helper()
	ctx := context.Background()

	if err := p.AddIssueLabel(ctx, repo, issueNumber, label); err != nil {
		t.Fatalf("AddIssueLabel: %v", err)
	}
	issues, err := p.ListLabeledIssues(ctx, repo, triggerLabel)
	if err != nil {
		t.Fatalf("ListLabeledIssues (after add): %v", err)
	}
	withLabel, ok := findIssue(issues, issueNumber)
	if !ok {
		t.Fatalf("issue #%d not found after adding label; issues=%+v", issueNumber, issues)
	}
	if !slices.Contains(withLabel.Labels, label) {
		t.Fatalf("issue #%d missing label %q after add; labels=%v", issueNumber, label, withLabel.Labels)
	}

	if err := p.RemoveIssueLabel(ctx, repo, issueNumber, label); err != nil {
		t.Fatalf("RemoveIssueLabel: %v", err)
	}
	issues, err = p.ListLabeledIssues(ctx, repo, triggerLabel)
	if err != nil {
		t.Fatalf("ListLabeledIssues (after remove): %v", err)
	}
	withoutLabel, ok := findIssue(issues, issueNumber)
	if !ok {
		t.Fatalf("issue #%d not found after removing label; issues=%+v", issueNumber, issues)
	}
	if slices.Contains(withoutLabel.Labels, label) {
		t.Fatalf("issue #%d still has label %q after remove; labels=%v", issueNumber, label, withoutLabel.Labels)
	}
}

func findIssue(issues []provider.Issue, number int) (provider.Issue, bool) {
	for _, iss := range issues {
		if iss.Number == number {
			return iss, true
		}
	}
	return provider.Issue{}, false
}

// AssertErrorIs is errors.Is with a readable failure message.
func AssertErrorIs(t testing.TB, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want errors.Is(..., %v)", got, want)
	}
}
