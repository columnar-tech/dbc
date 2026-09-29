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
		assert.Error(t, err)
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
	})

	t.Run("invalid archive does not publish prefix", func(t *testing.T) {
		f := writePackageArchive(t, packageFile("MANIFEST", ""), packageFile("first.so", "new"), packageFile("../escape", "x"))
		out := filepath.Join(t.TempDir(), "destination")
		require.NoError(t, os.Mkdir(out, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(out, "first.so"), []byte("old"), 0o600))
		_, err := InflateTarball(f, out)
		require.Error(t, err)
		_, closeErr := f.Stat()
		assert.Error(t, closeErr)
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
	stage, _, _, err := extractPackageArchive(f, t.TempDir())
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
		{name: "reserved receipt filename", entries: []*tar.Header{packageFile("MANIFEST", ""), packageFile(strings.ToUpper(packageInstallReceiptFilename), "x")}},
		{name: "sparse", entries: []*tar.Header{packageFile("MANIFEST", ""), {Name: "sparse", Typeflag: tar.TypeGNUSparse}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := writePackageArchive(t, tt.entries...)
			defer f.Close()
			stage, _, _, err := extractPackageArchive(f, t.TempDir())
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
			_, _, _, err := extractPackageArchive(f, t.TempDir())
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
		stage, _, _, err := extractPackageArchive(f, t.TempDir())
		require.NoError(t, err)
		defer os.RemoveAll(stage)
	})

	t.Run("manifest limit", func(t *testing.T) {
		f := writeCustomPackageArchive(t, strings.Repeat("x", maxPackageManifestSize+1))
		defer f.Close()
		_, _, _, err := extractPackageArchive(f, t.TempDir())
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
		assert.Error(t, closeErr)
		assert.NoFileExists(t, filepath.Join(out, "driver.so"))
	})
}

func TestPackageArchiveLimits(t *testing.T) {
	t.Run("single declared size over production limit is rejected from header", func(t *testing.T) {
		f := writeHeaderOnlyPackageArchive(t, &tar.Header{Name: "large", Typeflag: tar.TypeReg, Size: defaultPackageArchiveLimits.entrySize + 1})
		assertArchiveLimitFailure(t, f, defaultPackageArchiveLimits)
	})

	t.Run("regular total at limit succeeds and one over fails", func(t *testing.T) {
		manifest := "name = \"Test Driver\"\nversion = \"1.0.0\"\n"
		payloadSize := int64(8)
		limits := testPackageArchiveLimits()
		limits.totalSize = int64(len(manifest)) + payloadSize
		f := writeCustomPackageArchive(t, manifest, packageFile("data", strings.Repeat("x", int(payloadSize))))
		stage, _, _, err := extractPackageArchiveWithLimits(f, t.TempDir(), limits)
		require.NoError(t, err)
		assert.NoError(t, os.RemoveAll(stage))
		assert.NoError(t, f.Close())

		limits.totalSize--
		f = writeCustomPackageArchive(t, manifest, packageFile("data", strings.Repeat("x", int(payloadSize))))
		assertArchiveLimitFailure(t, f, limits)
	})

	t.Run("zero byte entries count", func(t *testing.T) {
		limits := testPackageArchiveLimits()
		limits.entryCount = 2
		f := writePackageArchive(t, packageFile("MANIFEST", ""), packageFile("zero-one", ""), packageFile("zero-two", ""))
		assertArchiveLimitFailure(t, f, limits)
	})

	t.Run("global PAX entries count before they are skipped", func(t *testing.T) {
		limits := testPackageArchiveLimits()
		limits.entryCount = 1
		f := writePackageArchive(t,
			&tar.Header{Name: "global", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "metadata"}},
			packageFile("MANIFEST", ""),
		)
		assertArchiveLimitFailure(t, f, limits)
	})

	t.Run("local PAX metadata uses metadata budget", func(t *testing.T) {
		limits := testPackageArchiveLimits()
		limits.metadataSize = 2048
		f := writePackageArchive(t,
			packageFile("MANIFEST", ""),
			&tar.Header{Name: "payload", Typeflag: tar.TypeReg, Size: 1, Format: tar.FormatPAX, PAXRecords: map[string]string{"comment": strings.Repeat("x", 8192)}},
		)
		err := assertArchiveLimitFailure(t, f, limits)
		assert.ErrorIs(t, err, errPackageArchiveByteLimit)
	})

	t.Run("GNU long name metadata uses metadata budget", func(t *testing.T) {
		limits := testPackageArchiveLimits()
		limits.metadataSize = 2048
		f := writePackageArchive(t,
			packageFile("MANIFEST", ""),
			&tar.Header{Name: strings.Repeat("n", 2048), Typeflag: tar.TypeReg, Format: tar.FormatGNU},
		)
		err := assertArchiveLimitFailure(t, f, limits)
		assert.ErrorIs(t, err, errPackageArchiveByteLimit)
	})

	t.Run("trailing zero data is bounded after tar EOF", func(t *testing.T) {
		limits := testPackageArchiveLimits()
		limits.metadataSize = 2048
		f := writePackageArchiveWithTrailingData(t, 8192, false)
		err := assertArchiveLimitFailure(t, f, limits)
		assert.ErrorIs(t, err, errPackageArchiveByteLimit)
	})

	t.Run("later gzip member is bounded after tar EOF", func(t *testing.T) {
		limits := testPackageArchiveLimits()
		limits.metadataSize = 2048
		f := writePackageArchiveWithTrailingData(t, 8192, true)
		err := assertArchiveLimitFailure(t, f, limits)
		assert.ErrorIs(t, err, errPackageArchiveByteLimit)
	})

	t.Run("high compression ratio is allowed below absolute limits", func(t *testing.T) {
		limits := testPackageArchiveLimits()
		limits.entrySize = 32768
		limits.totalSize = 32768 + maxPackageManifestSize
		limits.metadataSize = 4096
		f := writePackageArchive(t, packageFile("MANIFEST", ""), packageFile("compressed", strings.Repeat("x", 32768)))
		stage, _, _, err := extractPackageArchiveWithLimits(f, t.TempDir(), limits)
		require.NoError(t, err)
		assert.NoError(t, os.RemoveAll(stage))
		assert.NoError(t, f.Close())
	})
}

