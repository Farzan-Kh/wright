// SPDX-License-Identifier: Apache-2.0

package retrying

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/farzan-kh/wright/internal/agent/llm"
	"github.com/farzan-kh/wright/internal/retry"
)

func testConfig() retry.Config {
	return retry.Config{Strategy: retry.Fixed, MaxAttempts: 4, BaseDelay: time.Millisecond}
}

func TestRetriesUntilSuccess(t *testing.T) {
	calls := 0
	wrapped := providerFunc(func(ctx context.Context, req llm.MessageRequest) (llm.MessageResponse, error) {
		calls++
		if calls < 3 {
			return llm.MessageResponse{}, errors.New("connection reset")
		}
		return llm.MessageResponse{StopReason: "end_turn"}, nil
	})

	p := New(wrapped, testConfig())
	resp, err := p.CreateMessage(context.Background(), llm.MessageRequest{})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn", resp.StopReason)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestExhaustsMaxAttempts(t *testing.T) {
	calls := 0
	wantErr := errors.New("persistent failure")
	wrapped := providerFunc(func(ctx context.Context, req llm.MessageRequest) (llm.MessageResponse, error) {
		calls++
		return llm.MessageResponse{}, wantErr
	})

	p := New(wrapped, testConfig())
	_, err := p.CreateMessage(context.Background(), llm.MessageRequest{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if calls != 4 {
		t.Errorf("calls = %d, want 4 (MaxAttempts)", calls)
	}
}

// providerFunc adapts a function to llm.LLMProvider for tests.
type providerFunc func(ctx context.Context, req llm.MessageRequest) (llm.MessageResponse, error)

func (f providerFunc) CreateMessage(ctx context.Context, req llm.MessageRequest) (llm.MessageResponse, error) {
	return f(ctx, req)
}
