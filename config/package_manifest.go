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
	"fmt"
	"github.com/Masterminds/semver/v3"
	"github.com/pelletier/go-toml/v2"
	"strings"
)

func decodePackageManifest(name string, data []byte) (packageManifest, error) {
	switch name {
	case packageV2MetadataName:
		return decodePackageV2Metadata(data)
	case legacyPackageManifestName:
		return decodeLegacyPackageManifest(data)
	default:
		return packageManifest{}, fmt.Errorf("unsupported package metadata filename %q", name)
	}
}

func decodePackageV2Metadata(data []byte) (packageManifest, error) {
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return packageManifest{}, fmt.Errorf("%w: error decoding package v2 metadata: %v", ErrInvalidManifest, err)
	}

	rawVersion, hasDiscriminator := root["package_version"]
	if !hasDiscriminator {
		return packageManifest{}, fmt.Errorf("%w: package_version = 2 is required in %s", ErrInvalidManifest, packageV2MetadataName)
	}
	version, ok := rawVersion.(int64)
	if !ok {
		return packageManifest{}, fmt.Errorf("%w: package_version must be an integer", ErrInvalidManifest)
	}
	if version != 2 {
		return packageManifest{}, fmt.Errorf("%w: package version %d is unsupported", ErrInvalidManifest, version)
	}

	var wire packageManifestV2Wire
	if err := toml.Unmarshal(data, &wire); err != nil {
		return packageManifest{}, fmt.Errorf("%w: error decoding package v2 metadata: %v", ErrInvalidManifest, err)
	}
	if wire.PackageVersion != 2 {
		return packageManifest{}, fmt.Errorf("%w: package_version must be 2", ErrInvalidManifest)
	}
	if wire.ManifestVersion != nil {
		return packageManifest{}, fmt.Errorf("%w: package v2 must not set runtime manifest_version", ErrInvalidManifest)
	}
	if err := validateFlatName(wire.ID); err != nil {
		return packageManifest{}, fmt.Errorf("%w: invalid package id: %v", ErrInvalidManifest, err)
	}
	if strings.TrimSpace(wire.Name) == "" {
		return packageManifest{}, fmt.Errorf("%w: name is required", ErrInvalidManifest)
	}
	if wire.Version == "" {
		return packageManifest{}, fmt.Errorf("%w: version is required", ErrInvalidManifest)
	}
	parsedVersion, err := semver.StrictNewVersion(wire.Version)
	if err != nil {
		return packageManifest{}, fmt.Errorf("%w: version %q must be valid SemVer 2.0.0: %v", ErrInvalidManifest, wire.Version, err)
	}
	if err := validatePlatformIdentifier(wire.Platform); err != nil {
		return packageManifest{}, fmt.Errorf("%w: invalid platform: %v", ErrInvalidManifest, err)
	}
	if strings.TrimSpace(wire.Driver.Entrypoint) == "" {
		return packageManifest{}, fmt.Errorf("%w: Driver.entrypoint is required", ErrInvalidManifest)
	}
	if err := validateFlatName(wire.Files.Driver); err != nil {
		return packageManifest{}, fmt.Errorf("%w: invalid Files.driver: %v", ErrInvalidManifest, err)
	}

	return packageManifest{
		manifest: Manifest{
			PackageVersion:  2,
			PackagePlatform: wire.Platform,
			DriverInfo: DriverInfo{
				ID:      wire.ID,
				Name:    wire.Name,
				Version: parsedVersion,
				Driver: struct {
					Entrypoint string
					Shared     driverMap
				}{Entrypoint: wire.Driver.Entrypoint},
			},
			Files: struct {
				Driver    string `toml:"driver,omitempty"`
				Signature string `toml:"signature,omitempty"`
			}{Driver: wire.Files.Driver},
		},
		id: wire.ID, platform: wire.Platform, v2: true,
	}, nil
}

