// Copyright 2026 Columnar Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package sourceresolution

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/columnar-tech/dbc/internal/packslip"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type packslipResolverStub struct {
	release resolution.ResolvedRelease
	err     error
	source  packslip.PackslipSource
	request packslip.Request
}

func (stub *packslipResolverStub) Resolve(_ context.Context, source packslip.PackslipSource, request packslip.Request) (resolution.ResolvedRelease, error) {
	stub.source, stub.request = source, request
	return stub.release, stub.err
}

func TestResolvePackslipUsesDeclarationAndValidatesRelease(t *testing.T) {
	release := resolvedPackslipRelease("github.com/example/driver", "1.2.3", "example")
	resolver := &packslipResolverStub{release: release}
	got, err := ResolvePackslip(context.Background(), resolver, "GitHub.com/Example/Driver", "example", "1.2.3")
	require.NoError(t, err)
	assert.Equal(t, release, got)
	assert.Equal(t, "GitHub.com/Example/Driver", resolver.source.Project)
	assert.Equal(t, packslip.Request{DriverID: "example", Version: "1.2.3"}, resolver.request)
}

func TestResolvePackslipRejectsMismatchedMetadata(t *testing.T) {
	tests := []struct {
		name   string
		change func(*resolution.ResolvedRelease)
		want   string
	}{
		{name: "driver ID", change: func(r *resolution.ResolvedRelease) { r.DriverID = "other" }, want: "driver ID"},
		{name: "version", change: func(r *resolution.ResolvedRelease) { r.Version = "1.2.4" }, want: "version"},
		{name: "source type", change: func(r *resolution.ResolvedRelease) { r.Source.Type = "registry" }, want: "source identity"},
		{name: "source identity", change: func(r *resolution.ResolvedRelease) { r.Source.Reference = "github.com/other/driver" }, want: "source identity"},
		{name: "artifact hash", change: func(r *resolution.ResolvedRelease) { r.Artifacts[0].Hash = "" }, want: "no finalized hash"},
		{name: "artifact size", change: func(r *resolution.ResolvedRelease) { r.Artifacts[0].Size = nil }, want: "no finalized size"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			release := resolvedPackslipRelease("github.com/example/driver", "1.2.3", "example")
			test.change(&release)
			_, err := ResolvePackslip(context.Background(), &packslipResolverStub{release: release}, "github.com/example/driver", "example", "1.2.3")
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestResolvePackslipPreservesUnsupportedError(t *testing.T) {
	want := fmt.Errorf("%w: unsupported runtime", packslip.ErrUnsupported)
	resolver := &packslipResolverStub{err: want}
	_, err := ResolvePackslip(context.Background(), resolver, "github.com/example/driver", "example", "1.2.3")
	require.ErrorIs(t, err, packslip.ErrUnsupported)
}

func TestResolvePackslipRequiresExactSemver(t *testing.T) {
	resolver := &packslipResolverStub{}
	_, err := ResolvePackslip(context.Background(), resolver, "github.com/example/driver", "example", ">=1.0.0")
	require.ErrorContains(t, err, "exact SemVer")
	assert.Empty(t, resolver.request.Version, "invalid requests must not reach the resolver")
}

func TestResolvePathUsesProjectBaseAndPreservesDeclaration(t *testing.T) {
	projectDir := t.TempDir()
	cwd := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(projectDir, "packages"), 0o700))
	archive := testPackageArchive(t, "dbc-package.toml", v2Metadata("example", "1.2.3", "linux_amd64"))
	archivePath := filepath.Join(projectDir, "packages", "driver.tar.gz")
	require.NoError(t, os.WriteFile(archivePath, archive, 0o600))
	t.Chdir(cwd)

	declaration := "./packages/driver.tar.gz"
	release, err := ResolvePath(context.Background(), declaration, Request{
		DriverID: "example", Version: "1.2.3", Target: resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"},
		Platform: "linux_amd64", BaseDir: projectDir,
	})
	require.NoError(t, err)
	require.Equal(t, "path", release.Source.Type)
	assert.Equal(t, declaration, release.Source.Reference)
	require.Len(t, release.Artifacts, 1)
	artifact := release.Artifacts[0]
	assert.Equal(t, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: declaration}, artifact.Location)
	assert.Equal(t, "tar.gz", artifact.Format)
	assert.Equal(t, int64(len(archive)), *artifact.Size)
	assert.Equal(t, digest(archive), artifact.Hash)
	assert.Equal(t, resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, artifact.Target)
}

