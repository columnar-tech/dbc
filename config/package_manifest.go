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
	"github.com/pelletier/go-toml/v2"
	"strings"
)

func decodePackageManifest(name string, data []byte) (Manifest, error) {
	switch name {
	case legacyPackageManifestName:
		return decodeLegacyPackageManifest(data)
	case reservedPackageMetadataName:
		return Manifest{}, fmt.Errorf("package metadata format %q is reserved for future use and is not supported yet", name)
	default:
		return Manifest{}, fmt.Errorf("unsupported package metadata filename %q", name)
	}
}

func decodeLegacyPackageManifest(data []byte) (Manifest, error) {
	var wire legacyPackageManifestWire
	if err := toml.Unmarshal(data, &wire); err != nil {
		return Manifest{}, fmt.Errorf("%w: error decoding legacy package manifest: %v", ErrInvalidManifest, err)
	}
	if wire.ManifestVersion > currentManifestVersion {
		return Manifest{}, fmt.Errorf("manifest version %d is unsupported, only %d and lower are supported by this version of dbc", wire.ManifestVersion, currentManifestVersion)
	}
	if strings.TrimSpace(wire.Name) == "" {
		return Manifest{}, fmt.Errorf("%w: name is required", ErrInvalidManifest)
	}
	if wire.Version == nil {
		return Manifest{}, fmt.Errorf("%w: version is required", ErrInvalidManifest)
	}
	if wire.Files.Driver != "" {
		if err := validateFlatName(wire.Files.Driver); err != nil {
			return Manifest{}, fmt.Errorf("%w: invalid Files.driver: %v", ErrInvalidManifest, err)
		}
	}
	shared, err := decodeDriverShared(wire.Driver.Shared, false)
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{
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
	}, nil
}

func classifyPackageMetadataName(name string) (string, bool, error) {
	for _, expected := range []string{legacyPackageManifestName, reservedPackageMetadataName} {
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
	reserved, hasReserved := metadata[reservedPackageMetadataName]
	if hasLegacy && hasReserved {
		return "", nil, fmt.Errorf("package archive must not contain both %s and %s", legacyPackageManifestName, reservedPackageMetadataName)
	}
	if hasLegacy {
		return legacyPackageManifestName, legacy, nil
	}
	if hasReserved {
		return reservedPackageMetadataName, reserved, nil
	}
	return "", nil, fmt.Errorf("package archive must contain %s metadata", legacyPackageManifestName)
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