func decodeLegacyPackageManifest(data []byte) (packageManifest, error) {
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return packageManifest{}, fmt.Errorf("%w: error decoding legacy package manifest: %v", ErrInvalidManifest, err)
	}
	if _, hasPackageVersion := root["package_version"]; hasPackageVersion {
		return packageManifest{}, fmt.Errorf("%w: package_version is not allowed in legacy %s; use %s for package v2 metadata", ErrInvalidManifest, legacyPackageManifestName, packageV2MetadataName)
	}

	var wire legacyPackageManifestWire
	if err := toml.Unmarshal(data, &wire); err != nil {
		return packageManifest{}, fmt.Errorf("%w: error decoding legacy package manifest: %v", ErrInvalidManifest, err)
	}
	if wire.ManifestVersion > currentManifestVersion {
		return packageManifest{}, fmt.Errorf("manifest version %d is unsupported, only %d and lower are supported by this version of dbc", wire.ManifestVersion, currentManifestVersion)
	}
	if strings.TrimSpace(wire.Name) == "" {
		return packageManifest{}, fmt.Errorf("%w: name is required", ErrInvalidManifest)
	}
	if wire.Version == nil {
		return packageManifest{}, fmt.Errorf("%w: version is required", ErrInvalidManifest)
	}
	if wire.Files.Driver != "" {
		if err := validateFlatName(wire.Files.Driver); err != nil {
			return packageManifest{}, fmt.Errorf("%w: invalid Files.driver: %v", ErrInvalidManifest, err)
		}
	}
	shared, err := decodeDriverShared(wire.Driver.Shared, false)
	if err != nil {
		return packageManifest{}, err
	}
	return packageManifest{
		manifest: Manifest{
			DriverInfo: DriverInfo{
				Name:      wire.Name,
				Publisher: wire.Publisher,
				License:   wire.License,
				Version:   wire.Version,
				Source:    wire.Source,
				AdbcInfo:  wire.AdbcInfo,
				Driver: struct {
					Entrypoint string
					Shared     driverMap
				}{Entrypoint: wire.Driver.Entrypoint, Shared: shared},
			},
			Files:       wire.Files,
			PostInstall: wire.PostInstall,
		},
	}, nil
}

func classifyPackageMetadataName(name string) (string, bool, error) {
	for _, expected := range []string{legacyPackageManifestName, packageV2MetadataName} {
		if !strings.EqualFold(name, expected) {
			continue
		}
		if name != expected {
			return "", false, fmt.Errorf("package metadata file must be named exactly %q", expected)
		}
		return expected, true, nil
	}
	return "", false, nil
}

func selectPackageMetadata(metadata map[string][]byte) (string, []byte, error) {
	legacy, hasLegacy := metadata[legacyPackageManifestName]
	v2, hasV2 := metadata[packageV2MetadataName]
	if hasLegacy && hasV2 {
		return "", nil, fmt.Errorf("package archive must contain either %s or %s, not both", legacyPackageManifestName, packageV2MetadataName)
	}
	if hasLegacy {
		return legacyPackageManifestName, legacy, nil
	}
	if hasV2 {
		return packageV2MetadataName, v2, nil
	}
	return "", nil, fmt.Errorf("package archive must contain exactly one of %s or %s", legacyPackageManifestName, packageV2MetadataName)
}

func decodeDriverShared(value any, required bool) (driverMap, error) {
	var result driverMap
	switch shared := value.(type) {
	case string:
		result.defaultPath = shared
	case map[string]any:
		result.platformMap = make(map[string]string, len(shared))
		for platform, rawPath := range shared {
			path, ok := rawPath.(string)
			if !ok {
				return driverMap{}, fmt.Errorf("%w: invalid type for platform %s, expected string", ErrInvalidManifest, platform)
			}
			result.platformMap[platform] = path
		}
	default:
		if required {
			return driverMap{}, fmt.Errorf("%w: invalid type for 'Driver.shared' in manifest, expected string or table", ErrInvalidManifest)
		}
	}
	return result, nil
}
