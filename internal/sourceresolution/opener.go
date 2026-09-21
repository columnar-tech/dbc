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
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/columnar-tech/dbc/internal/resolution"
)

// URLFetcher retrieves one URL artifact. Callers may adapt an authenticated
// client such as dbc.Client.Download without coupling this package to its API.
type URLFetcher func(context.Context, *url.URL) (io.ReadCloser, error)

// OpenedArtifact owns a private temporary copy of an artifact. Close removes
// only that temporary copy; local source paths and their parent directories
// are never cleanup targets.
type OpenedArtifact struct {
	File    *os.File
	tempDir string
	once    sync.Once
	err     error
}

func (artifact *OpenedArtifact) Close() error {
	if artifact == nil {
		return nil
	}
	artifact.once.Do(func() {
		if artifact.File != nil {
			artifact.err = artifact.File.Close()
		}
		if artifact.tempDir != "" {
			if err := os.RemoveAll(artifact.tempDir); err != nil {
				artifact.err = errors.Join(artifact.err, err)
			}
		}
	})
	return artifact.err
}

// OpenArtifact validates and copies a URL or path artifact to a private
// temporary file so either source can be consumed identically. URL retrieval
// uses the caller-provided fetcher; callers can adapt Client.Download to retain
// dbc authentication and HTTP error behavior. Relative path locations are
// resolved against the absolute project directory.
func OpenArtifact(ctx context.Context, fetchURL URLFetcher, location resolution.ArtifactLocation, baseDir string) (*OpenedArtifact, error) {
	if err := resolution.ValidateArtifactLocation(location); err != nil {
		return nil, fmt.Errorf("invalid artifact location: %w", err)
	}
	if ctx == nil {
		return nil, errors.New("artifact open requires a context")
	}
	tempDir, err := os.MkdirTemp("", "dbc-artifact-")
	if err != nil {
		return nil, fmt.Errorf("create artifact snapshot directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }
	tempFile, err := os.CreateTemp(tempDir, "artifact-*")
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create artifact snapshot file: %w", err)
	}
	closeOnError := func(err error) (*OpenedArtifact, error) {
		_ = tempFile.Close()
		cleanup()
		return nil, err
	}

	var source io.ReadCloser
	switch location.Kind {
	case resolution.ArtifactLocationURL:
		if fetchURL == nil {
			return closeOnError(errors.New("URL artifact requires a URL fetcher"))
		}
		parsed, err := url.Parse(location.Value)
		if err != nil {
			return closeOnError(fmt.Errorf("parse artifact URL: %w", err))
		}
		source, err = fetchURL(ctx, parsed)
		if err != nil {
			return closeOnError(err)
		}
	case resolution.ArtifactLocationPath:
		resolvedPath, err := resolveDeclaredPath(location.Value, baseDir)
		if err != nil {
			return closeOnError(err)
		}
		file, err := os.Open(resolvedPath)
		if err != nil {
			return closeOnError(fmt.Errorf("open local artifact %q: %w", location.Value, err))
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return closeOnError(fmt.Errorf("inspect local artifact %q: %w", location.Value, err))
		}
		if !info.Mode().IsRegular() {
			_ = file.Close()
			return closeOnError(fmt.Errorf("local artifact %q is not a regular file", location.Value))
		}
		source = file
	default:
		return closeOnError(fmt.Errorf("unsupported artifact location kind %q", location.Kind))
	}
	defer source.Close()
	if _, err := io.Copy(tempFile, contextReader{ctx: ctx, reader: source}); err != nil {
		return closeOnError(fmt.Errorf("snapshot artifact: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return closeOnError(err)
	}
	if err := tempFile.Sync(); err != nil {
		return closeOnError(fmt.Errorf("sync artifact snapshot: %w", err))
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		return closeOnError(fmt.Errorf("rewind artifact snapshot: %w", err))
	}
	return &OpenedArtifact{File: tempFile, tempDir: tempDir}, nil
}

func resolveDeclaredPath(declared, baseDir string) (string, error) {
	if filepath.IsAbs(declared) {
		return filepath.Clean(declared), nil
	}
	if !filepath.IsAbs(baseDir) {
		return "", errors.New("relative artifact path requires an absolute project base directory")
	}
	return filepath.Clean(filepath.Join(baseDir, declared)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
