// SPDX-License-Identifier: Apache-2.0

// Package pipeline wires poll -> gate -> execution -> failure reporting.
package pipeline

import (
	"context"
	"errors"
	"strings"

	"github.com/farzan-kh/wright/internal/cost"
	"github.com/farzan-kh/wright/internal/gate"
	"github.com/farzan-kh/wright/internal/logging"
	"github.com/farzan-kh/wright/internal/poller"
	"github.com/farzan-kh/wright/internal/provider"
)

// ReadyHandler executes the expensive path for a gate-approved issue.
type ReadyHandler func(ctx context.Context, issue provider.Issue) (cost.Summary, error)

// Pipeline executes one sequential pass for one repo.
type Pipeline struct {
	Provider        provider.Provider
	Repo            provider.Repo
	TriggerLabel    string
	NeedsHumanLabel string
	Poller          *poller.Poller
	Gate            *gate.Gate
	OnReady         ReadyHandler
}

// SkipError indicates that an issue was intentionally skipped.
type SkipError struct {
	Reason string
}

func (e *SkipError) Error() string {
	if strings.TrimSpace(e.Reason) == "" {
		return "pipeline: skipped"
	}
	return e.Reason
}

// NewSkipError builds a skip marker error.
func NewSkipError(reason string) error {
	return &SkipError{Reason: reason}
}

// IssueReport captures one issue outcome.
type IssueReport struct {
	IssueNumber int
	Status      string
	Detail      string
	Cost        cost.Summary
}

func (p *Pipeline) RunOnce(ctx context.Context) ([]IssueReport, error) {
	l := logging.FromContext(ctx)
	issues, err := p.Poller.Once(ctx)
	if err != nil {
		l.Error("pipeline: poll failed", "error", err.Error())
		return nil, err
	}
	l.Debug("pipeline: poll ok", "count", len(issues))

	reports := make([]IssueReport, 0, len(issues))
	for _, iss := range issues {
		il := l.With("issue", iss.Number)
		il.Debug("pipeline: issue picked up")
		rep := IssueReport{IssueNumber: iss.Number}
		total := cost.NewAccumulator()

		v, gateUsage, err := p.Gate.CheckWithUsage(ctx, iss)
		total.Add(gateUsage)
		if err != nil {
			rep.Status = "error"
			rep.Detail = "gate: " + err.Error()
			rep.Cost = total.Summary()
			il.Error("pipeline: issue outcome", "status", rep.Status, "detail", rep.Detail)
			reports = append(reports, rep)
			continue
		}
		if !v.Ready {
			rep.Status = "needs-info"
			rep.Detail = strings.TrimSpace(v.Missing)
			if rep.Detail == "" {
				rep.Detail = "issue is not implementation-ready"
			}
			_ = p.Provider.CommentOnIssue(ctx, p.Repo, iss.Number, "Wright couldn't start yet because this issue is missing details:\n\n"+rep.Detail)
			_ = p.Provider.RemoveIssueLabel(ctx, p.Repo, iss.Number, p.TriggerLabel)
			rep.Cost = total.Summary()
			il.Info("pipeline: issue outcome", "status", rep.Status, "detail", rep.Detail)
			reports = append(reports, rep)
			continue
		}

		if p.OnReady == nil {
			rep.Status = "ready"
			rep.Detail = "gate passed; execution pipeline not configured"
			rep.Cost = total.Summary()
			il.Info("pipeline: issue outcome", "status", rep.Status, "detail", rep.Detail)
			reports = append(reports, rep)
			continue
		}

		il.Debug("pipeline: gate passed, starting execution")
		execSummary, err := p.OnReady(ctx, iss)
		rep.Cost = mergeCost(total.Summary(), execSummary)
		if err != nil {
			var skip *SkipError
			if errors.As(err, &skip) {
				rep.Status = "skipped"
				rep.Detail = skip.Error()
				il.Info("pipeline: issue outcome", "status", rep.Status, "detail", rep.Detail)
				reports = append(reports, rep)
				continue
			}
			rep.Status = "needs-human"
			rep.Detail = err.Error()
			_ = p.Provider.RemoveIssueLabel(ctx, p.Repo, iss.Number, p.TriggerLabel)
			if strings.TrimSpace(p.NeedsHumanLabel) != "" {
				_ = p.Provider.AddIssueLabel(ctx, p.Repo, iss.Number, p.NeedsHumanLabel)
			}
			il.Error("pipeline: issue outcome", "status", rep.Status, "detail", rep.Detail)
		} else {
			rep.Status = "completed"
			rep.Detail = "issue processed"
			il.Info("pipeline: issue outcome", "status", rep.Status, "detail", rep.Detail)
		}
		reports = append(reports, rep)
	}
	return reports, nil
}

func mergeCost(a, b cost.Summary) cost.Summary {
	a.Turns += b.Turns
	a.Usage.InputTokens += b.Usage.InputTokens
	a.Usage.OutputTokens += b.Usage.OutputTokens
	a.Usage.CacheCreationInputTokens += b.Usage.CacheCreationInputTokens
	a.Usage.CacheReadInputTokens += b.Usage.CacheReadInputTokens
	return a
}
