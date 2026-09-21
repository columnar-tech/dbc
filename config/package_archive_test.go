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

package config_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/columnar-tech/dbc/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type archiveEntry struct {
	name     string
	data     []byte
	typeflag byte
	linkname string
}

func packageV2Manifest(id, version, platform, driverFile string) []byte {
	return []byte(fmt.Sprintf(`package_version = 2
id = %q
name = "Example Driver"
version = %q
platform = %q

[Driver]
entrypoint = "AdbcDriverExampleInit"

[Files]
driver = %q
`, id, version, platform, driverFile))
}

func makePackageArchive(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tarWriter := tar.NewWriter(gz)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name: entry.name, Mode: 0o644, Size: int64(len(entry.data)),
			Typeflag: typeflag, Linkname: entry.linkname, Format: tar.FormatPAX,
		}
		if typeflag == tar.TypeDir {
			header.Name = strings.TrimSuffix(header.Name, "/") + "/"
			header.Size = 0
		}
		if typeflag == tar.TypeSymlink || typeflag == tar.TypeLink {
			header.Size = 0
		}
		require.NoError(t, tarWriter.WriteHeader(header))
		if len(entry.data) > 0 {
			_, err := tarWriter.Write(entry.data)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gz.Close())
	return out.Bytes()
}

func openPackageArchive(t *testing.T, data []byte) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "package-*.tar.gz")
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	_, err = file.Write(data)
	require.NoError(t, err)
	_, err = file.Seek(0, 0)
	require.NoError(t, err)
	return file
}

func differentPackagePlatform() string {
	switch config.PlatformTuple() {
	case "linux_amd64":
		return "linux_arm64"
	case "linux_arm64":
		return "linux_amd64"
	case "macos_amd64":
		return "macos_arm64"
	case "macos_arm64":
		return "macos_amd64"
	case "windows_amd64":
		return "linux_amd64"
	default:
		return "linux_amd64"
	}
}

func expectedPackage(id, version, platform, source string, archive []byte) config.ExpectedPackageMetadata {
	digest := sha256.Sum256(archive)
	return config.ExpectedPackageMetadata{
		ID: id, Version: version, PackageVersion: 2, Platform: platform,
		SourceType: "packslip", SourceIdentity: source,
		ArchiveHash: "sha256:" + hex.EncodeToString(digest[:]), ArchiveSize: int64(len(archive)),
	}
}

func validV2Archive(t *testing.T, contents []byte) []byte {
	t.Helper()
	manifest := packageV2Manifest("example", "1.2.3", config.PlatformTuple(), "libexample.so")
	return makePackageArchive(t,
		archiveEntry{name: "dbc-package.toml", data: manifest},
		archiveEntry{name: "libexample.so", data: contents},
		archiveEntry{name: "LICENSE", data: []byte("license")},
	)
}

