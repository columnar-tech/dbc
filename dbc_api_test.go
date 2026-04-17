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

package dbc_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/go-faster/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testServer *httptest.Server

type testTransport struct{}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	srvURL, _ := url.Parse(testServer.URL)
	newURL := *req.URL
	newURL.Scheme = srvURL.Scheme
	newURL.Host = srvURL.Host
	newReq := req.Clone(req.Context())
	newReq.URL = &newURL
	newReq.Host = req.URL.Host
	return http.DefaultTransport.RoundTrip(newReq)
}

func TestMain(m *testing.M) {
	indexData, err := os.ReadFile(filepath.Join("cmd", "dbc", "testdata", "test_index.yaml"))
	if err != nil {
		panic("cannot read test_index.yaml: " + err.Error())
	}

	tarballData, err := os.ReadFile(filepath.Join("cmd", "dbc", "testdata", "test-driver-1.tar.gz"))
	if err != nil {
		panic("cannot read test-driver-1.tar.gz: " + err.Error())
	}

	testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			w.Header().Set("Content-Type", "application/yaml")
			w.Write(indexData)
		case "/test-driver-1.tar.gz":
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Length", fmt.Sprint(len(tarballData)))
			w.Write(tarballData)
		default:
			http.NotFound(w, r)
		}
	}))

	origClient := dbc.DefaultClient
	dbc.DefaultClient = &http.Client{Transport: &testTransport{}}

	code := m.Run()

	testServer.Close()
	dbc.DefaultClient = origClient
	os.Exit(code)
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func loadTestDrivers(t *testing.T, registry *dbc.Registry) []dbc.Driver {
	t.Helper()

	f, err := os.Open(filepath.Join("cmd", "dbc", "testdata", "test_index.yaml"))
	require.NoError(t, err)
	defer f.Close()

	var index struct {
		Drivers []dbc.Driver `yaml:"drivers"`
	}
	require.NoError(t, yaml.NewDecoder(f).Decode(&index))
	for i := range index.Drivers {
		index.Drivers[i].Registry = registry
	}
	return index.Drivers
}

func findDriver(t *testing.T, drivers []dbc.Driver, path string) dbc.Driver {
	t.Helper()
	for _, d := range drivers {
		if d.Path == path {
			return d
		}
	}
	t.Fatalf("driver %q not found in list", path)
	return dbc.Driver{}
}

func extractTarFiles(t *testing.T, tarPath string, names ...string) map[string][]byte {
	t.Helper()

	f, err := os.Open(tarPath)
	require.NoError(t, err)
	defer f.Close()

	gr, err := gzip.NewReader(f)
	require.NoError(t, err)
	defer gr.Close()

	result := make(map[string][]byte)
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		for _, name := range names {
			if hdr.Name == name {
				data, err := io.ReadAll(tr)
				require.NoError(t, err)
				result[name] = data
			}
		}
	}
	return result
}

func TestGetDriverList(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		drivers, _ := dbc.GetDriverList()
		require.NotEmpty(t, drivers, "GetDriverList must return at least one driver")

		var paths []string
		for _, d := range drivers {
			paths = append(paths, d.Path)
		}
		assert.Contains(t, paths, "test-driver-1")
		assert.Contains(t, paths, "test-driver-2")
		assert.Contains(t, paths, "test-driver-only-pre")

		driver1 := findDriver(t, drivers, "test-driver-1")
		assert.Equal(t, "Test Driver 1", driver1.Title)
		assert.Equal(t, "MIT", driver1.License)
		assert.NotNil(t, driver1.Registry)
		assert.NotEmpty(t, driver1.Registry.BaseURL)
	})
}

func TestDriverHasNonPrerelease(t *testing.T) {
	registry := &dbc.Registry{BaseURL: mustParseURL("https://registry.example.com")}
	drivers := loadTestDrivers(t, registry)

	t.Run("has_stable_releases", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-1")
		assert.True(t, d.HasNonPrerelease())
	})

	t.Run("only_prerelease", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-only-pre")
		assert.False(t, d.HasNonPrerelease())
	})

	t.Run("empty_pkginfo", func(t *testing.T) {
		d := dbc.Driver{}
		assert.False(t, d.HasNonPrerelease())
	})
}

