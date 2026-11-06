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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/go-faster/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validRegistryDigest = "3320bb08fa1654bfbd41a8fde4043785e9859efda3fb0d7bd0469788984a7da2"

func decodeHashTestDrivers(t *testing.T, hashLines string) []dbc.Driver {
	t.Helper()
	index := `drivers:
  - name: Hashed
    description: driver with hashes
    license: MIT
    path: hashed
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            url: hashed/v1.0.0/a.tar.gz
` + hashLines + `
          - platform: macos_arm64
            url: hashed/v1.0.0/b.tar.gz
      - version: v0.9.0
        packages:
          - platform: linux_amd64
            url: hashed/v0.9.0/a.tar.gz
  - name: Other
    description: unrelated driver
    license: MIT
    path: other
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            url: other/v1.0.0/a.tar.gz
            hash: sha256:` + validRegistryDigest + `
`
	var decoded struct {
		Drivers []dbc.Driver `yaml:"drivers"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(index), &decoded), "a bad hash must not fail index decoding")
	reg := &dbc.Registry{Name: "test", BaseURL: mustParseURL("https://registry.example.com")}
	for i := range decoded.Drivers {
		decoded.Drivers[i].Registry = reg
	}
	reg.Drivers = decoded.Drivers
	return decoded.Drivers
}

func TestRegistryPackageHash(t *testing.T) {
	v1 := semver.MustParse("v1.0.0")

	t.Run("valid hash is exposed without prefix", func(t *testing.T) {
		drivers := decodeHashTestDrivers(t, "            hash: sha256:"+validRegistryDigest)
		pkg, err := drivers[0].GetPackage(v1, "linux_amd64", false)
		require.NoError(t, err)
		assert.Equal(t, validRegistryDigest, pkg.ArchiveSHA256)

		pkg, err = drivers[0].GetWithConstraint(mustConstraint(t, "1.0.0"), "linux_amd64")
		require.NoError(t, err)
		assert.Equal(t, validRegistryDigest, pkg.ArchiveSHA256)
	})

	t.Run("missing hash is empty", func(t *testing.T) {
		drivers := decodeHashTestDrivers(t, "")
		pkg, err := drivers[0].GetPackage(v1, "linux_amd64", false)
		require.NoError(t, err)
		assert.Empty(t, pkg.ArchiveSHA256)
	})

	bad := map[string]string{
		"wrong algorithm": "            hash: sha512:" + validRegistryDigest,
		"no prefix":       "            hash: " + validRegistryDigest,
		"uppercase":       "            hash: sha256:" + strings.ToUpper(validRegistryDigest),
		"too short":       "            hash: sha256:" + validRegistryDigest[:63],
		"too long":        "            hash: sha256:" + validRegistryDigest + "0",
		"not hex":         "            hash: sha256:" + strings.Repeat("g", 64),
		"yaml map":        "            hash:\n              sha256: " + validRegistryDigest,
		"yaml list":       "            hash:\n              - sha256:" + validRegistryDigest,
		"yaml number":     "            hash: 12345",
		"yaml null":       "            hash: null",
		"yaml tilde null": "            hash: ~",
		"yaml empty":      "            hash:",
		"empty string":    `            hash: ""`,
	}
	for name, line := range bad {
		t.Run("malformed hash only breaks that package: "+name, func(t *testing.T) {
			drivers := decodeHashTestDrivers(t, line)

			_, err := drivers[0].GetPackage(v1, "linux_amd64", false)
			require.Error(t, err)
			for _, want := range []string{"hashed", "1.0.0", "linux_amd64", "hash"} {
				assert.Contains(t, err.Error(), want)
			}
			assert.NotContains(t, err.Error(), "a.tar.gz")

			_, err = drivers[0].GetWithConstraint(mustConstraint(t, "1.0.0"), "linux_amd64")
			require.Error(t, err)

			// Other platform, other version and other driver keep working.
			_, err = drivers[0].GetPackage(v1, "macos_arm64", false)
			require.NoError(t, err)
			_, err = drivers[0].GetPackage(semver.MustParse("v0.9.0"), "linux_amd64", false)
			require.NoError(t, err)
			other, err := drivers[1].GetPackage(nil, "linux_amd64", false)
			require.NoError(t, err)
			assert.Equal(t, validRegistryDigest, other.ArchiveSHA256)
			assert.Len(t, drivers[0].Versions("linux_amd64"), 2)
		})
	}
}

func mustConstraint(t *testing.T, s string) *semver.Constraints {
	t.Helper()
	c, err := semver.NewConstraint(s)
	require.NoError(t, err)
	return c
}

func TestClientSearchSurvivesMalformedHash(t *testing.T) {
	index := fmt.Sprintf(`drivers:
  - name: Bad Hash
    description: d
    license: MIT
    path: bad-hash
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: %s
            url: bad-hash/a.tar.gz
            hash: nonsense
`, config.PlatformTuple())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.yaml" {
			_, _ = w.Write([]byte(index))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	c := newTestClientForServer(t, srv.URL)

	drivers, err := c.Search(t.Context(), "")
	require.NoError(t, err)
	require.Len(t, drivers, 1)

	cfg := config.Config{Level: config.ConfigEnv, Location: t.TempDir()}
	_, err = c.Install(t.Context(), cfg, "bad-hash")
	require.ErrorContains(t, err, "invalid archive hash")
}

func TestClientInstallChecksRegistryArchiveHash(t *testing.T) {
	tarball, err := os.ReadFile(filepath.Join("cmd", "dbc", "testdata", "test-driver-tampered.tar.gz"))
	require.NoError(t, err)
	sum := sha256.Sum256(tarball)
	actual := hex.EncodeToString(sum[:])

	newClient := func(t *testing.T, hash string) *dbc.Client {
		index := fmt.Sprintf(`drivers:
  - name: Hashed Driver
    description: d
    license: MIT
    path: test-driver-hashed
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: %s
            url: test-driver-hashed/1.0.0/hashed.tar.gz
            hash: sha256:%s
`, config.PlatformTuple(), hash)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/index.yaml":
				_, _ = w.Write([]byte(index))
			case strings.HasSuffix(r.URL.Path, ".tar.gz"):
				_, _ = w.Write(tarball)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(srv.Close)
		return newTestClientForServer(t, srv.URL)
	}

	t.Run("mismatch fails even with AllowUnsigned", func(t *testing.T) {
		c := newClient(t, strings.Repeat("0", 64))
		cfg := config.Config{Level: config.ConfigEnv, Location: t.TempDir()}
		_, err := c.Install(t.Context(), cfg, "test-driver-hashed", dbc.AllowUnsigned())
		var mismatch *config.ArchiveHashMismatchError
		require.ErrorAs(t, err, &mismatch)
		assert.Equal(t, actual, mismatch.Actual)
		_, err = config.GetDriver(cfg, "test-driver-hashed")
		assert.Error(t, err)
	})

	t.Run("matching hash with AllowUnsigned installs", func(t *testing.T) {
		c := newClient(t, actual)
		cfg := config.Config{Level: config.ConfigEnv, Location: t.TempDir()}
		m, err := c.Install(t.Context(), cfg, "test-driver-hashed", dbc.AllowUnsigned())
		require.NoError(t, err)
		assert.FileExists(t, m.Driver.Shared.Get(config.PlatformTuple()))
	})
}

// readReceiptEvidence reads the persisted install receipt that sits next to
// the registered shared library of the installed driver.
func readReceiptEvidence(t *testing.T, cfg config.Config, driver string) (archiveSHA256 string, signatureVerified bool) {
	t.Helper()
	info, err := config.GetDriver(cfg, driver)
	require.NoError(t, err)
	shared := info.Driver.Shared.Get(config.PlatformTuple())
	data, err := os.ReadFile(filepath.Join(filepath.Dir(shared), "dbc-install-receipt.json"))
	require.NoError(t, err)
	var r struct {
		ArchiveSHA256     string `json:"archive_sha256"`
		SignatureVerified *bool  `json:"signature_verified"`
	}
	require.NoError(t, json.Unmarshal(data, &r))
	require.NotNil(t, r.SignatureVerified, "receipt must record signature_verified")
	return r.ArchiveSHA256, *r.SignatureVerified
}

func TestClientInstallReceiptEvidence(t *testing.T) {
	srv := newInstallTestServer(t)
	c := newTestClientForServer(t, srv.URL)
	tarball, err := os.ReadFile(filepath.Join("cmd", "dbc", "testdata", "test-driver-1.tar.gz"))
	require.NoError(t, err)
	sum := sha256.Sum256(tarball)
	digest := hex.EncodeToString(sum[:])

	t.Run("signature verified", func(t *testing.T) {
		cfg := config.Config{Level: config.ConfigEnv, Location: t.TempDir()}
		_, err := c.Install(t.Context(), cfg, "test-driver-1")
		require.NoError(t, err)
		got, verified := readReceiptEvidence(t, cfg, "test-driver-1")
		assert.Equal(t, digest, got)
		assert.True(t, verified)
	})

	t.Run("AllowUnsigned", func(t *testing.T) {
		cfg := config.Config{Level: config.ConfigEnv, Location: t.TempDir()}
		_, err := c.Install(t.Context(), cfg, "test-driver-1", dbc.AllowUnsigned())
		require.NoError(t, err)
		got, verified := readReceiptEvidence(t, cfg, "test-driver-1")
		assert.Equal(t, digest, got)
		assert.False(t, verified)
	})
}
