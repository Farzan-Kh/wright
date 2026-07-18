// SPDX-License-Identifier: Apache-2.0

package stack

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FileStore persists entries as one JSON file per stacked PR, at
// Dir/<sanitized-repo>/<pr-number>.json. Unlike internal/cache.FileStore,
// ListPending has no single issue number to key a lookup off - reconciling
// checks every pending stacked PR in a repo each poll cycle - so it scans the
// repo's directory rather than loading one known path.
type FileStore struct {
	Dir string
}

var _ Store = (*FileStore)(nil)

func (s *FileStore) Add(e Entry) error {
	if strings.TrimSpace(e.Repo) == "" || e.StackedPRNumber == 0 {
		return errors.New("stack: entry requires repo and stacked_pr_number")
	}
	p := s.path(e.Repo, e.StackedPRNumber)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("stack: create dir: %w", err)
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("stack: encode %s PR#%d: %w", e.Repo, e.StackedPRNumber, err)
	}
	// Write to a temp file and rename so a crash mid-write can never leave a
	// truncated/corrupt entry behind.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("stack: write %s PR#%d: %w", e.Repo, e.StackedPRNumber, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("stack: finalize %s PR#%d: %w", e.Repo, e.StackedPRNumber, err)
	}
	return nil
}

func (s *FileStore) ListPending(repo string) ([]Entry, error) {
	dirEntries, err := os.ReadDir(s.dir(repo))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stack: list %s: %w", repo, err)
	}

	var entries []Entry
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir(repo), de.Name()))
		if err != nil {
			return nil, fmt.Errorf("stack: read %s/%s: %w", repo, de.Name(), err)
		}
		var e Entry
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, fmt.Errorf("stack: decode %s/%s: %w", repo, de.Name(), err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (s *FileStore) Remove(repo string, stackedPRNumber int) error {
	err := os.Remove(s.path(repo, stackedPRNumber))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stack: remove %s PR#%d: %w", repo, stackedPRNumber, err)
	}
	return nil
}

func (s *FileStore) dir(repo string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_").Replace(repo)
	return filepath.Join(s.Dir, safe)
}

func (s *FileStore) path(repo string, stackedPRNumber int) string {
	return filepath.Join(s.dir(repo), strconv.Itoa(stackedPRNumber)+".json")
}