func TestResolvePathRejectsV2DriverIDAndPlatformMismatch(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata []byte
		platform string
		want     string
	}{
		{name: "driver ID", metadata: v2Metadata("other", "1.2.3", "linux_amd64"), platform: "linux_amd64", want: "ID"},
		{name: "platform", metadata: v2Metadata("example", "1.2.3", "macos_arm64"), platform: "linux_amd64", want: "platform"},
	} {
		t.Run(test.name, func(t *testing.T) {
			projectDir := t.TempDir()
			archive := testPackageArchive(t, "dbc-package.toml", test.metadata)
			require.NoError(t, os.WriteFile(filepath.Join(projectDir, "driver.tar.gz"), archive, 0o600))
			_, err := ResolvePath(context.Background(), "driver.tar.gz", Request{
				DriverID: "example", Version: "1.2.3", Target: resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"},
				Platform: test.platform, BaseDir: projectDir,
			})
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestResolvePathAdaptsLegacyPackageToRequestedIdentityAndTarget(t *testing.T) {
	projectDir := t.TempDir()
	archive := testPackageArchive(t, "MANIFEST", []byte("manifest_version = 1\nname = 'Legacy Driver'\nversion = '1.2.3'\n\n[Driver]\nshared = 'libexample.so'\n"))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "legacy.tgz"), archive, 0o600))

	release, err := ResolvePath(context.Background(), "legacy.tgz", Request{
		DriverID: "example", Version: "1.2.3", Target: resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"},
		Platform: "linux_amd64", BaseDir: projectDir,
	})
	require.NoError(t, err)
	assert.Equal(t, "example", release.DriverID)
	assert.Equal(t, resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, release.Artifacts[0].Target)
}

func TestResolvePathDetectsMissingAndMutatedArchives(t *testing.T) {
	projectDir := t.TempDir()
	path := filepath.Join(projectDir, "driver.tar.gz")
	firstBytes := testPackageArchive(t, "dbc-package.toml", v2Metadata("example", "1.2.3", "linux_amd64"))
	secondBytes := testPackageArchive(t, "dbc-package.toml", v2Metadata("example", "1.2.3", "linux_amd64"))
	secondBytes = append(secondBytes, 0)
	require.NoError(t, os.WriteFile(path, firstBytes, 0o600))
	request := Request{DriverID: "example", Version: "1.2.3", Target: resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, Platform: "linux_amd64", BaseDir: projectDir}
	first, err := ResolvePath(context.Background(), "driver.tar.gz", request)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, secondBytes, 0o600))
	second, err := ResolvePath(context.Background(), "driver.tar.gz", request)
	require.NoError(t, err)
	assert.NotEqual(t, first.Artifacts[0].Hash, second.Artifacts[0].Hash)
	assert.NotEqual(t, *first.Artifacts[0].Size, *second.Artifacts[0].Size)

	require.NoError(t, os.Remove(path))
	_, err = ResolvePath(context.Background(), "driver.tar.gz", request)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func resolvedPackslipRelease(project, version, driverID string) resolution.ResolvedRelease {
	size := int64(17)
	return resolution.ResolvedRelease{
		DriverID: driverID,
		Version:  version,
		Source:   resolution.SourceSpec{Type: "packslip", Reference: project},
		Artifacts: []resolution.Artifact{{
			Target: resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, Format: "tar.gz",
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/driver.tar.gz"},
			Hash:     "sha256:" + strings.Repeat("a", 64), Size: &size,
		}},
	}
}

func v2Metadata(id, version, platform string) []byte {
	return []byte(fmt.Sprintf("package_version = 2\nid = %q\nname = 'Example Driver'\nversion = %q\nplatform = %q\n\n[Driver]\nentrypoint = 'ExampleInit'\n\n[Files]\ndriver = 'driver.so'\n", id, version, platform))
}

