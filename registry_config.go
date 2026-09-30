// Copyright 2026 Columnar Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dbc

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/columnar-tech/dbc/internal/sourceidentity"
	"github.com/pelletier/go-toml/v2"
)

// RegistryEntry is a single registry declared in a global config.toml or a
// project's dbc.toml.
type RegistryEntry struct {
	URL  string `toml:"url"`
	Name string `toml:"name,omitempty"`
}

// GlobalConfig is the schema of a user's global dbc config.toml.
type GlobalConfig struct {
	Registries      []RegistryEntry `toml:"registries"`
	ReplaceDefaults bool            `toml:"replace_defaults,omitempty"`
}

// LoadGlobalConfig reads config.toml from configDir. It returns (nil, nil) if
// the file does not exist; every entry is validated.
func LoadGlobalConfig(configDir string) (*GlobalConfig, error) {
	configPath := filepath.Join(configDir, "config.toml")
	f, err := os.Open(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var cfg GlobalConfig
	if err := toml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", configPath, err)
	}

	// Note: a global replace_defaults=true with no [[registries]] is NOT rejected
	// here, because a project's dbc.toml may supply the entries at NewClient
	// time. The "zero resulting registries" case is enforced after merging in
	// NewClient so both library and CLI callers share the same semantics.
	for _, entry := range cfg.Registries {
		if err := validateRegistryEntry(entry); err != nil {
			return nil, fmt.Errorf("%s: %w", configPath, err)
		}
	}

	return &cfg, nil
}

func validateRegistryEntry(e RegistryEntry) error {
	if e.URL == "" {
		return errors.New("registry entry has empty url")
	}
	_, err := sourceidentity.Parse(sourceidentity.Registry, e.URL)
	return err
}

// mergeRegistries combines project, global, and default registries into a
// deduplicated list in priority order: project first, then global, then
// built-in defaults (unless either global or project overrides with
// replace_defaults).
func mergeRegistries(
	projectRegs []RegistryEntry,
	projectReplaceDefaults *bool,
	globalRegs []RegistryEntry,
	globalReplaceDefaults bool,
	defaults []Registry,
) []Registry {
	replaceDefaults := globalReplaceDefaults
	if projectReplaceDefaults != nil {
		replaceDefaults = *projectReplaceDefaults
	}

	seen := make(map[sourceidentity.Key]bool)
	var result []Registry

	addEntries := func(entries []RegistryEntry) {
		for _, e := range entries {
			key, err := sourceidentity.Parse(sourceidentity.Registry, e.URL)
			if err != nil {
				continue
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			baseURL, err := url.Parse(e.URL)
			if err != nil {
				continue
			}
			result = append(result, Registry{Name: e.Name, BaseURL: baseURL})
		}
	}

	addEntries(projectRegs)
	addEntries(globalRegs)

	if !replaceDefaults {
		for _, r := range defaults {
			if r.BaseURL == nil {
				continue
			}
			key, err := sourceidentity.Parse(sourceidentity.Registry, r.BaseURL.String())
			if err != nil {
				continue
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, r)
		}
	}

	return result
}

// WithGlobalConfig applies the registries declared in a loaded global config
// on top of the client's built-in defaults. Pass nil (or an empty config) to
// leave the default registry set untouched.
//
// WithBaseURL takes precedence over this option — when set, registries from
// the global config are ignored.
func WithGlobalConfig(cfg *GlobalConfig) Option {
	return func(c *clientConfig) {
		c.globalConfig = cfg
	}
}

// WithProjectRegistries applies registries declared in a project's dbc.toml on
// top of the global and default registries. replaceDefaults takes priority
// over the same flag in the global config; pass nil to inherit the global
// config's value.
//
// NewClient returns an error if any entry is malformed or if the merged
// registry list ends up empty (e.g. replaceDefaults=true with no project or
// global entries). replaceDefaults=true is valid on its own when a non-empty
// global config supplies the entries — the post-merge check is what matters.
//
// WithBaseURL takes precedence over this option — when set, project
// registries are ignored.
func WithProjectRegistries(entries []RegistryEntry, replaceDefaults *bool) Option {
	entriesCopy := append([]RegistryEntry(nil), entries...)
	return func(c *clientConfig) {
		c.projectRegistries = entriesCopy
		c.projectReplaceDefaults = replaceDefaults
	}
}