func TestPackageArchiveMetadataVersions(t *testing.T) {
	t.Run("legacy package manifest is adapted", func(t *testing.T) {
		archive, err := os.Open(filepath.Join("..", "cmd", "dbc", "testdata", "test-driver-1.tar.gz"))
		require.NoError(t, err)
		outDir := t.TempDir()
		manifest, err := config.InflateTarball(archive, outDir)
		require.NoError(t, err)
		assert.Equal(t, "Test Driver 1", manifest.Name)
		assert.Equal(t, "1.0.0", manifest.Version.String())
		assert.FileExists(t, filepath.Join(outDir, manifest.Files.Driver))
	})

	t.Run("package v2 is decoded separately from runtime v1", func(t *testing.T) {
		data := makePackageArchive(t,
			archiveEntry{name: "dbc-package.toml", data: packageV2Manifest("example", "1.2.3", "linux_amd64", "libexample.so")},
			archiveEntry{name: "libexample.so", data: []byte("library")},
		)
		f := openPackageArchive(t, data)
		manifest, err := config.InflateTarball(f, t.TempDir())
		require.NoError(t, err)
		assert.Equal(t, "example", manifest.ID)
		assert.Equal(t, 2, manifest.PackageVersion)
		assert.Equal(t, "AdbcDriverExampleInit", manifest.Driver.Entrypoint)
		assert.Equal(t, "libexample.so", manifest.Files.Driver)
		assert.Empty(t, manifest.Driver.Shared.Get("linux_amd64"))

		inspected, err := config.InspectPackageMetadata(openPackageArchive(t, data))
		require.NoError(t, err)
		assert.Equal(t, 2, inspected.PackageVersion)
	})

	t.Run("legacy package remains distinguishable from v2", func(t *testing.T) {
		archivePath := filepath.Join("..", "cmd", "dbc", "testdata", "test-driver-1.tar.gz")
		f, err := os.Open(archivePath)
		require.NoError(t, err)
		defer f.Close()
		manifest, err := config.InspectPackageMetadata(f)
		require.NoError(t, err)
		assert.Zero(t, manifest.PackageVersion)
	})

	t.Run("unknown discriminator does not fall back to legacy", func(t *testing.T) {
		manifest := []byte("package_version = 3\nname = 'Legacy-looking name'\nversion = '1.0.0'\n\n[Files]\ndriver = 'driver.so'\n")
		data := makePackageArchive(t,
			archiveEntry{name: "dbc-package.toml", data: manifest},
			archiveEntry{name: "driver.so", data: []byte("library")},
		)
		f := openPackageArchive(t, data)
		_, err := config.InflateTarball(f, t.TempDir())
		require.Error(t, err)
		assert.ErrorIs(t, err, config.ErrInvalidManifest)
	})

	t.Run("malformed discriminator does not fall back to legacy", func(t *testing.T) {
		manifest := []byte("package_version = '2'\nname = 'Legacy-looking name'\nversion = '1.0.0'\n\n[Files]\ndriver = 'driver.so'\n")
		data := makePackageArchive(t,
			archiveEntry{name: "dbc-package.toml", data: manifest},
			archiveEntry{name: "driver.so", data: []byte("library")},
		)
		f := openPackageArchive(t, data)
		_, err := config.InflateTarball(f, t.TempDir())
		require.Error(t, err)
		assert.ErrorIs(t, err, config.ErrInvalidManifest)
	})

	t.Run("legacy manifest-only package keeps its runtime shared value", func(t *testing.T) {
		archivePath := filepath.Join("..", "cmd", "dbc", "testdata", "test-driver-manifest-only.tar.gz")
		f, err := os.Open(archivePath)
		require.NoError(t, err)
		root := t.TempDir()
		cfg := config.Config{Level: config.ConfigEnv, Location: root}
		manifest, err := config.InstallDriver(cfg, "test-driver-manifest-only", f)
		require.NoError(t, err)
		assert.Empty(t, manifest.Files.Driver)
		assert.Equal(t, "test_driver", manifest.Driver.Shared.Get(config.PlatformTuple()))

		receipt, err := os.ReadFile(filepath.Join(root, "test-driver-manifest-only", "dbc-install-receipt.json"))
		require.NoError(t, err)
		assert.NotContains(t, string(receipt), "installed_library_hash")

		require.NoError(t, config.CreateManifest(cfg, manifest.DriverInfo))
		runtimeManifest, err := os.ReadFile(filepath.Join(root, "test-driver-manifest-only.toml"))
		require.NoError(t, err)
		assert.Contains(t, string(runtimeManifest), "shared = 'test_driver'")
		assert.Contains(t, string(runtimeManifest), "source = 'dbc'")
	})

	t.Run("legacy manifest-only platform shared table keeps its runtime library path", func(t *testing.T) {
		platform := config.PlatformTuple()
		libraryPath := "/opt/external/libexample.so"
		packageManifest := []byte(fmt.Sprintf(`name = "Legacy Table Driver"
version = "1.0.0"

[Driver]
entrypoint = "AdbcDriverLegacyTableInit"

[Driver.shared]
%q = %q
`, platform, libraryPath))
		archive := makePackageArchive(t, archiveEntry{name: "MANIFEST", data: packageManifest})
		f := openPackageArchive(t, archive)
		root := t.TempDir()
		cfg := config.Config{Level: config.ConfigEnv, Location: root}
		manifest, err := config.InstallDriver(cfg, "legacy-table", f)
		require.NoError(t, err)
		assert.Empty(t, manifest.Files.Driver)
		assert.Equal(t, libraryPath, manifest.Driver.Shared.Get(platform))

		require.NoError(t, config.CreateManifest(cfg, manifest.DriverInfo))
		runtimeManifest, err := os.ReadFile(filepath.Join(root, "legacy-table.toml"))
		require.NoError(t, err)
		assert.Contains(t, string(runtimeManifest), libraryPath)
		loaded, err := config.GetDriver(cfg, "legacy-table")
		require.NoError(t, err)
		assert.Equal(t, libraryPath, loaded.Driver.Shared.Get(platform))
	})
}

