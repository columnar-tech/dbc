// Copyright 2026 Columnar Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package sourceresolution

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenArtifactPathSnapshotsWithoutCleaningSource(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "packages")
	require.NoError(t, os.Mkdir(sourceDir, 0o700))
	sourcePath := filepath.Join(sourceDir, "driver.tar.gz")
	contents := []byte("local archive bytes")
	require.NoError(t, os.WriteFile(sourcePath, contents, 0o600))

	opened, err := OpenArtifact(context.Background(), nil, resolution.ArtifactLocation{
		Kind: resolution.ArtifactLocationPath, Value: "./packages/driver.tar.gz",
	}, projectDir)
	require.NoError(t, err)
	tempPath := opened.File.Name()
	got, err := io.ReadAll(opened.File)
	require.NoError(t, err)
	assert.Equal(t, contents, got)
	require.NoError(t, opened.Close())

	assert.NoFileExists(t, tempPath)
	assert.FileExists(t, sourcePath)
	assert.DirExists(t, sourceDir)
}

func TestOpenArtifactPathDoesNotUseCurrentWorkingDirectory(t *testing.T) {
	projectDir := t.TempDir()
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "archive.tgz"), []byte("from project"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(cwd, "project"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "project", "archive.tgz"), []byte("from cwd"), 0o600))
	t.Chdir(cwd)

	opened, err := OpenArtifact(context.Background(), nil, resolution.ArtifactLocation{
		Kind: resolution.ArtifactLocationPath, Value: "archive.tgz",
	}, projectDir)
	require.NoError(t, err)
	defer opened.Close()
	got, err := io.ReadAll(opened.File)
	require.NoError(t, err)
	assert.Equal(t, "from project", string(got))
}

func TestOpenArtifactURLUsesFetcherAndPreservesErrors(t *testing.T) {
	var requests []string
	fetchFailure := errors.New("HTTP 404 Not Found: missing payload")
	fetchURL := func(_ context.Context, parsed *url.URL) (io.ReadCloser, error) {
		requests = append(requests, parsed.String())
		if parsed.Path == "/missing" {
			return nil, fetchFailure
		}
		return io.NopCloser(strings.NewReader("downloaded package")), nil
	}

	opened, err := OpenArtifact(context.Background(), fetchURL, resolution.ArtifactLocation{
		Kind: resolution.ArtifactLocationURL, Value: "https://example.test/package.tar.gz",
	}, "")
	require.NoError(t, err)
	got, err := io.ReadAll(opened.File)
	require.NoError(t, err)
	assert.Equal(t, "downloaded package", string(got))
	tempPath := opened.File.Name()
	require.NoError(t, opened.Close())
	assert.NoFileExists(t, tempPath)
	assert.Equal(t, []string{"https://example.test/package.tar.gz"}, requests)

	_, err = OpenArtifact(context.Background(), fetchURL, resolution.ArtifactLocation{
		Kind: resolution.ArtifactLocationURL, Value: "https://example.test/missing",
	}, "")
	require.ErrorIs(t, err, fetchFailure)
	assert.Equal(t, []string{"https://example.test/package.tar.gz", "https://example.test/missing"}, requests)
}

func TestOpenArtifactValidatesLocationBeforeAccess(t *testing.T) {
	for _, location := range []resolution.ArtifactLocation{
		{Kind: resolution.ArtifactLocationURL, Value: "file:///tmp/archive.tar.gz"},
		{Kind: resolution.ArtifactLocationURL, Value: "https://user@example.test/archive"},
		{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/archive#fragment"},
		{Kind: resolution.ArtifactLocationPath, Value: "archive\x00.tar.gz"},
		{Kind: resolution.ArtifactLocationPath, Value: "https://example.test/archive"},
	} {
		_, err := OpenArtifact(context.Background(), nil, location, t.TempDir())
		require.Error(t, err)
		assert.Contains(t, strings.ToLower(err.Error()), "invalid artifact location")
	}
}

func TestOpenArtifactPathRejectsMissingAndNonRegularFiles(t *testing.T) {
	projectDir := t.TempDir()
	_, err := OpenArtifact(context.Background(), nil, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "missing.tgz"}, projectDir)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, os.Mkdir(filepath.Join(projectDir, "directory"), 0o700))
	_, err = OpenArtifact(context.Background(), nil, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "directory"}, projectDir)
	require.ErrorContains(t, err, "not a regular file")
}

func TestOpenArtifactPathRequiresAbsoluteBaseForRelativePath(t *testing.T) {
	_, err := OpenArtifact(context.Background(), nil, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "archive.tgz"}, "relative")
	require.ErrorContains(t, err, "absolute project base directory")
}
