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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	_, err = file.Write(data)
	require.NoError(t, err)
	_, err = file.Seek(0, 0)
	require.NoError(t, err)
	return file
}

func expectedPackage(id, version, platform, source string, archive []byte) config.ExpectedPackageMetadata {
	digest := sha256.Sum256(archive)
	return config.ExpectedPackageMetadata{
		ID: id, Version: version, Platform: platform,
		SourceType: "packslip", SourceIdentity: source,
		ArchiveHash: "sha256:" + hex.EncodeToString(digest[:]), ArchiveSize: int64(len(archive)),
	}
}

func validV2Archive(t *testing.T, contents []byte) []byte {
	t.Helper()
	manifest := packageV2Manifest("example", "1.2.3", config.PlatformTuple(), "libexample.so")
	return makePackageArchive(t,
		archiveEntry{name: "MANIFEST", data: manifest},
		archiveEntry{name: "libexample.so", data: contents},
		archiveEntry{name: "LICENSE", data: []byte("license")},
	)
}

func TestPackageArchiveManifestVersions(t *testing.T) {
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
			archiveEntry{name: "MANIFEST", data: packageV2Manifest("example", "1.2.3", "linux_amd64", "libexample.so")},
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

		inspected, err := config.InspectPackageManifest(openPackageArchive(t, data))
		require.NoError(t, err)
		assert.Equal(t, 2, inspected.PackageVersion)
	})

	t.Run("legacy package remains distinguishable from v2", func(t *testing.T) {
		archivePath := filepath.Join("..", "cmd", "dbc", "testdata", "test-driver-1.tar.gz")
		f, err := os.Open(archivePath)
		require.NoError(t, err)
		defer f.Close()
		manifest, err := config.InspectPackageManifest(f)
		require.NoError(t, err)
		assert.Zero(t, manifest.PackageVersion)
	})

	t.Run("unknown discriminator does not fall back to legacy", func(t *testing.T) {
		manifest := []byte("package_version = 3\nname = 'Legacy-looking name'\nversion = '1.0.0'\n\n[Files]\ndriver = 'driver.so'\n")
		data := makePackageArchive(t,
			archiveEntry{name: "MANIFEST", data: manifest},
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
			archiveEntry{name: "MANIFEST", data: manifest},
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

func TestInstallPackageArchiveChecksMetadataAndDigests(t *testing.T) {
	archive := validV2Archive(t, []byte("library"))
	tests := []struct {
		name   string
		mutate func(*config.ExpectedPackageMetadata)
	}{
		{name: "id mismatch", mutate: func(e *config.ExpectedPackageMetadata) { e.ID = "other" }},
		{name: "version mismatch", mutate: func(e *config.ExpectedPackageMetadata) { e.Version = "1.2.4" }},
		{name: "platform mismatch", mutate: func(e *config.ExpectedPackageMetadata) { e.Platform = "macos_arm64" }},
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
			_, err := config.InstallPackageArchive(cfg, f, expected)
			_ = f.Close()
			require.Error(t, err)
			assert.NoDirExists(t, filepath.Join(root, "example"))
		})
	}
}

func TestInstallPackageArchiveReceiptReplacementAndRuntimeManifest(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Level: config.ConfigEnv, Location: root}
	firstArchive := validV2Archive(t, []byte("first library"))
	first := expectedPackage("example", "1.2.3", config.PlatformTuple(), "github.com/first/source", firstArchive)
	f := openPackageArchive(t, firstArchive)
	manifest, err := config.InstallPackageArchive(cfg, f, first)
	require.NoError(t, err)
	_ = f.Close()
	assert.Equal(t, "dbc", manifest.Source)
	assert.FileExists(t, filepath.Join(root, "example", "libexample.so"))

	receiptPath := filepath.Join(root, "example", "dbc-install-receipt.json")
	firstReceiptBytes, err := os.ReadFile(receiptPath)
	require.NoError(t, err)
	var firstReceipt config.InstallReceipt
	require.NoError(t, json.Unmarshal(firstReceiptBytes, &firstReceipt))
	assert.Equal(t, "github.com/first/source", firstReceipt.SourceIdentity)
	assert.Equal(t, first.ArchiveHash, firstReceipt.ArchiveHash)
	assert.NotEqual(t, firstReceipt.ArchiveHash, firstReceipt.InstalledLibraryHash)
	installedDigest := sha256.Sum256([]byte("first library"))
	assert.Equal(t, "sha256:"+hex.EncodeToString(installedDigest[:]), firstReceipt.InstalledLibraryHash)

	secondArchive := validV2Archive(t, []byte("second library"))
	second := expectedPackage("example", "1.2.3", config.PlatformTuple(), "github.com/second/source", secondArchive)
	f = openPackageArchive(t, secondArchive)
	manifest, err = config.InstallPackageArchive(cfg, f, second)
	require.NoError(t, err)
	_ = f.Close()
	assert.Equal(t, "dbc", manifest.Source, "runtime source retains dbc uninstall semantics")
	contents, err := os.ReadFile(filepath.Join(root, "example", "libexample.so"))
	require.NoError(t, err)
	assert.Equal(t, "second library", string(contents))

	secondReceiptBytes, err := os.ReadFile(receiptPath)
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
	assert.Equal(t, filepath.Join(root, "example", "libexample.so"), loaded.Driver.Shared.Get(config.PlatformTuple()))
}

func TestFailedReplacementPreservesExistingPackage(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Level: config.ConfigEnv, Location: root}
	archive := validV2Archive(t, []byte("installed library"))
	expected := expectedPackage("example", "1.2.3", config.PlatformTuple(), "github.com/example/source", archive)
	f := openPackageArchive(t, archive)
	_, err := config.InstallPackageArchive(cfg, f, expected)
	require.NoError(t, err)
	_ = f.Close()
	originalLibrary, err := os.ReadFile(filepath.Join(root, "example", "libexample.so"))
	require.NoError(t, err)
	originalReceipt, err := os.ReadFile(filepath.Join(root, "example", "dbc-install-receipt.json"))
	require.NoError(t, err)

	badExpected := expected
	badExpected.ArchiveHash = "sha256:" + strings.Repeat("0", 64)
	f = openPackageArchive(t, validV2Archive(t, []byte("replacement library")))
	_, err = config.InstallPackageArchive(cfg, f, badExpected)
	_ = f.Close()
	require.Error(t, err)
	currentLibrary, err := os.ReadFile(filepath.Join(root, "example", "libexample.so"))
	require.NoError(t, err)
	currentReceipt, err := os.ReadFile(filepath.Join(root, "example", "dbc-install-receipt.json"))
	require.NoError(t, err)
	assert.Equal(t, originalLibrary, currentLibrary)
	assert.Equal(t, originalReceipt, currentReceipt)
}

func TestConcurrentInstallPackageArchiveSerializesSameTarget(t *testing.T) {
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
		_, err := config.InstallPackageArchive(cfg, firstFile, firstExpected)
		_ = firstFile.Close()
		results <- err
	}()
	go func() {
		_, err := config.InstallPackageArchive(cfg, secondFile, secondExpected)
		_ = secondFile.Close()
		results <- err
	}()
	for range 2 {
		require.NoError(t, <-results)
	}

	var receipt config.InstallReceipt
	receiptBytes, err := os.ReadFile(filepath.Join(root, "example", "dbc-install-receipt.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(receiptBytes, &receipt))
	library, err := os.ReadFile(filepath.Join(root, "example", "libexample.so"))
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