func TestPackageV2RequiresStrictSemVer(t *testing.T) {
	for _, version := range []string{
		"1.2.3",
		"1.2.3-rc.1",
		"1.2.3+build.5",
		"1.2.3-rc.1+build.5",
	} {
		t.Run("accepts "+version, func(t *testing.T) {
			archive := makePackageArchive(t,
				archiveEntry{name: "dbc-package.toml", data: packageV2Manifest("example", version, config.PlatformTuple(), "driver.so")},
				archiveEntry{name: "driver.so", data: []byte("library")},
			)
			for name, inspect := range map[string]func(*os.File) (config.Manifest, error){
				"inspect": config.InspectPackageMetadata,
				"extract": func(file *os.File) (config.Manifest, error) {
					return config.InflateTarball(file, t.TempDir())
				},
			} {
				t.Run(name, func(t *testing.T) {
					manifest, err := inspect(openPackageArchive(t, archive))
					require.NoError(t, err)
					require.NotNil(t, manifest.Version)
					assert.Equal(t, version, manifest.Version.String())
				})
			}
		})
	}

	for _, version := range []string{"v1.2.3", "1.2", "01.2.3"} {
		t.Run("rejects "+version, func(t *testing.T) {
			archive := makePackageArchive(t,
				archiveEntry{name: "dbc-package.toml", data: packageV2Manifest("example", version, config.PlatformTuple(), "driver.so")},
				archiveEntry{name: "driver.so", data: []byte("library")},
			)
			for name, inspect := range map[string]func(*os.File) error{
				"inspect": func(file *os.File) error {
					_, err := config.InspectPackageMetadata(file)
					return err
				},
				"extract": func(file *os.File) error {
					_, err := config.InflateTarball(file, t.TempDir())
					return err
				},
			} {
				t.Run(name, func(t *testing.T) {
					err := inspect(openPackageArchive(t, archive))
					require.ErrorIs(t, err, config.ErrInvalidManifest)
					assert.Contains(t, err.Error(), "must be valid SemVer 2.0.0")
				})
			}
		})
	}

	t.Run("legacy manifest keeps permissive version parsing", func(t *testing.T) {
		archive := makePackageArchive(t, archiveEntry{name: "MANIFEST", data: []byte(`name = "Legacy Driver"
version = "1.2"

[Driver]
shared = "legacy_driver"
`)})
		manifest, err := config.InspectPackageMetadata(openPackageArchive(t, archive))
		require.NoError(t, err)
		require.NotNil(t, manifest.Version)
		assert.Equal(t, "1.2.0", manifest.Version.String())
	})
}

func TestPackageArchiveMetadataFilenames(t *testing.T) {
	legacy := []byte("name = 'Legacy Driver'\nversion = '1.0.0'\n")
	v2 := packageV2Manifest("example", "1.2.3", config.PlatformTuple(), "driver.so")
	v2WithRuntimeVersion := bytes.Replace(v2, []byte("id = "), []byte("manifest_version = 1\nid = "), 1)
	tests := []struct {
		name        string
		entries     []archiveEntry
		wantMessage string
	}{
		{
			name:        "missing metadata",
			entries:     []archiveEntry{{name: "driver.so", data: []byte("library")}},
			wantMessage: "must contain exactly one of MANIFEST or dbc-package.toml",
		},
		{
			name: "both formats",
			entries: []archiveEntry{
				{name: "MANIFEST", data: legacy},
				{name: "dbc-package.toml", data: v2},
				{name: "driver.so", data: []byte("library")},
			},
			wantMessage: "not both",
		},
		{
			name:        "legacy filename requires legacy format",
			entries:     []archiveEntry{{name: "MANIFEST", data: v2}, {name: "driver.so", data: []byte("library")}},
			wantMessage: "package_version is not allowed in legacy MANIFEST",
		},
		{
			name:        "v2 filename requires package version",
			entries:     []archiveEntry{{name: "dbc-package.toml", data: []byte("name = 'Legacy-looking name'\nversion = '1.0.0'\n")}, {name: "driver.so", data: []byte("library")}},
			wantMessage: "package_version = 2 is required",
		},
		{
			name:        "v2 metadata rejects runtime manifest version",
			entries:     []archiveEntry{{name: "dbc-package.toml", data: v2WithRuntimeVersion}, {name: "driver.so", data: []byte("library")}},
			wantMessage: "must not set runtime manifest_version",
		},
		{
			name:        "legacy filename case mismatch",
			entries:     []archiveEntry{{name: "manifest", data: legacy}},
			wantMessage: `must be named exactly "MANIFEST"`,
		},
		{
			name:        "v2 filename case mismatch",
			entries:     []archiveEntry{{name: "dbc-Package.toml", data: v2}},
			wantMessage: `must be named exactly "dbc-package.toml"`,
		},
		{
			name: "legacy case collision",
			entries: []archiveEntry{
				{name: "MANIFEST", data: legacy},
				{name: "manifest", data: legacy},
			},
			wantMessage: "collide by name",
		},
		{
			name: "v2 case collision",
			entries: []archiveEntry{
				{name: "dbc-package.toml", data: v2},
				{name: "DBC-PACKAGE.TOML", data: v2},
			},
			wantMessage: "collide by name",
		},
		{
			name: "duplicate v2 metadata",
			entries: []archiveEntry{
				{name: "dbc-package.toml", data: v2},
				{name: "dbc-package.toml", data: v2},
			},
			wantMessage: "collide by name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := makePackageArchive(t, tt.entries...)
			for name, inspect := range map[string]func(*os.File) error{
				"inspect": func(file *os.File) error {
					_, err := config.InspectPackageMetadata(file)
					return err
				},
				"extract": func(file *os.File) error {
					_, err := config.InflateTarball(file, t.TempDir())
					return err
				},
			} {
				t.Run(name, func(t *testing.T) {
					err := inspect(openPackageArchive(t, archive))
					require.Error(t, err)
					assert.Contains(t, err.Error(), tt.wantMessage)
				})
			}
		})
	}
}

