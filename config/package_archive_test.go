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
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writePackageArchive(t *testing.T, entries ...*tar.Header) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package.tar.gz")
	f, err := os.Create(path)
	require.NoError(t, err)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, header := range entries {
		var body string
		if header.Name == "MANIFEST" {
			body = "name = \"Test Driver\"\nversion = \"1.0.0\"\n"
			header.Size = int64(len(body))
		} else if header.Size > 0 {
			body = strings.Repeat("x", int(header.Size))
		}
		require.NoError(t, tw.WriteHeader(header))
		if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
			_, err = tw.Write([]byte(body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)
	return f
}

func packageFile(name, body string) *tar.Header {
	return &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o4755, Size: int64(len(body))}
}

func TestInflateTarballStagesAndPublishes(t *testing.T) {
	t.Run("empty destination and nonzero input offset", func(t *testing.T) {
		f := writePackageArchive(t, packageFile("MANIFEST", ""), packageFile("driver.so", "driver"))
		_, err := f.Seek(7, io.SeekStart)
		require.NoError(t, err)
		out := t.TempDir()
		manifest, err := InflateTarball(f, out)
		require.NoError(t, err)
		assert.Equal(t, "Test Driver", manifest.Name)
		data, err := os.ReadFile(filepath.Join(out, "driver.so"))
		require.NoError(t, err)
		assert.Equal(t, "xxxxxx", string(data))
		_, err = f.Stat()
		assert.ErrorIs(t, err, os.ErrClosed)
	})

	t.Run("nonempty destination overwrites and retains other files", func(t *testing.T) {
		f := writePackageArchive(t, packageFile("MANIFEST", ""), packageFile("driver.so", "new"))
		out := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(out, "driver.so"), []byte("old"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(out, "keep.txt"), []byte("keep"), 0o600))
		_, err := InflateTarball(f, out)
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join(out, "driver.so"))
		require.NoError(t, err)
		assert.Equal(t, "xxx", string(data))
		assert.FileExists(t, filepath.Join(out, "keep.txt"))
		info, err := os.Stat(filepath.Join(out, "driver.so"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})

	t.Run("new destination does not inherit executable or setuid archive mode", func(t *testing.T) {
		f := writePackageArchive(t, packageFile("MANIFEST", ""), packageFile("driver.so", "x"))
		out := t.TempDir()
		_, err := InflateTarball(f, out)
		require.NoError(t, err)
		info, err := os.Stat(filepath.Join(out, "driver.so"))
		require.NoError(t, err)
		assert.Zero(t, info.Mode().Perm()&0o111)
		assert.Zero(t, info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky))
		baseline, err := os.Create(filepath.Join(out, "mode-baseline"))
		require.NoError(t, err)
		baselineInfo, err := baseline.Stat()
		require.NoError(t, err)
		require.NoError(t, baseline.Close())
		assert.Equal(t, baselineInfo.Mode().Perm(), info.Mode().Perm())
	})

	t.Run("invalid archive does not publish prefix", func(t *testing.T) {
		f := writePackageArchive(t, packageFile("MANIFEST", ""), packageFile("first.so", "new"), packageFile("../escape", "x"))
		out := filepath.Join(t.TempDir(), "destination")
		require.NoError(t, os.Mkdir(out, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(out, "first.so"), []byte("old"), 0o600))
		_, err := InflateTarball(f, out)
		require.Error(t, err)
		_, closeErr := f.Stat()
		assert.ErrorIs(t, closeErr, os.ErrClosed)
		data, err := os.ReadFile(filepath.Join(out, "first.so"))
		require.NoError(t, err)
		assert.Equal(t, "old", string(data))
		assert.NoFileExists(t, filepath.Join(out, "escape"))
	})

	t.Run("manifest only archive", func(t *testing.T) {
		f := writePackageArchive(t, packageFile("MANIFEST", ""))
		_, err := InflateTarball(f, t.TempDir())
		require.NoError(t, err)
	})
}

func TestPackageArchiveAcceptsPAXMetadata(t *testing.T) {
	manifest := "name = \"Test Driver\"\nversion = \"1.0.0\"\n"
	f := writePackageArchive(t,
		&tar.Header{Name: "global", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "global metadata"}},
		&tar.Header{Name: "MANIFEST", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(manifest)), Format: tar.FormatPAX, ModTime: time.Unix(1, 123456789)},
		&tar.Header{Name: "driver.so", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1, Format: tar.FormatPAX, PAXRecords: map[string]string{"comment": "file metadata"}},
	)
	defer f.Close()
	stage, _, err := extractPackageArchive(f, t.TempDir())
	require.NoError(t, err)
	defer os.RemoveAll(stage)
	assert.FileExists(t, filepath.Join(stage, "driver.so"))
}

func TestPackageArchiveRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries []*tar.Header
	}{
		{name: "absolute", entries: []*tar.Header{packageFile("MANIFEST", ""), packageFile("/tmp/escape", "x")}},
		{name: "traversal", entries: []*tar.Header{packageFile("MANIFEST", ""), packageFile("../escape", "x")}},
		{name: "separator", entries: []*tar.Header{packageFile("MANIFEST", ""), packageFile("sub\\file", "x")}},
		{name: "control", entries: []*tar.Header{packageFile("MANIFEST", ""), packageFile("bad\nname", "x")}},
		{name: "trailing dot", entries: []*tar.Header{packageFile("MANIFEST", ""), packageFile("bad.", "x")}},
		{name: "trailing space", entries: []*tar.Header{packageFile("MANIFEST", ""), packageFile("bad ", "x")}},
		{name: "reserved", entries: []*tar.Header{packageFile("MANIFEST", ""), packageFile("CON.txt", "x")}},
		{name: "directory", entries: []*tar.Header{packageFile("MANIFEST", ""), {Name: "subdir", Typeflag: tar.TypeDir}}},
		{name: "symlink", entries: []*tar.Header{packageFile("MANIFEST", ""), {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "target"}}},
		{name: "hardlink", entries: []*tar.Header{packageFile("MANIFEST", ""), {Name: "link", Typeflag: tar.TypeLink, Linkname: "target"}}},
		{name: "device", entries: []*tar.Header{packageFile("MANIFEST", ""), {Name: "device", Typeflag: tar.TypeChar}}},
		{name: "duplicate", entries: []*tar.Header{packageFile("MANIFEST", ""), packageFile("same", "a"), packageFile("same", "b")}},
		{name: "case collision", entries: []*tar.Header{packageFile("MANIFEST", ""), packageFile("Foo", "a"), packageFile("foo", "b")}},
		{name: "sparse", entries: []*tar.Header{packageFile("MANIFEST", ""), {Name: "sparse", Typeflag: tar.TypeGNUSparse}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := writePackageArchive(t, tt.entries...)
			defer f.Close()
			stage, _, err := extractPackageArchive(f, t.TempDir())
			assert.Error(t, err)
			assert.Empty(t, stage)
		})
	}
}

func TestPackageArchiveRejectsSparsePAXMetadata(t *testing.T) {
	assert.True(t, hasSparsePackagePAXRecord(map[string]string{"GNU.sparse.size": "10"}))
	assert.True(t, hasSparsePackagePAXRecord(map[string]string{"GNU.sparse": "value"}))
	assert.False(t, hasSparsePackagePAXRecord(map[string]string{"comment": "metadata"}))
}

func TestPackageArchiveValidatesManifestReferences(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
	}{
		{name: "missing driver", manifest: "name = \"Driver\"\nversion = \"1.0.0\"\n[Files]\ndriver = \"missing.so\"\n"},
		{name: "missing signature", manifest: "name = \"Driver\"\nversion = \"1.0.0\"\n[Files]\nsignature = \"missing.sig\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := writeCustomPackageArchive(t, tt.manifest, packageFile("other", "x"))
			defer f.Close()
			_, _, err := extractPackageArchive(f, t.TempDir())
			assert.Error(t, err)
		})
	}
}

func TestPackageArchiveRejectsLargeManifestAndBadGzipFooter(t *testing.T) {
	t.Run("manifest at limit", func(t *testing.T) {
		manifest := "name = \"Driver\"\nversion = \"1.0.0\"\n"
		manifest += strings.Repeat(" ", maxPackageManifestSize-len(manifest))
		f := writeCustomPackageArchive(t, manifest)
		defer f.Close()
		stage, _, err := extractPackageArchive(f, t.TempDir())
		require.NoError(t, err)
		defer os.RemoveAll(stage)
	})

	t.Run("manifest limit", func(t *testing.T) {
		f := writeCustomPackageArchive(t, strings.Repeat("x", maxPackageManifestSize+1))
		defer f.Close()
		_, _, err := extractPackageArchive(f, t.TempDir())
		assert.ErrorContains(t, err, "exceeds")
	})

	t.Run("gzip checksum", func(t *testing.T) {
		f := writePackageArchive(t, packageFile("MANIFEST", ""), packageFile("driver.so", "x"))
		_, err := f.Seek(-1, io.SeekEnd)
		require.NoError(t, err)
		last := []byte{0}
		_, err = f.Read(last)
		require.NoError(t, err)
		last[0] ^= 0xff
		_, err = f.Seek(-1, io.SeekEnd)
		require.NoError(t, err)
		_, err = f.Write(last)
		require.NoError(t, err)
		out := t.TempDir()
		_, err = InflateTarball(f, out)
		assert.Error(t, err)
		_, closeErr := f.Stat()
		assert.ErrorIs(t, closeErr, os.ErrClosed)
		assert.NoFileExists(t, filepath.Join(out, "driver.so"))
	})
}

func writeCustomPackageArchive(t *testing.T, manifest string, payloads ...*tar.Header) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package.tar.gz")
	f, err := os.Create(path)
	require.NoError(t, err)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	mh := &tar.Header{Name: "MANIFEST", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(manifest))}
	require.NoError(t, tw.WriteHeader(mh))
	_, err = tw.Write([]byte(manifest))
	require.NoError(t, err)
	for _, header := range payloads {
		body := strings.Repeat("x", int(header.Size))
		require.NoError(t, tw.WriteHeader(header))
		if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
			_, err = tw.Write([]byte(body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)
	return f
}
