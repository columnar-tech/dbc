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

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/cli/safeexec"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal"
)

func isUnderHomebrew(bin string) bool {
	brewExe, err := safeexec.LookPath("brew")
	if err != nil {
		return false
	}

	brewPrefixBytes, err := exec.Command(brewExe, "--prefix").Output()
	if err != nil {
		return false
	}

	brewBinPrefix := filepath.Join(strings.TrimSpace(
		string(brewPrefixBytes)), "bin") + string(filepath.Separator)
	return strings.HasPrefix(bin, brewBinPrefix)
}

func isPkgMgrInstall() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}

	if isUnderHomebrew(exe) {
		return true
	}

	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return false
	}

	switch filepath.Dir(exe) {
	case "/usr/local/bin":
		// pip installs here on linux
		return true
	case "/usr/bin":
		// likely a system package manager, but could be other things too
		return true
	default:
		// likely a local user install via script
		// or via msi on windows etc.
		// this is the case where we want to notify about updates
	}

	if runtime.GOOS == "windows" && strings.HasSuffix(filepath.Dir(exe), "\\Scripts") {
		// likely a pip install on windows
		return true
	}

	if strings.Contains(exe, "conda") || strings.Contains(exe, "venv") {
		// likely a conda or virtual environment install
		return true
	}

	return false
}

func notifyLatest() {
	if isPkgMgrInstall() {
		// skip notifying if installed via package manager,
		// since they likely have their own update mechanism
		return
	}

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
					dbc.Version, latestVer, "https://github.com/columnar-tech/dbc/releases/tag/v"+latestVer.String(), "https://docs.columnar.tech/dbc/getting_started/installation/")
				lipgloss.Println()
			}
		}
	}
}