func TestInspectPackageMetadataScansPastMetadata(t *testing.T) {
	archive := makePackageArchive(t,
		archiveEntry{name: "dbc-package.toml", data: packageV2Manifest("example", "1.2.3", config.PlatformTuple(), "driver.so")},
		archiveEntry{name: "driver.so", data: []byte("library")},
		archiveEntry{name: "nested/file", data: []byte("unsafe")},
	)
	_, err := config.InspectPackageMetadata(openPackageArchive(t, archive))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path separators")
}

func TestPackageMetadataSizeLimitAppliesToBothFormats(t *testing.T) {
	oversized := bytes.Repeat([]byte("x"), (1<<20)+1)
	for _, name := range []string{"MANIFEST", "dbc-package.toml"} {
		t.Run(name, func(t *testing.T) {
			archive := makePackageArchive(t, archiveEntry{name: name, data: oversized})
			for _, inspect := range []func(*os.File) error{
				func(file *os.File) error {
					_, err := config.InspectPackageMetadata(file)
					return err
				},
				func(file *os.File) error {
					_, err := config.InflateTarball(file, t.TempDir())
					return err
				},
			} {
				err := inspect(openPackageArchive(t, archive))
				require.Error(t, err)
				assert.Contains(t, err.Error(), "exceeds 1 MiB")
			}
		})
	}
}

func TestInstallDriverRejectsNilArchive(t *testing.T) {
	_, err := config.InstallDriver(config.Config{}, "example", nil)
	require.Error(t, err)
}

func TestInflateTarballRejectsArchiveAttacks(t *testing.T) {
	manifest := []byte("name = 'Legacy Driver'\nversion = '1.0.0'\n\n[Files]\ndriver = 'driver.so'\n")
	tests := []struct {
		name    string
		entries []archiveEntry
	}{
		{name: "absolute unix path", entries: []archiveEntry{{name: "MANIFEST", data: manifest}, {name: "/tmp/escape", data: []byte("bad")}}},
		{name: "absolute windows path", entries: []archiveEntry{{name: "MANIFEST", data: manifest}, {name: `C:\escape`, data: []byte("bad")}}},
		{name: "parent path", entries: []archiveEntry{{name: "MANIFEST", data: manifest}, {name: "../escape", data: []byte("bad")}}},
		{name: "nested path", entries: []archiveEntry{{name: "MANIFEST", data: manifest}, {name: "nested/file", data: []byte("bad")}}},
		{name: "windows reserved name", entries: []archiveEntry{{name: "MANIFEST", data: manifest}, {name: "CON.txt", data: []byte("bad")}}},
		{name: "duplicate case-folded names", entries: []archiveEntry{{name: "MANIFEST", data: manifest}, {name: "driver.so", data: []byte("one")}, {name: "DRIVER.SO", data: []byte("two")}}},
		{name: "duplicate manifest", entries: []archiveEntry{{name: "MANIFEST", data: manifest}, {name: "MANIFEST", data: manifest}}},
		{name: "directory", entries: []archiveEntry{{name: "MANIFEST", data: manifest}, {name: "nested", typeflag: tar.TypeDir}}},
		{name: "symlink", entries: []archiveEntry{{name: "MANIFEST", data: manifest}, {name: "link", typeflag: tar.TypeSymlink, linkname: "driver.so"}}},
		{name: "hardlink", entries: []archiveEntry{{name: "MANIFEST", data: manifest}, {name: "link", typeflag: tar.TypeLink, linkname: "driver.so"}}},
		{name: "manifest missing", entries: []archiveEntry{{name: "driver.so", data: []byte("library")}}},
		{name: "manifest case mismatch", entries: []archiveEntry{{name: "manifest", data: manifest}, {name: "driver.so", data: []byte("library")}}},
		{name: "driver missing", entries: []archiveEntry{{name: "MANIFEST", data: manifest}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := makePackageArchive(t, tt.entries...)
			outDir := t.TempDir()
			f := openPackageArchive(t, data)
			_, err := config.InflateTarball(f, outDir)
			require.Error(t, err)
			entries, readErr := os.ReadDir(outDir)
			require.NoError(t, readErr)
			assert.Empty(t, entries, "failed extraction must not publish partial files")
		})
	}
}

