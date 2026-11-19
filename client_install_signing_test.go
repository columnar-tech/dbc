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
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signedTestArchive returns a driver package whose library is signed with
// key, and the armored public key.
func signedTestArchive(t *testing.T, name string) (archive []byte, publicKey string) {
	t.Helper()
	key, err := crypto.PGP().KeyGeneration().AddUserId(name, name+"@example.invalid").New().GenerateKey()
	require.NoError(t, err)
	pub, err := key.ToPublic()
	require.NoError(t, err)
	publicKey, err = pub.Armor()
	require.NoError(t, err)

	lib := []byte("not a real driver library: " + name)
	signer, err := crypto.PGP().Sign().SigningKey(key).Detached().New()
	require.NoError(t, err)
	sig, err := signer.Sign(lib, crypto.Armor)
	require.NoError(t, err)

	manifest := []byte(`name = "Signed Driver"
version = "1.0.0"

[Files]
driver = "signed.so"
signature = "signed.so.sig"
`)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		body []byte
	}{{"MANIFEST", manifest}, {"signed.so", lib}, {"signed.so.sig", sig}} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write(f.body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes(), publicKey
}

// signedDriverServer serves a registry with the driver "signed-driver". When
// withPackage is false, the driver is listed without any version, so a client
// moves on to its next registry.
func signedDriverServer(t *testing.T, archive []byte, withPackage bool, downloads *atomic.Int32) *url.URL {
	t.Helper()
	pkginfo := "    pkginfo: []\n"
	if withPackage {
		pkginfo = fmt.Sprintf(`    pkginfo:
      - version: v1.0.0
        packages:
          - platform: %s
            url: signed-driver/1.0.0/signed.tar.gz
`, config.PlatformTuple())
	}
	index := "drivers:\n  - name: Signed Driver\n    description: Signed driver\n    license: MIT\n    path: signed-driver\n" + pkginfo
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/index.yaml":
			w.Write([]byte(index))
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			downloads.Add(1)
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return u
}

func TestClientInstallUsesTheKeyOfTheRegistry(t *testing.T) {
	archive, publicKey := signedTestArchive(t, "acme")
	acme, err := dbc.NewSigningKey([]byte(publicKey))
	require.NoError(t, err)

	install := func(t *testing.T, registries []dbc.Registry, opts ...dbc.InstallOption) (*config.Manifest, error) {
		t.Helper()
		c, err := dbc.NewClient(dbc.WithHTTPClient(&http.Client{}), dbc.WithRegistries(registries))
		require.NoError(t, err)
		cfg := config.Config{Level: config.ConfigEnv, Location: t.TempDir()}
		return c.Install(t.Context(), cfg, "signed-driver", opts...)
	}

	t.Run("the key of the registry verifies the package", func(t *testing.T) {
		var downloads atomic.Int32
		u := signedDriverServer(t, archive, true, &downloads)
		m, err := install(t, []dbc.Registry{{BaseURL: u, Signing: dbc.SignWith(acme)}})
		require.NoError(t, err)
		assert.FileExists(t, m.Driver.Shared.Get(config.PlatformTuple()))
	})

	t.Run("the Columnar key rejects it", func(t *testing.T) {
		var downloads atomic.Int32
		u := signedDriverServer(t, archive, true, &downloads)
		_, err := install(t, []dbc.Registry{{BaseURL: u}})
		require.ErrorContains(t, err, "signature verification failed")
	})

	t.Run("another key rejects it", func(t *testing.T) {
		var downloads atomic.Int32
		u := signedDriverServer(t, archive, true, &downloads)
		_, otherKey := signedTestArchive(t, "other")
		other, err := dbc.NewSigningKey([]byte(otherKey))
		require.NoError(t, err)
		_, err = install(t, []dbc.Registry{{BaseURL: u, Signing: dbc.SignWith(other)}})
		require.ErrorContains(t, err, "signature verification failed")
	})

	t.Run("an unsigned registry is not verified", func(t *testing.T) {
		var downloads atomic.Int32
		u := signedDriverServer(t, archive, true, &downloads)
		_, err := install(t, []dbc.Registry{{BaseURL: u, Signing: dbc.Unsigned()}})
		require.NoError(t, err)
	})

	// The first registry lists the driver without a version, so the package
	// comes from the second one, and its key must be used, not the key of
	// the first.
	t.Run("a fallback registry uses its own key", func(t *testing.T) {
		var firstDownloads, secondDownloads atomic.Int32
		first := signedDriverServer(t, archive, false, &firstDownloads)
		second := signedDriverServer(t, archive, true, &secondDownloads)
		_, err := install(t, []dbc.Registry{{BaseURL: first}, {BaseURL: second, Signing: dbc.SignWith(acme)}})
		require.NoError(t, err)
		assert.Equal(t, int32(1), secondDownloads.Load())

		_, err = install(t, []dbc.Registry{{BaseURL: first, Signing: dbc.SignWith(acme)}, {BaseURL: second}})
		require.ErrorContains(t, err, "signature verification failed")
	})

}
