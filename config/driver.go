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
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/internal/atomicfile"
	"github.com/pelletier/go-toml/v2"
)

const currentManifestVersion = 1

type Manifest struct {
	DriverInfo

	// PackageVersion is the parsed package wire version. It is in-memory
	// metadata only; runtime ADBC manifests continue to use ManifestVersion.
	PackageVersion int

	Files struct {
		Driver    string `toml:"driver,omitempty"`
		Signature string `toml:"signature,omitempty"`
	} `toml:"Files,omitempty"`

	PostInstall struct {
		Messages []string `toml:"messages,inline,omitempty"`
	} `toml:"PostInstall,omitempty"`
}

type DriverInfo struct {
	ID       string
	FilePath string

	Name      string
	Publisher string
	License   string
	Version   *semver.Version
	Source    string

	AdbcInfo struct {
		Version  *semver.Version `toml:"version"`
		Features struct {
			Supported   []string `toml:"supported,omitempty"`
			Unsupported []string `toml:"unsupported,omitempty"`
		} `toml:"features,omitempty"`
	} `toml:"ADBC"`

	Driver struct {
		Entrypoint string
		Shared     driverMap
	}
}

type driverMap struct {
	platformMap map[string]string
	defaultPath string
}

func (d *driverMap) Set(platformTuple, path string) {
	if d.platformMap == nil {
		d.platformMap = make(map[string]string)
	}
	d.platformMap[platformTuple] = path
}

func (d driverMap) Get(platformTuple string) string {
	if d.defaultPath != "" {
		return d.defaultPath
	}
	return d.platformMap[platformTuple]
}

func (d driverMap) Paths() iter.Seq[string] {
	if d.defaultPath != "" {
		return func(yield func(string) bool) {
			yield(d.defaultPath)
		}
	}

	return func(yield func(string) bool) {
		for _, path := range d.platformMap {
			if !yield(path) {
				return
			}
		}
	}
}

func (d driverMap) String() string {
	if d.defaultPath != "" {
		return "\t" + d.defaultPath
	}
	if len(d.platformMap) == 0 {
		return ""
	}
	var sb strings.Builder
	for platform, path := range d.platformMap {
		sb.WriteString(fmt.Sprintf("\t- %s: %s\n", platform, path))
	}
	return sb.String()
}

// runtimeManifestWire is the installed ADBC Driver Manifest format. A legacy
// package's MANIFEST uses legacyPackageManifestWire, while dbc-package.toml
// uses packageManifestV2Wire.
type runtimeManifestWire struct {
	PackageVersion  *int64          `toml:"package_version,omitempty"`
	ManifestVersion int32           `toml:"manifest_version"`
	Name            string          `toml:"name"`
	Publisher       string          `toml:"publisher"`
	License         string          `toml:"license"`
	Version         *semver.Version `toml:"version"`
	Source          string          `toml:"source"`

	AdbcInfo struct {
		Version  *semver.Version `toml:"version"`
		Features struct {
			Supported   []string `toml:"supported,omitempty"`
			Unsupported []string `toml:"unsupported,omitempty"`
		} `toml:"features,omitempty"`
	} `toml:"ADBC"`

	Driver struct {
		Entrypoint string `toml:"entrypoint,omitempty"`
		Shared     any    `toml:"shared"`
	}

	Files struct {
		Driver    string `toml:"driver,omitempty"`
		Signature string `toml:"signature,omitempty"`
	} `toml:"Files,omitempty"`

	PostInstall struct {
		Messages []string `toml:"messages,inline,omitempty"`
	} `toml:"PostInstall,omitempty"`
}

func loadDriverFromManifest(prefix, driverName string) (DriverInfo, error) {
	driverName = strings.TrimSuffix(driverName, ".toml")
	manifest := filepath.Join(prefix, driverName+".toml")
	f, err := os.Open(manifest)
	if err != nil {
		return DriverInfo{}, fmt.Errorf("error opening manifest %s: %w", manifest, err)
	}
	defer f.Close()

	m, err := decodeManifest(f, driverName, true)
	if err != nil {
		return DriverInfo{}, fmt.Errorf("error decoding manifest %s: %w", manifest, err)
	}

	m.DriverInfo.FilePath = prefix
	return m.DriverInfo, nil
}

