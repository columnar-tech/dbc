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
	"strings"

	"github.com/columnar-tech/dbc/internal/registryurl"
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
	Registries []RegistryEntry `toml:"registries"`
	// ExcludeBuiltinRegistries removes dbc's built-in registries from the
	// registry list. Registries declared in [[registries]] are kept.
	ExcludeBuiltinRegistries bool `toml:"exclude_builtin_registries,omitempty"`
	// Network holds settings that change where requests are sent. They are
	// only read from the user's config, never from a project's dbc.toml.
	Network NetworkConfig `toml:"network,omitempty"`
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

	// Note: a global exclude_builtin_registries=true with no [[registries]] is NOT rejected
	// here, because a project's dbc.toml may supply the entries at NewClient
	// time. The "zero resulting registries" case is enforced after merging in
	// NewClient so both library and CLI callers share the same semantics.
	if err := validateRegistryEntries(cfg.Registries); err != nil {
		return nil, fmt.Errorf("%s: %w", configPath, err)
	}

	if _, err := newURLRouter(cfg.Network.URLReplacements); err != nil {
		return nil, fmt.Errorf("%s: %w", configPath, err)
	}

	return &cfg, nil
}

// ErrProjectNetwork is returned when a project's dbc.toml contains a network
// table. Network settings such as URL replacements are only read from the
// user's config, so a project cannot redirect requests.
var ErrProjectNetwork = errors.New("network settings are only read from the user config, not from dbc.toml; remove the [network] table")

// LoadProjectRegistries reads only registries and exclude_builtin_registries
// from a project's dbc.toml, leaving driver specifications to the caller.
// Missing files and malformed entries are errors.
//
// Pass the result to WithProjectRegistries. Whether the resulting registry
// list is empty is checked by NewClient after merging global/project config.
func LoadProjectRegistries(path string) ([]RegistryEntry, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	var cfg struct {
		Registries               []RegistryEntry `toml:"registries"`
		ExcludeBuiltinRegistries bool            `toml:"exclude_builtin_registries"`
		Network                  map[string]any  `toml:"network"`
	}
	if err := toml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, false, fmt.Errorf("invalid %s: %w", path, err)
	}
	if cfg.Network != nil {
		return nil, false, fmt.Errorf("%s: %w", path, ErrProjectNetwork)
	}
	if err := validateRegistryEntries(cfg.Registries); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	return cfg.Registries, cfg.ExcludeBuiltinRegistries, nil
}

// validateRegistryEntries validates the registries declared in one file. A
// name identifies a registry within its file, so it must not repeat there;
// the same name in the user config and in a project is allowed.
func validateRegistryEntries(entries []RegistryEntry) error {
	seen := map[string]bool{}
	for _, e := range entries {
		if err := validateRegistryEntry(e); err != nil {
			return err
		}
		if e.Name == "" {
			continue
		}
		if seen[e.Name] {
			return fmt.Errorf("registry name %q is declared more than once", e.Name)
		}
		seen[e.Name] = true
	}
	return nil
}

func validateRegistryEntry(e RegistryEntry) error {
	if e.URL == "" {
		return errors.New("registry entry has empty url")
	}
	if _, err := registryurl.Parse(e.URL); err != nil {
		return fmt.Errorf("invalid registry URL %w", err)
	}
	return nil
}

// mergeRegistries combines project, global, and default registries into a
// deduplicated list in priority order: project first, then global, then
// built-in defaults (unless excludeBuiltin is set).
func mergeRegistries(
	projectRegs []RegistryEntry,
	globalRegs []RegistryEntry,
	excludeBuiltin bool,
	defaults []Registry,
) []Registry {
	seen := make(map[string]bool)
	var result []Registry

	// urlKey returns a canonical form that collapses only truly no-op
	// differences: scheme/host casing, trailing-slash on the path, and
	// fragments. Query, userinfo, and path segments are preserved because
	// they change the effective registry endpoint (tenant selectors,
	// credential-bearing URLs, path-mounted registries) and must be
	// treated as distinct registries here.
	urlKey := func(u *url.URL) string {
		cp := *u
		cp.Scheme = strings.ToLower(cp.Scheme)
		cp.Host = strings.ToLower(cp.Host)
		cp.Path = strings.TrimRight(cp.Path, "/")
		cp.Fragment = ""
		cp.RawFragment = ""
		return cp.String()
	}

	addEntries := func(entries []RegistryEntry) {
		for _, e := range entries {
			u, err := url.Parse(e.URL)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				continue
			}
			key := urlKey(u)
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, Registry{Name: e.Name, BaseURL: u})
		}
	}

	addEntries(projectRegs)
	addEntries(globalRegs)

	if !excludeBuiltin {
		for _, r := range defaults {
			if r.BaseURL == nil {
				continue
			}
			key := urlKey(r.BaseURL)
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
// top of the global and default registries. The built-in registries are
// excluded when excludeBuiltin is true or when the global config excludes
// them; a project cannot bring back built-in registries that the global
// config excludes.
//
// NewClient returns an error if any entry is malformed or if the merged
// registry list ends up empty (e.g. excludeBuiltin=true with no project or
// global entries). excludeBuiltin=true is valid on its own when a non-empty
// global config supplies the entries — the post-merge check is what matters.
//
// WithBaseURL takes precedence over this option — when set, project
// registries are ignored.
func WithProjectRegistries(entries []RegistryEntry, excludeBuiltin bool) Option {
	entriesCopy := append([]RegistryEntry(nil), entries...)
	return func(c *clientConfig) {
		c.projectRegistries = entriesCopy
		c.projectExcludeBuiltin = excludeBuiltin
	}
}