func TestInstallPackageChecksMetadataAndDigests(t *testing.T) {
	archive := validV2Archive(t, []byte("library"))
	tests := []struct {
		name   string
		mutate func(*config.ExpectedPackageMetadata)
	}{
		{name: "id mismatch", mutate: func(e *config.ExpectedPackageMetadata) { e.ID = "other" }},
		{name: "version mismatch", mutate: func(e *config.ExpectedPackageMetadata) { e.Version = "1.2.4" }},
		{name: "platform mismatch", mutate: func(e *config.ExpectedPackageMetadata) { e.Platform = differentPackagePlatform() }},
		{name: "archive hash mismatch", mutate: func(e *config.ExpectedPackageMetadata) { e.ArchiveHash = "sha256:" + strings.Repeat("0", 63) + "1" }},
		{name: "archive size mismatch", mutate: func(e *config.ExpectedPackageMetadata) { e.ArchiveSize++ }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := config.Config{Level: config.ConfigEnv, Location: root}
			expected := expectedPackage("example", "1.2.3", config.PlatformTuple(), "github.com/example/project", archive)
			tt.mutate(&expected)
			f := openPackageArchive(t, archive)
			_, err := config.InstallPackage(cfg, "example", f, expected, config.InstallOptions{})
			_ = f.Close()
			require.Error(t, err)
			assert.NoDirExists(t, filepath.Join(root, "example"))
		})
	}
}

func TestPackslipPackageVersionRejectsLegacyArchiveBeforeRuntimeMutation(t *testing.T) {
	legacy := []byte(`name = "Legacy Driver"
version = "1.2.3"

[Driver]
shared = "external-driver"
`)
	archive := makePackageArchive(t, archiveEntry{name: "MANIFEST", data: legacy})
	root := t.TempDir()
	cfg := config.Config{Level: config.ConfigEnv, Location: root}
	existingArchive := validV2Archive(t, []byte("existing"))
	existingFile := openPackageArchive(t, existingArchive)
	installed, err := config.InstallPackage(cfg, "example", existingFile,
		expectedPackage("example", "1.2.3", config.PlatformTuple(), "existing", existingArchive), config.InstallOptions{})
	require.NoError(t, err)
	require.NoError(t, existingFile.Close())
	beforeManifest, err := os.ReadFile(filepath.Join(root, "example.toml"))
	require.NoError(t, err)
	beforeLibrary, err := os.ReadFile(installed.Driver.Shared.Get(config.PlatformTuple()))
	require.NoError(t, err)

	candidate := expectedPackage("example", "1.2.3", config.PlatformTuple(), "packslip-project", archive)
	candidateFile := openPackageArchive(t, archive)
	validation, err := config.PreparePackage(cfg, "example", candidateFile, candidate, config.InstallOptions{})
	require.ErrorContains(t, err, "dbc package version mismatch: archive declares 0, expected 2")
	if validation.Prepared != nil {
		require.NoError(t, validation.Prepared.Close())
	}
	_, err = candidateFile.Seek(0, 0)
	require.NoError(t, err)
	_, err = config.InstallPackage(cfg, "example", candidateFile, candidate, config.InstallOptions{})
	require.ErrorContains(t, err, "dbc package version mismatch")
	require.NoError(t, candidateFile.Close())

	afterManifest, err := os.ReadFile(filepath.Join(root, "example.toml"))
	require.NoError(t, err)
	afterLibrary, err := os.ReadFile(installed.Driver.Shared.Get(config.PlatformTuple()))
	require.NoError(t, err)
	assert.Equal(t, beforeManifest, afterManifest)
	assert.Equal(t, beforeLibrary, afterLibrary)
}

