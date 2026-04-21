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

//go:build !no_notify_latest

package main

import (
	"errors"
	"os"
	"path/filepath"

	"charm.land/lipgloss/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal"
)

func notifyLatest() {
	configDir, err := internal.GetDbcConfigPath()
	if err != nil {
		return
	}

	// skip notifying if $dbc_config_home/.no-update exists
	_, err = os.Stat(filepath.Join(configDir, ".no-update"))
	if errors.Is(err, os.ErrNotExist) {
		latestVer, err := dbc.GetLatestDbcVersion()
		if dbc.Version != "(devel)" && err == nil {
			if semver.MustParse(dbc.Version).LessThan(latestVer) {
				lipgloss.Printf(descStyle.Render("Update available: A new version of dbc is available. You're running %s and v%s is available. Please upgrade.\nChangelog: %s. Docs: %s"),
					dbc.Version, latestVer, "https://github.com/columnar-tech/dbc/releases/tag/"+latestVer.String(), "https://docs.columnar.tech/dbc/getting_started/installation/")
				lipgloss.Println()
			}
		}
	}
}