func TestDriverVersions(t *testing.T) {
	registry := &dbc.Registry{BaseURL: mustParseURL("https://registry.example.com")}
	drivers := loadTestDrivers(t, registry)

	t.Run("linux_amd64", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-1")
		versions := d.Versions("linux_amd64")
		require.Len(t, versions, 2)
		assert.Equal(t, "1.0.0", versions[0].String())
		assert.Equal(t, "1.1.0", versions[1].String())
	})

	t.Run("unknown_platform", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-1")
		versions := d.Versions("unknown_platform")
		assert.Empty(t, versions)
	})

	t.Run("prerelease_included", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-2")
		versions := d.Versions("linux_amd64")
		require.Len(t, versions, 3)
		assert.Equal(t, "2.0.0", versions[0].String())
		assert.Equal(t, "2.1.0-beta.1", versions[1].String())
		assert.Equal(t, "2.1.0", versions[2].String())
	})
}

func TestDriverMaxVersion(t *testing.T) {
	registry := &dbc.Registry{BaseURL: mustParseURL("https://registry.example.com")}
	drivers := loadTestDrivers(t, registry)

	t.Run("returns_highest_version", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-1")
		max, ok := d.MaxVersion()
		require.True(t, ok)
		assert.Equal(t, "1.1.0", max.Version.String())
	})

	t.Run("prerelease_can_be_max", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-only-pre")
		max, ok := d.MaxVersion()
		require.True(t, ok)
		assert.Equal(t, "0.9.0-alpha.1", max.Version.String())
	})
}

func TestDriverGetPackage(t *testing.T) {
	registry := &dbc.Registry{BaseURL: mustParseURL("https://registry.example.com")}
	drivers := loadTestDrivers(t, registry)

	t.Run("latest_version_linux_amd64", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-1")
		pkg, err := d.GetPackage(nil, "linux_amd64", false)
		require.NoError(t, err)
		assert.Equal(t, "1.1.0", pkg.Version.String())
		assert.Equal(t, "linux_amd64", pkg.PlatformTuple)
		assert.NotNil(t, pkg.Path)
	})

	t.Run("specific_version", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-1")
		v := semver.MustParse("1.0.0")
		pkg, err := d.GetPackage(v, "linux_amd64", false)
		require.NoError(t, err)
		assert.Equal(t, "1.0.0", pkg.Version.String())
	})

	t.Run("version_not_found", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-1")
		v := semver.MustParse("9.9.9")
		_, err := d.GetPackage(v, "linux_amd64", false)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "version 9.9.9 not found")
	})

	t.Run("prerelease_filtered_out", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-2")
		pkg, err := d.GetPackage(nil, "linux_amd64", false)
		require.NoError(t, err)
		assert.Equal(t, "2.1.0", pkg.Version.String())
		assert.Equal(t, "", pkg.Version.Prerelease())
	})

	t.Run("allow_prerelease", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-2")
		pkg, err := d.GetPackage(nil, "linux_amd64", true)
		require.NoError(t, err)
		assert.Equal(t, "2.1.0", pkg.Version.String())
	})

	t.Run("only_prerelease_not_allowed", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-only-pre")
		_, err := d.GetPackage(nil, "linux_amd64", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "prerelease versions filtered out")
	})

	t.Run("no_packages_for_platform", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-1")
		_, err := d.GetPackage(nil, "nonexistent_platform", false)
		assert.Error(t, err)
	})

	t.Run("empty_driver", func(t *testing.T) {
		d := dbc.Driver{Path: "empty"}
		_, err := d.GetPackage(nil, "linux_amd64", false)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "driver `empty` not found")
	})
}

func TestDriverGetWithConstraint(t *testing.T) {
	registry := &dbc.Registry{BaseURL: mustParseURL("https://registry.example.com")}
	drivers := loadTestDrivers(t, registry)

	t.Run("matches_constraint", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-1")
		c, err := semver.NewConstraint(">=1.0.0 <1.1.0")
		require.NoError(t, err)

		pkg, err := d.GetWithConstraint(c, "linux_amd64")
		require.NoError(t, err)
		assert.Equal(t, "1.0.0", pkg.Version.String())
	})

	t.Run("returns_highest_matching", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-1")
		c, err := semver.NewConstraint(">=1.0.0")
		require.NoError(t, err)

		pkg, err := d.GetWithConstraint(c, "linux_amd64")
		require.NoError(t, err)
		assert.Equal(t, "1.1.0", pkg.Version.String())
	})

	t.Run("no_matching_version", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-1")
		c, err := semver.NewConstraint(">=99.0.0")
		require.NoError(t, err)

		_, err = d.GetWithConstraint(c, "linux_amd64")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no package found for driver")
	})

	t.Run("no_matching_platform", func(t *testing.T) {
		d := findDriver(t, drivers, "test-driver-1")
		c, err := semver.NewConstraint(">=1.0.0")
		require.NoError(t, err)

		_, err = d.GetWithConstraint(c, "nonexistent_platform")
		assert.Error(t, err)
	})

	t.Run("empty_pkginfo", func(t *testing.T) {
		d := dbc.Driver{Path: "empty"}
		c, err := semver.NewConstraint(">=1.0.0")
		require.NoError(t, err)

		_, err = d.GetWithConstraint(c, "linux_amd64")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no package info available")
	})
}