// Create a symlink to manifestPath in the parent dir
func createManifestSymlink(location, driverID, manifestPath string) {
	parentDir := filepath.Dir(filepath.Clean(location))
	safeDriverID := filepath.Base(driverID)
	symlink := filepath.Join(parentDir, safeDriverID+".toml")

	if filepath.Dir(symlink) == parentDir {
		parentAbs, parentErr := filepath.Abs(parentDir)
		manifestAbs, manifestErr := filepath.Abs(manifestPath)
		if parentErr != nil || manifestErr != nil {
			return
		}
		target, err := filepath.Rel(parentAbs, manifestAbs)
		if err != nil {
			target = manifestAbs
		}
		_ = os.Symlink(target, symlink)
	}
}

// Remove the symlink to manifestPath in the parent dir
func removeManifestSymlink(filePath, driverID string) {
	parentDir := filepath.Dir(filepath.Clean(filePath))
	safeDriverID := filepath.Base(driverID)
	symlink := filepath.Join(parentDir, safeDriverID+".toml")

	if filepath.Dir(symlink) != parentDir {
		return
	}
	linkInfo, err := os.Lstat(symlink)
	if err != nil || linkInfo.Mode()&os.ModeSymlink == 0 {
		return
	}
	target, err := os.Readlink(symlink)
	if err != nil {
		return
	}
	expected, expectedErr := filepath.Abs(filepath.Join(filePath, safeDriverID+".toml"))
	if expectedErr != nil {
		return
	}
	targetsExpectedManifest := func(candidate string) bool {
		actual, err := filepath.Abs(candidate)
		return err == nil && filepath.Clean(actual) == filepath.Clean(expected)
	}
	isTargetRegistration := false
	if filepath.IsAbs(target) {
		isTargetRegistration = targetsExpectedManifest(target)
	} else {
		// New links are relative to their parent directory. Older links may
		// contain the original relative manifestPath. Only the exact original
		// value is accepted for that legacy form; do not reinterpret arbitrary
		// relative targets from the process working directory.
		isTargetRegistration = targetsExpectedManifest(filepath.Join(parentDir, target))
		legacyManifestPath := filepath.Join(filePath, safeDriverID+".toml")
		isTargetRegistration = isTargetRegistration || target == legacyManifestPath
	}
	if isTargetRegistration {
		_ = os.Remove(symlink)
	}
}

func createDriverManifest(location string, driver DriverInfo) error {
	if _, err := os.Stat(location); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(location, 0755); err != nil {
			return fmt.Errorf("error creating driver location %s: %w", location, err)
		}
	}

	manifestPath := filepath.Join(location, driver.ID+".toml")
	toEncode := runtimeManifestWire{
		ManifestVersion: currentManifestVersion,
		Name:            driver.Name,
		Publisher:       driver.Publisher,
		License:         driver.License,
		Version:         driver.Version,
		Source:          driver.Source,
		AdbcInfo:        driver.AdbcInfo,
	}

	toEncode.Driver.Entrypoint = driver.Driver.Entrypoint
	if driver.Driver.Shared.defaultPath != "" {
		toEncode.Driver.Shared = driver.Driver.Shared.defaultPath
	} else if len(driver.Driver.Shared.platformMap) > 0 {
		toEncode.Driver.Shared = driver.Driver.Shared.platformMap
	}

	var encoded bytes.Buffer
	enc := toml.NewEncoder(&encoded).SetIndentTables(false)
	if err := enc.Encode(toEncode); err != nil {
		return fmt.Errorf("error encoding manifest %s: %w", driver.ID, err)
	}
	if err := atomicfile.WriteFile(manifestPath, encoded.Bytes(), 0o644); err != nil {
		return fmt.Errorf("error writing manifest %s: %w", driver.ID, err)
	}

	// Work around older driver managers that look one directory above the
	// configured manifest directory.
	createManifestSymlink(location, driver.ID, manifestPath)

	return nil
}

type manifestRollbackError struct {
	writeErr    error
	rollbackErr error
}

func (e *manifestRollbackError) Error() string {
	return fmt.Sprintf("%v; restoring previous manifest registration failed: %v", e.writeErr, e.rollbackErr)
}

func (e *manifestRollbackError) Unwrap() error { return e.writeErr }
