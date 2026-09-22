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

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/packslip"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/stretchr/testify/require"
)

func mustTestInstallItem(t *testing.T, release resolution.ResolvedRelease, platform string, lockEntry *lockInfo) installItem {
	t.Helper()
	item, err := newInstallItem(release, 0, platform, lockEntry)
	require.NoError(t, err)
	return item
}

type syncPackslipResolverStub struct {
	release resolution.ResolvedRelease
	calls   int
	project string
	request packslip.Request
}

func (stub *syncPackslipResolverStub) Resolve(_ context.Context, source packslip.PackslipSource, request packslip.Request) (resolution.ResolvedRelease, error) {
	stub.calls++
	stub.project = source.Project
	stub.request = request
	return cloneResolvedReleaseForSync(stub.release), nil
}

func makeSyncPackageV2Archive(t *testing.T, path, id, version, platform string) ([]byte, string) {
	t.Helper()
	metadata := []byte(fmt.Sprintf(`package_version = 2
id = %q
name = "Test Driver"
version = %q
platform = %q

[Driver]
entrypoint = "TestDriverInit"

[Files]
driver = "driver.so"
`, id, version, platform))
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range []struct {
		name string
		data []byte
	}{{name: "dbc-package.toml", data: metadata}, {name: "driver.so", data: []byte("test library bytes")}} {
		require.NoError(t, tarWriter.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o600, Size: int64(len(entry.data)), Typeflag: tar.TypeReg}))
		_, err := tarWriter.Write(entry.data)
		require.NoError(t, err)
	}
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())
	archiveBytes := archive.Bytes()
	require.NoError(t, os.WriteFile(path, archiveBytes, 0o600))
	digest, err := checksum(path)
	require.NoError(t, err)
	return archiveBytes, "sha256:" + digest
}

func makeSyncPackslipRelease(id, version, url, hash string, size int64) resolution.ResolvedRelease {
	primary := testTarget(config.PlatformTuple())
	secondary := testTarget(differentTestPlatformTuple(config.PlatformTuple()))
	otherSize := int64(9)
	return resolution.ResolvedRelease{
		DriverID: id,
		Version:  version,
		Source:   resolution.SourceSpec{Type: "packslip", Reference: "github.com/example/driver"},
		Evidence: []resolution.Evidence{{
			Kind:     resolution.EvidenceKindReleaseMetadata,
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://github.com/example/driver/releases/download/v1.2.3/packslip.sigstore.json"},
			Hash:     "sha256:" + strings.Repeat("a", 64),
		}},
		Artifacts: []resolution.Artifact{
			{Target: primary, Format: "tgz", PackageVersion: 2, Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: url}, Hash: hash, Size: &size},
			{Target: secondary, Format: "tar.gz", PackageVersion: 2, Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://assets.example.test/macos.tar.gz"}, Hash: "sha256:" + strings.Repeat("b", 64), Size: &otherSize},
		},
	}
}

func differentTestPlatformTuple(platform string) string {
	host, err := resolution.TargetFromPlatformTuple(platform)
	if err != nil {
		panic(err)
	}
	for _, candidate := range []string{"linux_amd64", "linux_arm64", "macos_amd64", "macos_arm64", "windows_amd64"} {
		target, err := resolution.TargetFromPlatformTuple(candidate)
		if err != nil {
			panic(err)
		}
		if target != host {
			return candidate
		}
	}
	panic("no alternate test platform is available")
}
