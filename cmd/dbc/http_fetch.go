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
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/columnar-tech/dbc"
)

// downloadHTTPPkg downloads the archive of a driver with an HTTP source into
// a private temporary directory, without registry credentials.
func downloadHTTPPkg(ctx context.Context, p dbc.PkgInfo) (*os.File, error) {
	client, err := currentClient()
	if err != nil {
		return nil, err
	}
	if p.Path == nil {
		return nil, fmt.Errorf("cannot download package for %s: no url set", p.Driver.Title)
	}
	body, err := client.OpenAnonymous(ctx, p.Path)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	dir, err := os.MkdirTemp("", "adbc-drivers-*")
	if err != nil {
		return nil, err
	}
	// A fixed name: the URL path need not be a usable file name.
	f, err := os.Create(filepath.Join(dir, "archive.tar.gz"))
	if err == nil {
		_, err = io.Copy(f, body)
	}
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		if f != nil {
			f.Close()
		}
		os.RemoveAll(dir)
		return nil, fmt.Errorf("failed to write driver file: %w", err)
	}
	return f, nil
}

// fetchHTTPFile reads a small file of an HTTP source, such as a checksum
// file, without registry credentials.
func fetchHTTPFile(ctx context.Context, u *url.URL) ([]byte, error) {
	client, err := currentClient()
	if err != nil {
		return nil, err
	}
	return readHTTPFile(func(u *url.URL) (io.ReadCloser, error) {
		return client.OpenAnonymous(ctx, u)
	}, u)
}

// readHTTPFile reads a small file of an HTTP source, such as a checksum file,
// with open, up to maxChecksumFile bytes.
func readHTTPFile(open func(*url.URL) (io.ReadCloser, error), u *url.URL) ([]byte, error) {
	body, err := open(u)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxChecksumFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxChecksumFile {
		return nil, errors.New("the file is too large")
	}
	return data, nil
}