func TestPreparePackageIsNonMutatingAndPreparedPackageCanBeInstalled(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Level: config.ConfigEnv, Location: root}
	oldArchive := validV2Archive(t, []byte("existing library"))
	oldFile := openPackageArchive(t, oldArchive)
	oldManifest, err := config.InstallPackage(cfg, "example", oldFile,
		expectedPackage("example", "1.2.3", config.PlatformTuple(), "old-source", oldArchive), config.InstallOptions{})
	require.NoError(t, err)
	require.NoError(t, oldFile.Close())
	oldRegistration, err := os.ReadFile(filepath.Join(root, "example.toml"))
	require.NoError(t, err)
	oldLibrary, err := os.ReadFile(oldManifest.Driver.Shared.Get(config.PlatformTuple()))
	require.NoError(t, err)
	entriesBefore, err := os.ReadDir(root)
	require.NoError(t, err)

	candidateLibrary := []byte("candidate verified library")
	candidateArchive := validV2Archive(t, candidateLibrary)
	candidate := expectedPackage("example", "1.2.3", config.PlatformTuple(), "candidate-source", candidateArchive)
	candidateFile := openPackageArchive(t, candidateArchive)
	defer candidateFile.Close()
	verifyCalls := 0
	var validationStagingDir string
	options := config.InstallOptions{Verify: func(stagingDir string, manifest config.Manifest) error {
		verifyCalls++
		validationStagingDir = stagingDir
		got, readErr := os.ReadFile(filepath.Join(stagingDir, manifest.Files.Driver))
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(got, candidateLibrary) {
			return fmt.Errorf("staged library = %q, want %q", got, candidateLibrary)
		}
		return nil
	}}
	validation, err := config.PreparePackage(cfg, "example", candidateFile, candidate, options)
	require.NoError(t, err)
	require.NotNil(t, validation.Prepared)
	t.Cleanup(func() { require.NoError(t, validation.Prepared.Close()) })
	wantLibraryHash := sha256.Sum256(candidateLibrary)
	assert.Equal(t, "sha256:"+hex.EncodeToString(wantLibraryHash[:]), validation.VerifiedLibraryHash)
	assert.NotEqual(t, candidate.ArchiveHash, validation.VerifiedLibraryHash)
	assert.Equal(t, 1, verifyCalls)
	_, err = os.Stat(validationStagingDir)
	assert.NoError(t, err, "prepared payload remains staged until it is installed or closed")

	registrationAfterValidation, err := os.ReadFile(filepath.Join(root, "example.toml"))
	require.NoError(t, err)
	assert.Equal(t, oldRegistration, registrationAfterValidation)
	currentLibrary, err := os.ReadFile(oldManifest.Driver.Shared.Get(config.PlatformTuple()))
	require.NoError(t, err)
	assert.Equal(t, oldLibrary, currentLibrary)
	entriesAfter, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Len(t, entriesAfter, len(entriesBefore)+1, "preparation adds one private workspace without changing the registered package")
	assert.Contains(t, entryNames(entriesAfter), filepath.Base(filepath.Dir(validationStagingDir)))

	ensure, err := config.EnsurePackage(context.Background(), cfg, "example", candidate, config.EnsurePackageCallbacks{
		CurrentMatches: func(*config.DriverInfo) (bool, error) { return false, nil },
		Prepare:        func(context.Context) (*config.PreparedPackage, error) { return validation.Prepared, nil },
	})
	require.NoError(t, err, "the prepared payload should remain reusable for EnsurePackage")
	require.NotNil(t, ensure.Manifest)
	assert.Equal(t, 1, verifyCalls, "prepared package verification is reused for installation")
	_, err = os.Stat(validationStagingDir)
	assert.ErrorIs(t, err, os.ErrNotExist, "EnsurePackage closes the consumed prepared workspace")
	installedLibrary, err := os.ReadFile(ensure.Manifest.Driver.Shared.Get(config.PlatformTuple()))
	require.NoError(t, err)
	assert.Equal(t, candidateLibrary, installedLibrary)
}

