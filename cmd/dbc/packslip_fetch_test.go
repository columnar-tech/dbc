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
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackslipArtifactFetchPreservesSignedURLWithoutCredentials(t *testing.T) {
	const rawQuery = "X-Amz-Credential=release%2Ftoken&X-Amz-Signature=abc%2B123&response-content-type=application%2Fgzip"
	var requestQuery string
	var authorization string
	var midPresent, uidPresent bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestQuery = r.URL.RawQuery
		authorization = r.Header.Get("Authorization")
		query := r.URL.Query()
		_, midPresent = query["mid"]
		_, uidPresent = query["uid"]
		_, _ = io.WriteString(w, "signed package bytes")
	}))
	defer server.Close()
	artifactURL, err := url.Parse(server.URL + "/driver.tgz?" + rawQuery)
	require.NoError(t, err)
	target, err := resolution.TargetFromPlatformTuple(config.PlatformTuple())
	require.NoError(t, err)
	item, err := newInstallItem(resolution.ResolvedRelease{
		DriverID: "example", Version: "1.2.3",
		Source: resolution.SourceSpec{Type: "packslip", Reference: "github.com/example/driver"},
		Artifacts: []resolution.Artifact{{
			Target: target, Format: "tgz", PackageVersion: 2,
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: artifactURL.String()},
		}},
	}, 0, config.PlatformTuple(), nil)
	require.NoError(t, err)
	registryCalls := 0
	executor := newPackageExecutor(config.Config{}, t.TempDir(), true,
		func(context.Context, dbc.PkgInfo) (io.ReadCloser, error) {
			registryCalls++
			return nil, errors.New("Packslip fetch must not use the registry transport")
		}, nil, nil, nil)
	executor.fetchPackslip = func(ctx context.Context, url *url.URL) (io.ReadCloser, error) {
		return fetchPackslipArtifactWithClient(ctx, server.Client(), url)
	}
	opened, err := executor.openResolvedArtifact(context.Background(), item)
	require.NoError(t, err)
	defer opened.Close()
	contents, err := io.ReadAll(opened.File)
	require.NoError(t, err)
	assert.Equal(t, "signed package bytes", string(contents))
	assert.Equal(t, rawQuery, requestQuery, "signed query must be sent exactly as resolved")
	assert.Empty(t, authorization)
	assert.False(t, midPresent)
	assert.False(t, uidPresent)
	assert.Zero(t, registryCalls)
}

func TestPackageExecutorRoutesRegistryURLThroughExistingDownloader(t *testing.T) {
	target, err := resolution.TargetFromPlatformTuple(config.PlatformTuple())
	require.NoError(t, err)
	item, err := newInstallItem(resolution.ResolvedRelease{
		DriverID: "example", Version: "1.2.3",
		Source: resolution.SourceSpec{Type: "registry", Reference: "https://registry.example.test"},
		Artifacts: []resolution.Artifact{{
			Target: target, Format: "tar.gz",
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://assets.example.test/driver.tar.gz?sig=unchanged"},
		}},
	}, 0, config.PlatformTuple(), nil)
	require.NoError(t, err)
	registryCalls, packslipCalls := 0, 0
	executor := newPackageExecutor(config.Config{}, t.TempDir(), true,
		func(_ context.Context, pkg dbc.PkgInfo) (io.ReadCloser, error) {
			registryCalls++
			assert.Equal(t, "sig=unchanged", pkg.Path.RawQuery)
			return io.NopCloser(strings.NewReader("registry package bytes")), nil
		}, nil, nil, nil)
	executor.fetchPackslip = func(context.Context, *url.URL) (io.ReadCloser, error) {
		packslipCalls++
		return nil, errors.New("registry fetch must not use Packslip transport")
	}
	opened, err := executor.openResolvedArtifact(context.Background(), item)
	require.NoError(t, err)
	defer opened.Close()
	contents, err := io.ReadAll(opened.File)
	require.NoError(t, err)
	assert.Equal(t, "registry package bytes", string(contents))
	assert.Equal(t, 1, registryCalls)
	assert.Zero(t, packslipCalls)
}

func TestPackageExecutorFailsClosedForUnknownURLSource(t *testing.T) {
	target, err := resolution.TargetFromPlatformTuple(config.PlatformTuple())
	require.NoError(t, err)
	item, err := newInstallItem(resolution.ResolvedRelease{
		DriverID: "example", Version: "1.2.3",
		Source: resolution.SourceSpec{Type: "unknown", Reference: "opaque"},
		Artifacts: []resolution.Artifact{{
			Target: target, Format: "tgz",
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://assets.example.test/driver.tgz"},
		}},
	}, 0, config.PlatformTuple(), nil)
	require.NoError(t, err)
	registryCalls, packslipCalls := 0, 0
	executor := newPackageExecutor(config.Config{}, t.TempDir(), true,
		func(context.Context, dbc.PkgInfo) (io.ReadCloser, error) {
			registryCalls++
			return nil, nil
		}, nil, nil, nil)
	executor.fetchPackslip = func(context.Context, *url.URL) (io.ReadCloser, error) {
		packslipCalls++
		return nil, nil
	}
	_, err = executor.openResolvedArtifact(context.Background(), item)
	require.ErrorContains(t, err, `source "unknown" cannot open a URL artifact`)
	assert.Zero(t, registryCalls)
	assert.Zero(t, packslipCalls)
}

func TestPackslipArtifactFetchBoundsErrorBody(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "prefix"+strings.Repeat("x", packslipErrorBodyLimit)+"body-tail-sentinel")
	}))
	defer server.Close()
	artifactURL, err := url.Parse(server.URL + "/driver.tgz")
	require.NoError(t, err)
	_, err = fetchPackslipArtifactWithClient(context.Background(), server.Client(), artifactURL)
	require.Error(t, err)
	assert.ErrorContains(t, err, "502 Bad Gateway")
	assert.NotContains(t, err.Error(), "body-tail-sentinel")
	assert.Less(t, len(err.Error()), packslipErrorBodyLimit+256)
}

func TestPackslipArtifactFetchRejectsHTTPSDowngradeRedirect(t *testing.T) {
	var downgradeHits int
	httpTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		downgradeHits++
	}))
	defer httpTarget.Close()
	tlsSource := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, httpTarget.URL+"/archive", http.StatusFound)
	}))
	defer tlsSource.Close()
	artifactURL, err := url.Parse(tlsSource.URL + "/signed")
	require.NoError(t, err)
	_, err = fetchPackslipArtifactWithClient(context.Background(), tlsSource.Client(), artifactURL)
	require.Error(t, err)
	assert.ErrorContains(t, err, "reject Packslip artifact redirect")
	assert.ErrorContains(t, err, "must be HTTPS")
	assert.Zero(t, downgradeHits)
}
