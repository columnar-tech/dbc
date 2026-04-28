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
	"testing"

	"github.com/Masterminds/semver/v3"
)

func TestAllVersions(t *testing.T) {
	tests := []struct {
		name     string
		driver   Driver
		expected []VersionInfo
	}{
		{
			name: "no packages",
			driver: Driver{
				Registry: &Registry{
					BaseURL: mustParseURL("https://example.com"),
				},
				Path:    "test-driver",
				PkgInfo: []pkginfo{},
			},
			expected: []VersionInfo{},
		},
		{
			name: "absolute URL package",
			driver: Driver{
				Registry: &Registry{
					BaseURL: mustParseURL("https://example.com"),
				},
				Path: "test-driver",
				PkgInfo: []pkginfo{
					{
						Version: semver.MustParse("1.0.0"),
						Packages: []struct {
							PlatformTuple string `yaml:"platform"`
							URL           string `yaml:"url"`
						}{
							{
								PlatformTuple: "linux-x64",
								URL:           "https://other.com/driver.tar.gz",
							},
						},
					},
				},
			},
			expected: []VersionInfo{
				{
					Version: semver.MustParse("1.0.0"),
					Packages: []PackageInfo{
						{
							Platform: "linux-x64",
							URL:      "https://other.com/driver.tar.gz",
						},
					},
				},
			},
		},
		{
			name: "relative URL package",
			driver: Driver{
				Registry: &Registry{
					BaseURL: mustParseURL("https://example.com"),
				},
				Path: "test-driver",
				PkgInfo: []pkginfo{
					{
						Version: semver.MustParse("1.0.0"),
						Packages: []struct {
							PlatformTuple string `yaml:"platform"`
							URL           string `yaml:"url"`
						}{
							{
								PlatformTuple: "linux-x64",
								URL:           "drivers/test-driver-1.0.0.tar.gz",
							},
						},
					},
				},
			},
			expected: []VersionInfo{
				{
					Version: semver.MustParse("1.0.0"),
					Packages: []PackageInfo{
						{
							Platform: "linux-x64",
							URL:      "https://example.com/drivers/test-driver-1.0.0.tar.gz",
						},
					},
				},
			},
		},
		{
			name: "empty URL package",
			driver: Driver{
				Registry: &Registry{
					BaseURL: mustParseURL("https://example.com"),
				},
				Path: "test-driver",
				PkgInfo: []pkginfo{
					{
						Version: semver.MustParse("1.0.0"),
						Packages: []struct {
							PlatformTuple string `yaml:"platform"`
							URL           string `yaml:"url"`
						}{
							{
								PlatformTuple: "linux-x64",
								URL:           "",
							},
						},
					},
				},
			},
			expected: []VersionInfo{
				{
					Version: semver.MustParse("1.0.0"),
					Packages: []PackageInfo{
						{
							Platform: "linux-x64",
							URL:      "https://example.com/test-driver/1.0.0/test-driver_linux-x64-1.0.0.tar.gz",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.driver.AllVersions()

			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d versions, got %d", len(tt.expected), len(result))
			}

			for i, v := range result {
				if !v.Version.Equal(tt.expected[i].Version) {
					t.Errorf("version mismatch at index %d: expected %s, got %s", i, tt.expected[i].Version, v.Version)
				}

				if len(v.Packages) != len(tt.expected[i].Packages) {
					t.Errorf("package count mismatch at index %d: expected %d, got %d", i, len(tt.expected[i].Packages), len(v.Packages))
				}

				for j, pkg := range v.Packages {
					if pkg.Platform != tt.expected[i].Packages[j].Platform {
						t.Errorf("platform mismatch at [%d][%d]: expected %s, got %s", i, j, tt.expected[i].Packages[j].Platform, pkg.Platform)
					}
					if pkg.URL != tt.expected[i].Packages[j].URL {
						t.Errorf("URL mismatch at [%d][%d]: expected %s, got %s", i, j, tt.expected[i].Packages[j].URL, pkg.URL)
					}
				}
			}
		})
	}
}
