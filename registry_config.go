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
