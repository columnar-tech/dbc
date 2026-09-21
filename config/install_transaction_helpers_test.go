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
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"testing"
)

type installArchiveEntry struct {
	name string
	data []byte
}

func makeInstallArchive(t *testing.T, id, version, library string, libraryData []byte) []byte {
	t.Helper()
	manifest := []byte(fmt.Sprintf(`package_version = 2
id = %q
name = "Example Driver"
version = %q
platform = %q

[Driver]
entrypoint = "AdbcDriverExampleInit"

[Files]
driver = %q
`, id, version, PlatformTuple(), library))
	return makeInstallArchiveWithEntries(t,
		installArchiveEntry{name: "dbc-package.toml", data: manifest},
		installArchiveEntry{name: library, data: libraryData},
	)
}

func makeInstallArchiveWithEntries(t *testing.T, entries ...installArchiveEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	writer := tar.NewWriter(gz)
	for _, entry := range entries {
		err := writer.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o644, Size: int64(len(entry.data)), Typeflag: tar.TypeReg, Format: tar.FormatPAX})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func writeInstallArchive(t *testing.T, data []byte, name string) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), name+"-*.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	return file
}

func installExpected(id, source string, data []byte) ExpectedPackageMetadata {
	digest := sha256.Sum256(data)
	return ExpectedPackageMetadata{
		ID: id, Version: "1.0.0", Platform: PlatformTuple(),
		SourceType: "registry", SourceIdentity: source,
		ArchiveHash: "sha256:" + hex.EncodeToString(digest[:]), ArchiveSize: int64(len(data)),
	}
}
