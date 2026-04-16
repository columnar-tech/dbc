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

	"github.com/pelletier/go-toml/v2"
)

type RegistryEntry struct {
	URL  string `toml:"url"`
	Name string `toml:"name"`
}

type GlobalConfig struct {
	Registries      []RegistryEntry `toml:"registries"`
	ReplaceDefaults bool            `toml:"replace_defaults"`
}

func loadGlobalConfig(configDir string) (*GlobalConfig, error) {
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
		return nil, err
	}

	for _, entry := range cfg.Registries {
		if entry.URL == "" {
			return nil, fmt.Errorf("registry entry in %s has empty url", configPath)
		}
		if _, err := url.Parse(entry.URL); err != nil {
			return nil, fmt.Errorf("invalid registry URL %q in %s: %w", entry.URL, configPath, err)
		}
	}

	return &cfg, nil
}

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

	seen := make(map[string]bool)
	var result []Registry

	urlKey := func(u *url.URL) string {
		path := strings.TrimRight(u.Path, "/")
		return u.Host + path
	}

	toRegistry := func(entry RegistryEntry) (Registry, bool) {
		u, err := url.Parse(entry.URL)
		if err != nil || u.Host == "" {
			return Registry{}, false
		}
		return Registry{Name: entry.Name, BaseURL: u}, true
	}

	addEntries := func(entries []RegistryEntry) {
		for _, e := range entries {
			r, ok := toRegistry(e)
			if !ok {
				continue
			}
			key := urlKey(r.BaseURL)
			if !seen[key] {
				seen[key] = true
				result = append(result, r)
			}
		}
	}

	addEntries(projectRegs)
	addEntries(globalRegs)

	if !replaceDefaults {
		for _, r := range defaults {
			if r.BaseURL == nil {
				continue
			}
			key := urlKey(r.BaseURL)
			if !seen[key] {
				seen[key] = true
				result = append(result, r)
			}
		}
	}

	return result
}
