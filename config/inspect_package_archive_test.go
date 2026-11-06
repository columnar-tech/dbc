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
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInspectPackageArchive(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)

	archive := testPackageArchive(t, "library bytes")
	info, err := InspectPackageArchive(archive)
	require.NoError(t, err)

	libSum := sha256.Sum256([]byte("library bytes"))
	assert.Equal(t, fileSHA256(t, archive), info.ArchiveSHA256)
	assert.Equal(t, hex.EncodeToString(libSum[:]), info.LibrarySHA256)
	assert.Equal(t, "driver.so", info.Manifest.Files.Driver)
	assert.Equal(t, "1.0.0", info.Manifest.Version.String())

	entries, err := os.ReadDir(tmp)
	require.NoError(t, err)
	assert.Empty(t, entries, "no staging directory may be left behind")

	// The file stays open and installable.
	_, err = archive.Seek(3, io.SeekStart)
	require.NoError(t, err)
	cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
	_, err = InstallPackage(context.Background(), cfg, "driver", archive, InstallPackageOptions{ExpectedArchiveSHA256: info.ArchiveSHA256})
	require.NoError(t, err)
}

func TestInspectPackageArchiveRejectsMalformed(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)

	for name, f := range map[string]*os.File{
		"traversal":  writePackageArchive(t, packageFile("MANIFEST", ""), packageFile("driver.so", "x"), packageFile("../escape", "x")),
		"no driver":  writePackageArchive(t, packageFile("MANIFEST", "")),
		"directory":  writePackageArchive(t, packageFile("MANIFEST", ""), &tar.Header{Name: "subdir", Typeflag: tar.TypeDir}),
		"not a gzip": notGzipFile(t),
	} {
		_, err := InspectPackageArchive(f)
		require.Error(t, err, name)
		// InspectPackageArchive leaves f open for the caller; Windows cannot
		// remove the temporary directory while it is open.
		require.NoError(t, f.Close())
	}
	entries, err := os.ReadDir(tmp)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func notGzipFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "bad")
	require.NoError(t, err)
	_, err = f.WriteString(strings.Repeat("not gzip", 10))
	require.NoError(t, err)
	return f
}

func TestCheckInstalledPackageLibrarySHA256(t *testing.T) {
	requireReuseSupported(t)
	cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
	digest := installReusePackage(t, cfg, InstallPackageOptions{})
	libSum := sha256.Sum256([]byte("new"))

	expect := InstalledPackageExpectation{Version: semver.MustParse("1.0.0"), ArchiveSHA256: digest, LibrarySHA256: hex.EncodeToString(libSum[:])}
	_, ok, err := CheckInstalledPackage(context.Background(), cfg, "driver", expect)
	require.NoError(t, err)
	assert.True(t, ok)

	expect.LibrarySHA256 = strings.Repeat("0", 64)
	assertNotReusable(t, cfg, expect)

	for _, bad := range []string{"abc", strings.Repeat("A", 64)} {
		expect.LibrarySHA256 = bad
		_, ok, err := CheckInstalledPackage(context.Background(), cfg, "driver", expect)
		require.Error(t, err, bad)
		assert.False(t, ok)
	}
}