func TestPreparePackageRejectsMetadataAndVerifierFailures(t *testing.T) {
	archive := validV2Archive(t, []byte("library"))
	mutations := []struct {
		name   string
		mutate func(*config.ExpectedPackageMetadata)
	}{
		{name: "id", mutate: func(expected *config.ExpectedPackageMetadata) { expected.ID = "other" }},
		{name: "version", mutate: func(expected *config.ExpectedPackageMetadata) { expected.Version = "1.2.4" }},
		{name: "platform", mutate: func(expected *config.ExpectedPackageMetadata) { expected.Platform = differentPackagePlatform() }},
		{name: "archive hash", mutate: func(expected *config.ExpectedPackageMetadata) {
			expected.ArchiveHash = "sha256:" + strings.Repeat("0", 64)
		}},
		{name: "archive size", mutate: func(expected *config.ExpectedPackageMetadata) { expected.ArchiveSize++ }},
	}
	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			expected := expectedPackage("example", "1.2.3", config.PlatformTuple(), "source", archive)
			tt.mutate(&expected)
			file := openPackageArchive(t, archive)
			defer file.Close()
			cfg := config.Config{Level: config.ConfigEnv, Location: t.TempDir()}
			validation, err := config.PreparePackage(cfg, "example", file, expected, config.InstallOptions{})
			if validation.Prepared != nil {
				require.NoError(t, validation.Prepared.Close())
			}
			require.Error(t, err)
		})
	}

	t.Run("signature verifier error", func(t *testing.T) {
		verifyErr := errors.New("signature rejected")
		var stagingDir string
		file := openPackageArchive(t, archive)
		defer file.Close()
		cfg := config.Config{Level: config.ConfigEnv, Location: t.TempDir()}
		validation, err := config.PreparePackage(cfg, "example", file,
			expectedPackage("example", "1.2.3", config.PlatformTuple(), "source", archive),
			config.InstallOptions{Verify: func(dir string, _ config.Manifest) error {
				stagingDir = dir
				return verifyErr
			}})
		if validation.Prepared != nil {
			require.NoError(t, validation.Prepared.Close())
		}
		require.ErrorIs(t, err, verifyErr)
		_, statErr := os.Stat(stagingDir)
		assert.ErrorIs(t, statErr, os.ErrNotExist)
	})
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestInstallPackageReceiptReplacementAndRuntimeManifest(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Level: config.ConfigEnv, Location: root}
	firstArchive := validV2Archive(t, []byte("first library"))
	first := expectedPackage("example", "1.2.3", config.PlatformTuple(), "github.com/first/source", firstArchive)
	f := openPackageArchive(t, firstArchive)
	manifest, err := config.InstallPackage(cfg, "example", f, first, config.InstallOptions{})
	require.NoError(t, err)
	_ = f.Close()
	assert.Equal(t, "dbc", manifest.Source)
	firstLibraryPath := manifest.Driver.Shared.Get(config.PlatformTuple())
	assert.FileExists(t, firstLibraryPath)

	firstReceiptPath := filepath.Join(filepath.Dir(firstLibraryPath), "dbc-install-receipt.json")
	firstReceiptBytes, err := os.ReadFile(firstReceiptPath)
	require.NoError(t, err)
	var firstReceipt config.InstallReceipt
	require.NoError(t, json.Unmarshal(firstReceiptBytes, &firstReceipt))
	assert.Equal(t, "github.com/first/source", firstReceipt.SourceIdentity)
	assert.Equal(t, first.ArchiveHash, firstReceipt.ArchiveHash)
	assert.Equal(t, 2, firstReceipt.PackageVersion)
	assert.True(t, config.InstallReceiptMatchesExpectedPackage(firstReceipt, first), "the new package version is part of the receipt proof")
	assert.NotEqual(t, firstReceipt.ArchiveHash, firstReceipt.InstalledLibraryHash)
	installedDigest := sha256.Sum256([]byte("first library"))
	assert.Equal(t, "sha256:"+hex.EncodeToString(installedDigest[:]), firstReceipt.InstalledLibraryHash)

	secondArchive := validV2Archive(t, []byte("second library"))
	second := expectedPackage("example", "1.2.3", config.PlatformTuple(), "github.com/second/source", secondArchive)
	f = openPackageArchive(t, secondArchive)
	manifest, err = config.InstallPackage(cfg, "example", f, second, config.InstallOptions{})
	require.NoError(t, err)
	_ = f.Close()
	assert.Equal(t, "dbc", manifest.Source, "runtime source retains dbc uninstall semantics")
	secondLibraryPath := manifest.Driver.Shared.Get(config.PlatformTuple())
	contents, err := os.ReadFile(secondLibraryPath)
	require.NoError(t, err)
	assert.Equal(t, "second library", string(contents))

	secondReceiptPath := filepath.Join(filepath.Dir(secondLibraryPath), "dbc-install-receipt.json")
	secondReceiptBytes, err := os.ReadFile(secondReceiptPath)
	require.NoError(t, err)
	var secondReceipt config.InstallReceipt
	require.NoError(t, json.Unmarshal(secondReceiptBytes, &secondReceipt))
	assert.Equal(t, "github.com/second/source", secondReceipt.SourceIdentity)
	assert.Equal(t, second.ArchiveHash, secondReceipt.ArchiveHash)
	assert.NotEqual(t, firstReceipt.ArchiveHash, secondReceipt.ArchiveHash)

	require.NoError(t, config.CreateManifest(cfg, manifest.DriverInfo))
	runtimeManifestPath := filepath.Join(root, "example.toml")
	runtimeBytes, err := os.ReadFile(runtimeManifestPath)
	require.NoError(t, err)
	runtimeText := string(runtimeBytes)
	assert.Contains(t, runtimeText, "manifest_version = 1")
	assert.Contains(t, runtimeText, "source = 'dbc'")
	assert.Contains(t, runtimeText, "[Driver.shared]")
	assert.NotContains(t, runtimeText, "package_version")
	assert.NotContains(t, runtimeText, "[Files]")

	loaded, err := config.GetDriver(cfg, "example")
	require.NoError(t, err)
	assert.Equal(t, "dbc", loaded.Source)
	assert.Equal(t, secondLibraryPath, loaded.Driver.Shared.Get(config.PlatformTuple()))
}

