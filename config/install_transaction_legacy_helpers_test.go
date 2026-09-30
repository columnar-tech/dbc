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

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
)

func countInstallGenerations(t *testing.T, location, runtimeID string) int {
	t.Helper()
	entries, err := os.ReadDir(location)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".dbc-package-"+runtimeID+"-") {
			count++
		}
	}
	return count
}

func createLegacyInstall(t *testing.T, location, runtimeID, version string, sharedPath string) string {
	t.Helper()
	directory := filepath.Join(location, runtimeID+"_"+PlatformTuple()+"_v"+version)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if sharedPath == "" {
		sharedPath = filepath.Join(directory, "old-library.so")
	}
	if err := os.WriteFile(sharedPath, []byte("old library"), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := DriverInfo{
		ID: runtimeID, Name: "Example Driver", Version: semver.MustParse(version), Source: "dbc",
	}
	driver.Driver.Shared.defaultPath = sharedPath
	if err := CreateManifest(Config{Level: ConfigEnv, Location: location}, driver); err != nil {
		t.Fatal(err)
	}
	return directory
}

func installVersionTwo(t *testing.T, cfg Config, runtimeID string, verify func(string, Manifest) error, register func(Config, DriverInfo) error) error {
	t.Helper()
	archive := makeInstallArchive(t, runtimeID, "2.0.0", "new-library.so", []byte("new library"))
	file := writeInstallArchive(t, archive, "version-two")
	defer file.Close()
	expected := installExpected(runtimeID, "new-source", archive)
	expected.Version = "2.0.0"
	if register == nil {
		_, err := InstallPackage(cfg, runtimeID, file, expected, InstallOptions{Verify: verify})
		return err
	}
	_, err := installPackage(cfg, runtimeID, file, expected, InstallOptions{Verify: verify}, register)
	return err
}