func TestPkgInfoDownloadPackage(t *testing.T) {
	t.Run("no_url", func(t *testing.T) {
		pkg := dbc.PkgInfo{
			Driver:  dbc.Driver{Title: "test-driver"},
			Version: semver.MustParse("1.0.0"),
		}
		_, err := pkg.DownloadPackage(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no url set")
	})

	t.Run("success", func(t *testing.T) {
		u := mustParseURL(testServer.URL + "/test-driver-1.tar.gz")
		pkg := dbc.PkgInfo{
			Driver:        dbc.Driver{Title: "test-driver-1"},
			Version:       semver.MustParse("1.0.0"),
			PlatformTuple: "linux_amd64",
			Path:          u,
		}

		f, err := pkg.DownloadPackage(nil)
		require.NoError(t, err)
		require.NotNil(t, f)
		defer os.Remove(f.Name())
		defer f.Close()

		fi, err := f.Stat()
		require.NoError(t, err)
		assert.Greater(t, fi.Size(), int64(0))
	})

	t.Run("success_with_progress_callback", func(t *testing.T) {
		u := mustParseURL(testServer.URL + "/test-driver-1.tar.gz")
		pkg := dbc.PkgInfo{
			Driver:        dbc.Driver{Title: "test-driver-1"},
			Version:       semver.MustParse("1.0.0"),
			PlatformTuple: "linux_amd64",
			Path:          u,
		}

		var lastWritten, lastTotal int64
		f, err := pkg.DownloadPackage(func(written, total int64) {
			lastWritten = written
			lastTotal = total
		})
		require.NoError(t, err)
		require.NotNil(t, f)
		defer os.Remove(f.Name())
		defer f.Close()

		assert.Greater(t, lastWritten, int64(0))
		_ = lastTotal
	})

	t.Run("server_error", func(t *testing.T) {
		u := mustParseURL(testServer.URL + "/does-not-exist.tar.gz")
		pkg := dbc.PkgInfo{
			Driver:        dbc.Driver{Title: "test-driver-1"},
			Version:       semver.MustParse("1.0.0"),
			PlatformTuple: "linux_amd64",
			Path:          u,
		}

		_, err := pkg.DownloadPackage(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to download driver")
	})
}

func TestSignedByColumnar(t *testing.T) {
	t.Run("valid_signature", func(t *testing.T) {
		tarPath := filepath.Join("cmd", "dbc", "testdata", "test-driver-1.tar.gz")
		files := extractTarFiles(t, tarPath,
			"test-driver-1-not-valid.so",
			"test-driver-1-not-valid.so.sig",
		)
		require.Contains(t, files, "test-driver-1-not-valid.so")
		require.Contains(t, files, "test-driver-1-not-valid.so.sig")

		lib := bytes.NewReader(files["test-driver-1-not-valid.so"])
		sig := bytes.NewReader(files["test-driver-1-not-valid.so.sig"])

		err := dbc.SignedByColumnar(lib, sig)
		assert.NoError(t, err)
	})

	t.Run("invalid_signature", func(t *testing.T) {
		lib := strings.NewReader("this is not a real driver binary")
		sig := strings.NewReader("this is not a real pgp signature")

		err := dbc.SignedByColumnar(lib, sig)
		assert.Error(t, err)
	})

	t.Run("empty_inputs", func(t *testing.T) {
		lib := strings.NewReader("")
		sig := strings.NewReader("")

		err := dbc.SignedByColumnar(lib, sig)
		assert.Error(t, err)
	})
}