func TestInstallReceiptPackageVersionProofPreservesUnspecifiedSources(t *testing.T) {
	expected := config.ExpectedPackageMetadata{
		ID: "example", Version: "1.2.3", PackageVersion: 2, Platform: config.PlatformTuple(),
		SourceType: "packslip", SourceIdentity: "github.com/example/driver",
		ArchiveHash: "sha256:" + strings.Repeat("a", 64), ArchiveSize: 10,
	}
	receipt := config.InstallReceipt{
		DriverID: "example", DriverVersion: "1.2.3", PackageVersion: 2, Platform: config.PlatformTuple(),
		SourceType: "packslip", SourceIdentity: "github.com/example/driver",
		ArchiveHash: expected.ArchiveHash, ArchiveSize: expected.ArchiveSize,
	}
	assert.True(t, config.InstallReceiptMatchesExpectedPackage(receipt, expected))
	receipt.PackageVersion = 0
	assert.False(t, config.InstallReceiptMatchesExpectedPackage(receipt, expected), "old receipts cannot prove the Packslip package contract")
	expected.PackageVersion = 0
	assert.True(t, config.InstallReceiptMatchesExpectedPackage(receipt, expected), "registry and local legacy sources retain unspecified package-version matching")
}

func TestFailedReplacementPreservesExistingPackage(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Level: config.ConfigEnv, Location: root}
	archive := validV2Archive(t, []byte("installed library"))
	expected := expectedPackage("example", "1.2.3", config.PlatformTuple(), "github.com/example/source", archive)
	f := openPackageArchive(t, archive)
	installed, err := config.InstallPackage(cfg, "example", f, expected, config.InstallOptions{})
	require.NoError(t, err)
	_ = f.Close()
	originalLibraryPath := installed.Driver.Shared.Get(config.PlatformTuple())
	originalReceiptPath := filepath.Join(filepath.Dir(originalLibraryPath), "dbc-install-receipt.json")
	originalLibrary, err := os.ReadFile(originalLibraryPath)
	require.NoError(t, err)
	originalReceipt, err := os.ReadFile(originalReceiptPath)
	require.NoError(t, err)

	badExpected := expected
	badExpected.ArchiveHash = "sha256:" + strings.Repeat("0", 64)
	f = openPackageArchive(t, validV2Archive(t, []byte("replacement library")))
	_, err = config.InstallPackage(cfg, "example", f, badExpected, config.InstallOptions{})
	_ = f.Close()
	require.Error(t, err)
	currentLibrary, err := os.ReadFile(originalLibraryPath)
	require.NoError(t, err)
	currentReceipt, err := os.ReadFile(originalReceiptPath)
	require.NoError(t, err)
	assert.Equal(t, originalLibrary, currentLibrary)
	assert.Equal(t, originalReceipt, currentReceipt)
}

func TestConcurrentInstallPackageSerializesSameTarget(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Level: config.ConfigEnv, Location: root}
	firstArchive := validV2Archive(t, []byte("first library"))
	secondArchive := validV2Archive(t, []byte("second library"))
	firstExpected := expectedPackage("example", "1.2.3", config.PlatformTuple(), "github.com/first/source", firstArchive)
	secondExpected := expectedPackage("example", "1.2.3", config.PlatformTuple(), "github.com/second/source", secondArchive)
	firstFile := openPackageArchive(t, firstArchive)
	secondFile := openPackageArchive(t, secondArchive)

	results := make(chan error, 2)
	go func() {
		_, err := config.InstallPackage(cfg, "example", firstFile, firstExpected, config.InstallOptions{})
		_ = firstFile.Close()
		results <- err
	}()
	go func() {
		_, err := config.InstallPackage(cfg, "example", secondFile, secondExpected, config.InstallOptions{})
		_ = secondFile.Close()
		results <- err
	}()
	for range 2 {
		require.NoError(t, <-results)
	}

	installed, err := config.GetDriver(cfg, "example")
	require.NoError(t, err)
	libraryPath := installed.Driver.Shared.Get(config.PlatformTuple())
	var receipt config.InstallReceipt
	receiptBytes, err := os.ReadFile(filepath.Join(filepath.Dir(libraryPath), "dbc-install-receipt.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(receiptBytes, &receipt))
	library, err := os.ReadFile(libraryPath)
	require.NoError(t, err)
	switch receipt.SourceIdentity {
	case firstExpected.SourceIdentity:
		assert.Equal(t, "first library", string(library))
		assert.Equal(t, firstExpected.ArchiveHash, receipt.ArchiveHash)
	case secondExpected.SourceIdentity:
		assert.Equal(t, "second library", string(library))
		assert.Equal(t, secondExpected.ArchiveHash, receipt.ArchiveHash)
	default:
		t.Fatalf("unexpected source identity in receipt: %q", receipt.SourceIdentity)
	}
}
