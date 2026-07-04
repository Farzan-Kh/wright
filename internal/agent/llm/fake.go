package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrFakeExhausted is returned when the fake has no queued responses left.
var ErrFakeExhausted = errors.New("llm fake exhausted")

// FakeProvider is a deterministic LLMProvider for tests.
type FakeProvider struct {
	mu        sync.Mutex
	Responses []MessageResponse
	Err       error
	Requests  []MessageRequest
}

var _ LLMProvider = (*FakeProvider)(nil)

func (f *FakeProvider) CreateMessage(_ context.Context, req MessageRequest) (MessageResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Requests = append(f.Requests, req)
	if f.Err != nil {
		return MessageResponse{}, f.Err
	}
	if len(f.Responses) == 0 {
		return MessageResponse{}, fmt.Errorf("%w: no response queued", ErrFakeExhausted)
	}
	resp := f.Responses[0]
	f.Responses = f.Responses[1:]
	return resp, nil
}
