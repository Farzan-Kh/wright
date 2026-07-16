// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads, decodes, defaults, and validates the config at path. Unknown
// fields are rejected so typos fail loudly rather than being silently ignored.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)

	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	c.applyDefaults()

	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("config: invalid %s: %w", path, err)
	}
	return &c, nil
}

// SelectRepo picks the repo entry a single-repo command operates on. When
// fullPath is empty it returns the sole entry, erroring if the config has more
// than one. When fullPath is set it returns the entry with a matching Repo,
// erroring if none match.
func (c *Config) SelectRepo(fullPath string) (*RepoConfig, error) {
	if fullPath == "" {
		if len(c.Repos) != 1 {
			return nil, fmt.Errorf("config: --repo is required: %d repos configured", len(c.Repos))
		}
		return &c.Repos[0], nil
	}
	for i := range c.Repos {
		if c.Repos[i].Repo == fullPath {
			return &c.Repos[i], nil
		}
	}
	return nil, fmt.Errorf("config: no repo %q in config", fullPath)
}
