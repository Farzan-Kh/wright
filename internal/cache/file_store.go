// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FileStore persists cache entries as one JSON file per issue, at
// Dir/<sanitized-repo>/<issue-number>.json. Flat files rather than a database
// on purpose: entries are meant to be human-inspectable and hand-deletable on
// a self-hosted, single-daemon install.
type FileStore struct {
	Dir string
}

var _ Store = (*FileStore)(nil)

func (s *FileStore) Load(repo string, issueNumber int) (*Entry, error) {
	data, err := os.ReadFile(s.path(repo, issueNumber))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cache: read %s#%d: %w", repo, issueNumber, err)
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("cache: decode %s#%d: %w", repo, issueNumber, err)
	}
	return &e, nil
}

func (s *FileStore) Save(e Entry) error {
	if strings.TrimSpace(e.Repo) == "" || e.IssueNumber == 0 {
		return errors.New("cache: entry requires repo and issue_number")
	}
	p := s.path(e.Repo, e.IssueNumber)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("cache: create dir: %w", err)
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("cache: encode %s#%d: %w", e.Repo, e.IssueNumber, err)
	}
	// Write to a temp file and rename so a crash mid-write can never leave a
	// truncated/corrupt cache entry behind - the one failure mode that would
	// be worse than not caching at all.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("cache: write %s#%d: %w", e.Repo, e.IssueNumber, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("cache: finalize %s#%d: %w", e.Repo, e.IssueNumber, err)
	}
	return nil
}

func (s *FileStore) Clear(repo string, issueNumber int) error {
	err := os.Remove(s.path(repo, issueNumber))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cache: remove %s#%d: %w", repo, issueNumber, err)
	}
	return nil
}

func (s *FileStore) path(repo string, issueNumber int) string {
	safe := strings.NewReplacer("/", "_", "\\", "_").Replace(repo)
	return filepath.Join(s.Dir, safe, strconv.Itoa(issueNumber)+".json")
}