func testPackageArchive(t *testing.T, metadataName string, metadata []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zipper := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(zipper)
	require.NoError(t, writer.WriteHeader(&tar.Header{Name: metadataName, Mode: 0o600, Size: int64(len(metadata)), Typeflag: tar.TypeReg}))
	_, err := writer.Write(metadata)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, zipper.Close())
	return buffer.Bytes()
}

func digest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestResolvePathRequiresDeclaredVersion(t *testing.T) {
	projectDir := t.TempDir()
	archive := testPackageArchive(t, "dbc-package.toml", v2Metadata("example", "1.2.3", "linux_amd64"))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "driver.tar.gz"), archive, 0o600))
	_, err := ResolvePath(context.Background(), "driver.tar.gz", Request{
		DriverID: "example", Version: "1.2.4", Target: resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"},
		Platform: "linux_amd64", BaseDir: projectDir,
	})
	require.ErrorContains(t, err, "does not match requested version")
}

func TestResolvePathDerivesOmittedVersionFromPackageMetadata(t *testing.T) {
	projectDir := t.TempDir()
	archive := testPackageArchive(t, "dbc-package.toml", v2Metadata("example", "1.2.3", "linux_amd64"))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "driver.tar.gz"), archive, 0o600))

	release, err := ResolvePath(context.Background(), "driver.tar.gz", Request{
		DriverID: "example", Target: resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"},
		Platform: "linux_amd64", BaseDir: projectDir,
	})
	require.NoError(t, err)
	assert.Equal(t, "1.2.3", release.Version)
}

func TestResolvePathRejectsNonArchiveExtension(t *testing.T) {
	projectDir := t.TempDir()
	archive := testPackageArchive(t, "dbc-package.toml", v2Metadata("example", "1.2.3", "linux_amd64"))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "driver.zip"), archive, 0o600))
	_, err := ResolvePath(context.Background(), "driver.zip", Request{
		DriverID: "example", Version: "1.2.3", Target: resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"},
		Platform: "linux_amd64", BaseDir: projectDir,
	})
	require.ErrorContains(t, err, "must use .tar.gz or .tgz")
}

func TestResolvePathRequiresAbsoluteProjectBase(t *testing.T) {
	_, err := ResolvePath(context.Background(), "driver.tar.gz", Request{
		DriverID: "example", Version: "1.2.3", Target: resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"},
		Platform: "linux_amd64", BaseDir: "relative",
	})
	require.ErrorContains(t, err, "must be absolute")
}

func TestResolvePathRequiresPlatformTargetConsistency(t *testing.T) {
	projectDir := t.TempDir()
	archive := testPackageArchive(t, "dbc-package.toml", v2Metadata("example", "1.2.3", "linux_amd64"))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "driver.tar.gz"), archive, 0o600))
	_, err := ResolvePath(context.Background(), "driver.tar.gz", Request{
		DriverID: "example", Version: "1.2.3", Target: resolution.Target{OS: "macos", Arch: "arm64"},
		Platform: "linux_amd64", BaseDir: projectDir,
	})
	require.ErrorContains(t, err, "does not match platform tuple")
}

func TestResolvePathSnapshotsCanonicalTargetAliases(t *testing.T) {
	projectDir := t.TempDir()
	archive := testPackageArchive(t, "MANIFEST", []byte("manifest_version = 1\nname = 'Legacy Driver'\nversion = '1.2.3'\n\n[Driver]\nshared = 'libexample.so'\n"))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "legacy.tgz"), archive, 0o600))

	tests := []struct {
		name     string
		target   resolution.Target
		platform string
		want     resolution.Target
	}{
		{
			name:   "Darwin and x86_64 aliases",
			target: resolution.Target{OS: "darwin", Arch: "x86_64"}, platform: "darwin_x86_64",
			want: resolution.Target{OS: "macos", Arch: "amd64"},
		},
		{
			name:   "Linux defaults to GNU libc",
			target: resolution.Target{OS: "linux", Arch: "amd64"}, platform: "linux_amd64",
			want: resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			release, err := ResolvePath(context.Background(), "legacy.tgz", Request{
				DriverID: "example", Version: "1.2.3", Target: test.target,
				Platform: test.platform, BaseDir: projectDir,
			})
			require.NoError(t, err)
			require.Len(t, release.Artifacts, 1)
			assert.Equal(t, test.want, release.Artifacts[0].Target)
		})
	}
}