func testPackageArchiveLimits() packageArchiveLimits {
	return packageArchiveLimits{
		manifestSize: maxPackageManifestSize,
		entrySize:    1 << 20,
		totalSize:    1 << 20,
		entryCount:   32,
		metadataSize: 16 << 10,
	}
}

func assertArchiveLimitFailure(t *testing.T, f *os.File, limits packageArchiveLimits) error {
	t.Helper()
	parent := t.TempDir()
	out := filepath.Join(parent, "destination")
	require.NoError(t, os.Mkdir(out, 0o755))
	marker := filepath.Join(out, "marker")
	require.NoError(t, os.WriteFile(marker, []byte("unchanged"), 0o600))
	_, err := inflateTarballWithLimits(f, out, limits)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "limit")
	_, closeErr := f.Stat()
	assert.Error(t, closeErr)
	data, readErr := os.ReadFile(marker)
	assert.NoError(t, readErr)
	assert.Equal(t, "unchanged", string(data))
	entries, readDirErr := os.ReadDir(parent)
	assert.NoError(t, readDirErr)
	assert.Len(t, entries, 1)
	assert.Equal(t, "destination", entries[0].Name())
	return err
}

func writeHeaderOnlyPackageArchive(t *testing.T, header *tar.Header) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "header-only.tar.gz")
	f, err := os.Create(path)
	require.NoError(t, err)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(header))
	require.NoError(t, gz.Close())
	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)
	return f
}

func writePackageArchiveWithTrailingData(t *testing.T, trailingBytes int, secondMember bool) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trailing.tar.gz")
	f, err := os.Create(path)
	require.NoError(t, err)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	manifest := "name = \"Test Driver\"\nversion = \"1.0.0\"\n"
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "MANIFEST", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(manifest))}))
	_, err = tw.Write([]byte(manifest))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	if !secondMember {
		_, err = gz.Write(make([]byte, trailingBytes))
		require.NoError(t, err)
	}
	require.NoError(t, gz.Close())
	if secondMember {
		gz = gzip.NewWriter(f)
		_, err = gz.Write(make([]byte, trailingBytes))
		require.NoError(t, err)
		require.NoError(t, gz.Close())
	}
	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)
	return f
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
