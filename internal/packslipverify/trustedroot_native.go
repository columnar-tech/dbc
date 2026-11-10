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

//go:build !js

package packslipverify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/columnar-tech/dbc/internal"
	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/util"
	"github.com/theupdateframework/go-tuf/v2/metadata/fetcher"
)

// cacheLockTimeout bounds the wait for the TUF cache lock, in addition to ctx.
const cacheLockTimeout = 30 * time.Second

// rootLoader loads a trusted root through TUF.
type rootLoader func(*tuf.Options) (*root.TrustedRoot, error)

// configureTUF resolves the HTTP client and cache directory for public-good
// TUF access.
func (v *nativeVerifier) configureTUF(cfg Config) error {
	v.httpClient = cfg.HTTPClient
	if v.httpClient == nil {
		v.httpClient = defaultHTTPClient()
	}
	v.cacheDir = cfg.CacheDir
	if v.cacheDir == "" {
		dir, err := internal.GetUserConfigPath()
		if err != nil {
			return fmt.Errorf("find dbc config directory for Sigstore TUF cache: %w", err)
		}
		v.cacheDir = filepath.Join(dir, "cache", "sigstore-tuf")
	}
	if !filepath.IsAbs(v.cacheDir) {
		return fmt.Errorf("Sigstore TUF cache directory must be absolute: %q", v.cacheDir)
	}
	return nil
}

func defaultHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: transport, Timeout: 2 * time.Minute}
}

// trustedRoot returns the static root, or loads the root through TUF. Nothing
// from a successful TUF load is kept in memory: TUF's own cache and expiry
// logic decides on every call whether to refetch, so an earlier success cannot
// hide a later expiry or update failure.
func (v *nativeVerifier) trustedRoot(ctx context.Context) (*root.TrustedRoot, error) {
	if v.staticRoot != nil {
		return v.staticRoot, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(v.cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("create Sigstore TUF cache directory: %w", err)
	}

	// Build fresh options per call; the caller's HTTP client is only wrapped.
	f := fetcher.NewDefaultFetcher()
	f.SetHTTPUserAgent(util.ConstructUserAgent())
	f.SetHTTPClient(ctxHTTPClient{ctx: ctx, client: v.httpClient})
	opts := tuf.DefaultOptions()
	opts.CachePath = v.cacheDir
	opts.Fetcher = f
	opts.Context = ctx

	// The lock covers TUF client creation, the metadata refresh and reading the
	// trusted_root target, since all of them touch the cache directory.
	lock, err := fslock.AcquireContext(ctx, filepath.Join(v.cacheDir, "tuf.lock"), cacheLockTimeout)
	if err != nil {
		return nil, fmt.Errorf("acquire Sigstore TUF cache lock: %w", err)
	}
	tr, loadErr := v.loadRoot(opts)
	if releaseErr := lock.Release(); releaseErr != nil {
		loadErr = errors.Join(loadErr, fmt.Errorf("release Sigstore TUF cache lock: %w", releaseErr))
	}
	if loadErr != nil {
		return nil, loadErr
	}
	// A cancelled caller must not succeed because the cache was warm.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tr == nil {
		return nil, errors.New("Sigstore trusted root loader returned no root")
	}
	return tr, nil
}

// ctxHTTPClient attaches the caller's context to every request go-tuf makes
// without modifying the wrapped client or its transport.
type ctxHTTPClient struct {
	ctx    context.Context
	client *http.Client
}

func (c ctxHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return c.client.Do(req.Clone(c.ctx))
}
