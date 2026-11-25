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

package dbc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/columnar-tech/dbc/auth"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAnonymous(t *testing.T) {
	var got *http.Request
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "content")
	}))
	t.Cleanup(plain.Close)
	open := func(t *testing.T, c *Client, raw string) (string, error) {
		t.Helper()
		u, err := url.Parse(raw)
		require.NoError(t, err)
		body, err := c.OpenAnonymous(context.Background(), u)
		if err != nil {
			return "", err
		}
		defer body.Close()
		data, err := io.ReadAll(body)
		return string(data), err
	}

	t.Run("no credentials or identifiers", func(t *testing.T) {
		c, err := NewClient(WithBaseURL(plain.URL), WithCredentialResolver(func(*url.URL) (*auth.Credential, error) {
			t.Fatal("no credentials are looked up")
			return nil, nil
		}))
		require.NoError(t, err)
		data, err := open(t, c, plain.URL+"/a.tar.gz")
		require.NoError(t, err)
		assert.Equal(t, "content", data)
		assert.Empty(t, got.URL.RawQuery, "no mid or uid")
		assert.Empty(t, got.Header.Get("Authorization"))
	})

	t.Run("a status other than 200 is an error", func(t *testing.T) {
		c, err := NewClient()
		require.NoError(t, err)
		_, err = open(t, c, plain.URL+"/missing")
		assert.ErrorContains(t, err, "404")
	})

	t.Run("url replacements apply", func(t *testing.T) {
		c, err := NewClient(WithURLReplacements([]URLReplacement{{Prefix: "https://drivers.example.com/acme", Replacement: plain.URL + "/mirror"}}))
		require.Error(t, err, "an https prefix is never replaced by http")
		mirror := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r
			_, _ = io.WriteString(w, "mirrored")
		}))
		t.Cleanup(mirror.Close)
		c, err = NewClient(WithHTTPClient(mirror.Client()), WithURLReplacements([]URLReplacement{{Prefix: "https://drivers.example.com/acme", Replacement: mirror.URL + "/mirror"}}))
		require.NoError(t, err)
		data, err := open(t, c, "https://drivers.example.com/acme/v1/a.tar.gz")
		require.NoError(t, err)
		assert.Equal(t, "mirrored", data)
		assert.Equal(t, "/mirror/v1/a.tar.gz", got.URL.Path)
	})

	t.Run("an https to http redirect is refused", func(t *testing.T) {
		got = nil
		secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, plain.URL+"/a.tar.gz", http.StatusFound)
		}))
		t.Cleanup(secure.Close)
		hc := secure.Client()
		hc.CheckRedirect = nil // a caller's client may allow any redirect
		c, err := NewClient(WithHTTPClient(hc))
		require.NoError(t, err)
		_, err = open(t, c, secure.URL+"/a.tar.gz")
		assert.ErrorContains(t, err, "HTTPS to HTTP")
		assert.Nil(t, got, "nothing is sent over plain HTTP")
	})

	t.Run("only http and https", func(t *testing.T) {
		c, err := NewClient()
		require.NoError(t, err)
		_, err = open(t, c, "file:///etc/passwd")
		assert.Error(t, err)
	})
}
