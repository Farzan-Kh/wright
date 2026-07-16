// SPDX-License-Identifier: Apache-2.0

package sandbox

import (
	"context"
	"fmt"
	"sync"
)

// FakeExec is an in-memory ToolExec for tests.
type FakeExec struct {
	mu sync.Mutex

	Files  map[string]string
	BashFn func(command string) (string, error)
}

var _ ToolExec = (*FakeExec)(nil)

func (f *FakeExec) Bash(_ context.Context, command string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.BashFn != nil {
		return f.BashFn(command)
	}
	return "", nil
}

func (f *FakeExec) ReadFile(_ context.Context, path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Files == nil {
		f.Files = map[string]string{}
	}
	v, ok := f.Files[path]
	if !ok {
		return "", fmt.Errorf("read %s: not found", path)
	}
	return v, nil
}

func (f *FakeExec) WriteFile(_ context.Context, path, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Files == nil {
		f.Files = map[string]string{}
	}
	f.Files[path] = content
	return nil
}

func (f *FakeExec) ReplaceText(_ context.Context, path, oldText, newText string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Files == nil {
		f.Files = map[string]string{}
	}
	v, ok := f.Files[path]
	if !ok {
		return fmt.Errorf("replace %s: not found", path)
	}
	updated, err := replaceUnique(v, oldText, newText)
	if err != nil {
		return fmt.Errorf("%w (%s)", err, path)
	}
	f.Files[path] = updated
	return nil
}

func (f *FakeExec) Exists(_ context.Context, path string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Files == nil {
		return false, nil
	}
	_, ok := f.Files[path]
	return ok, nil
}
