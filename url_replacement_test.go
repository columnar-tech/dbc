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
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/columnar-tech/dbc/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func routeString(t *testing.T, rules []URLReplacement, raw string) (string, bool, error) {
	t.Helper()
	r, err := newURLRouter(rules)
	require.NoError(t, err)
	u, err := url.Parse(raw)
	require.NoError(t, err)
	eff, replaced, err := r.route(u)
	if err != nil || !replaced {
		return "", replaced, err
	}
	return eff.String(), replaced, nil
}

func TestURLRouterMatching(t *testing.T) {
	canon := URLReplacement{Prefix: "https://dbc-cdn.columnar.tech", Replacement: "https://mirror.example.com/dbc-cdn"}
	cases := []struct {
		name  string
		rules []URLReplacement
		in    string
		want  string // empty means no replacement
	}{
		{"basic", []URLReplacement{canon}, "https://dbc-cdn.columnar.tech/index.yaml", "https://mirror.example.com/dbc-cdn/index.yaml"},
		{"origin case", []URLReplacement{canon}, "HTTPS://DBC-CDN.Columnar.Tech/index.yaml", "https://mirror.example.com/dbc-cdn/index.yaml"},
		{"default port filled in", []URLReplacement{canon}, "https://dbc-cdn.columnar.tech:443/a", "https://mirror.example.com/dbc-cdn/a"},
		{"other port", []URLReplacement{canon}, "https://dbc-cdn.columnar.tech:8443/a", ""},
		{"other scheme", []URLReplacement{canon}, "http://dbc-cdn.columnar.tech/a", ""},
		{"look-alike host", []URLReplacement{canon}, "https://dbc-cdn.columnar.tech.evil.example/x", ""},
		{"look-alike userinfo host", []URLReplacement{canon}, "https://dbc-cdn.columnar.tech@evil.example/x", ""},
		{"prefix port explicit default", []URLReplacement{{Prefix: "https://a.example:443", Replacement: "https://m.example"}}, "https://a.example/x", "https://m.example/x"},
		{"http default port", []URLReplacement{{Prefix: "http://a.example:80", Replacement: "http://m.example"}}, "http://a.example/x", "http://m.example/x"},
		{"segment boundary exact", []URLReplacement{{Prefix: "https://a.example/foo", Replacement: "https://m.example/bar"}}, "https://a.example/foo", "https://m.example/bar"},
		{"segment boundary child", []URLReplacement{{Prefix: "https://a.example/foo", Replacement: "https://m.example/bar"}}, "https://a.example/foo/baz", "https://m.example/bar/baz"},
		{"segment boundary violated", []URLReplacement{{Prefix: "https://a.example/foo", Replacement: "https://m.example/bar"}}, "https://a.example/foobar", ""},
		{"trailing slash on prefix", []URLReplacement{{Prefix: "https://a.example/foo/", Replacement: "https://m.example/bar"}}, "https://a.example/foo/x", "https://m.example/bar/x"},
		{"trailing slash on replacement", []URLReplacement{{Prefix: "https://a.example/foo", Replacement: "https://m.example/bar/"}}, "https://a.example/foo/x", "https://m.example/bar/x"},
		{"trailing slash on both", []URLReplacement{{Prefix: "https://a.example/", Replacement: "https://m.example/"}}, "https://a.example/x/y", "https://m.example/x/y"},
		{"escaped path kept verbatim", []URLReplacement{{Prefix: "https://a.example/foo", Replacement: "https://m.example/bar"}}, "https://a.example/foo/a%2Fb%20c", "https://m.example/bar/a%2Fb%20c"},
		{"escaped slash is not a boundary", []URLReplacement{{Prefix: "https://a.example/foo", Replacement: "https://m.example"}}, "https://a.example/foo%2Fbar", ""},
		{"first match wins", []URLReplacement{
			{Prefix: "https://a.example/x", Replacement: "https://one.example"},
			{Prefix: "https://a.example", Replacement: "https://two.example"},
		}, "https://a.example/x/y", "https://one.example/y"},
		{"later rule when earlier does not match", []URLReplacement{
			{Prefix: "https://a.example/x", Replacement: "https://one.example"},
			{Prefix: "https://a.example", Replacement: "https://two.example"},
		}, "https://a.example/z", "https://two.example/z"},
		{"no chaining", []URLReplacement{
			{Prefix: "https://a.example", Replacement: "https://b.example"},
			{Prefix: "https://b.example", Replacement: "https://c.example"},
		}, "https://a.example/x", "https://b.example/x"},
		{"root request", []URLReplacement{canon}, "https://dbc-cdn.columnar.tech", "https://mirror.example.com/dbc-cdn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, replaced, err := routeString(t, tc.rules, tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.want != "", replaced)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestURLRouterQueryRefused(t *testing.T) {
	rules := []URLReplacement{{Prefix: "https://a.example", Replacement: "https://m.example"}}
	_, _, err := routeString(t, rules, "https://a.example/x?tenant=secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query")
	assert.NotContains(t, err.Error(), "secret")

	// A non-matching URL with a query is untouched and not an error.
	_, replaced, err := routeString(t, rules, "https://other.example/x?tenant=secret")
	require.NoError(t, err)
	assert.False(t, replaced)
}

func TestNilRouter(t *testing.T) {
	var r *urlRouter
	u, _ := url.Parse("https://a.example/x")
	_, replaced, err := r.route(u)
	require.NoError(t, err)
	assert.False(t, replaced)
}

func TestURLReplacementValidation(t *testing.T) {
	ok := URLReplacement{Prefix: "https://a.example", Replacement: "https://m.example"}
	cases := []struct {
		name    string
		rule    URLReplacement
		wantErr string
	}{
		{"relative prefix", URLReplacement{Prefix: "/foo", Replacement: "https://m.example"}, "prefix"},
		{"relative replacement", URLReplacement{Prefix: "https://a.example", Replacement: "m.example/x"}, "replacement"},
		{"non-http scheme", URLReplacement{Prefix: "ftp://a.example", Replacement: "https://m.example"}, "http or https"},
		{"file replacement", URLReplacement{Prefix: "https://a.example", Replacement: "file:///tmp/x"}, "http or https"},
		{"no host", URLReplacement{Prefix: "https:///x", Replacement: "https://m.example"}, "host"},
		{"userinfo prefix", URLReplacement{Prefix: "https://u:p@a.example", Replacement: "https://m.example"}, "userinfo"},
		{"userinfo replacement", URLReplacement{Prefix: "https://a.example", Replacement: "https://u:p@m.example"}, "userinfo"},
		{"query prefix", URLReplacement{Prefix: "https://a.example/x?a=b", Replacement: "https://m.example"}, "query"},
		{"query replacement", URLReplacement{Prefix: "https://a.example", Replacement: "https://m.example/x?a=b"}, "query"},
		{"empty query marker", URLReplacement{Prefix: "https://a.example", Replacement: "https://m.example/x?"}, "query"},
		{"fragment", URLReplacement{Prefix: "https://a.example#f", Replacement: "https://m.example"}, "fragment"},
		{"https to http", URLReplacement{Prefix: "https://a.example", Replacement: "http://m.example"}, "https prefix"},
		{"empty", URLReplacement{}, "prefix"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The invalid rule is second so the index is reported.
			_, err := newURLRouter([]URLReplacement{ok, tc.rule})
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidURLReplacement)
			assert.Contains(t, err.Error(), "url_replacements[1]")
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}

	t.Run("http to https and http to http are allowed", func(t *testing.T) {
		_, err := newURLRouter([]URLReplacement{
			{Prefix: "http://a.example", Replacement: "https://m.example"},
			{Prefix: "http://b.example", Replacement: "http://m.example"},
		})
		require.NoError(t, err)
	})

	t.Run("NewClient rejects invalid rules", func(t *testing.T) {
		_, err := NewClient(WithURLReplacements([]URLReplacement{{Prefix: "x", Replacement: "y"}}))
		require.ErrorIs(t, err, ErrInvalidURLReplacement)
		_, err = NewClient(WithBaseURL("https://a.example"),
			WithGlobalConfig(&GlobalConfig{Network: NetworkConfig{URLReplacements: []URLReplacement{{Prefix: "x", Replacement: "y"}}}}))
		require.ErrorIs(t, err, ErrInvalidURLReplacement)
	})

	t.Run("SetDefaultURLReplacements keeps previous rules on error", func(t *testing.T) {
		t.Cleanup(func() { _ = SetDefaultURLReplacements(nil) })
		require.NoError(t, SetDefaultURLReplacements([]URLReplacement{ok}))
		before := defaultRouter.Load()
		require.Error(t, SetDefaultURLReplacements([]URLReplacement{{Prefix: "x"}}))
		assert.Same(t, before, defaultRouter.Load())
	})
}

func TestLoadGlobalConfigURLReplacements(t *testing.T) {
	dir := t.TempDir()
	write := func(content string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600))
	}

	write(`
[[network.url_replacements]]
prefix = "https://dbc-cdn.columnar.tech"
replacement = "https://artifactory.example.com/dbc-cdn"

[[network.url_replacements]]
prefix = "https://other.example"
replacement = "https://artifactory.example.com/other"
`)
	cfg, err := LoadGlobalConfig(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, []URLReplacement{
		{Prefix: "https://dbc-cdn.columnar.tech", Replacement: "https://artifactory.example.com/dbc-cdn"},
		{Prefix: "https://other.example", Replacement: "https://artifactory.example.com/other"},
	}, cfg.Network.URLReplacements)

	write(`
[[network.url_replacements]]
prefix = "https://dbc-cdn.columnar.tech"
replacement = "http://artifactory.example.com"
`)
	_, err = LoadGlobalConfig(dir)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidURLReplacement)
	assert.Contains(t, err.Error(), "config.toml")
	assert.Contains(t, err.Error(), "url_replacements[0]")
}

func TestLoadProjectRegistriesRejectsNetwork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbc.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[[network.url_replacements]]
prefix = "https://dbc-cdn.columnar.tech"
replacement = "https://evil.example"
`), 0o600))
	_, _, err := LoadProjectRegistries(path)
	require.ErrorIs(t, err, ErrProjectNetwork)
	assert.Contains(t, err.Error(), "user config")
}

// mirrorFixture is an "upstream" and a "mirror" server that record the
// requests they receive.
type mirrorFixture struct {
	upstream, mirror         *httptest.Server
	upstreamHits, mirrorHits atomic.Int32

	mu         sync.Mutex
	mirrorReqs []*http.Request
}

func newMirrorFixture(t *testing.T, mirrorHandler http.HandlerFunc) *mirrorFixture {
	t.Helper()
	f := &mirrorFixture{}
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.upstreamHits.Add(1)
		io.WriteString(w, "upstream")
	}))
	t.Cleanup(f.upstream.Close)
	if mirrorHandler == nil {
		mirrorHandler = func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "mirror") }
	}
	f.mirror = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mirrorHits.Add(1)
		f.mu.Lock()
		f.mirrorReqs = append(f.mirrorReqs, r.Clone(r.Context()))
		f.mu.Unlock()
		mirrorHandler(w, r)
	}))
	t.Cleanup(f.mirror.Close)
	return f
}

func (f *mirrorFixture) rules() []URLReplacement {
	return []URLReplacement{{Prefix: f.upstream.URL, Replacement: f.mirror.URL + "/prefix"}}
}

func credFor(t *testing.T, srv *httptest.Server, refreshes *atomic.Int32) func(*url.URL) (*auth.Credential, error) {
	t.Helper()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return func(*url.URL) (*auth.Credential, error) {
		return &auth.Credential{Type: auth.TypeApiKey, ApiKey: "k", Token: "secret", RegistryURL: auth.Uri(*u)}, nil
	}
}

func readBody(t *testing.T, rsp *http.Response) string {
	t.Helper()
	defer rsp.Body.Close()
	b, err := io.ReadAll(rsp.Body)
	require.NoError(t, err)
	return string(b)
}

func TestMakeRequestURLReplacement(t *testing.T) {
	t.Run("goes to the mirror anonymously", func(t *testing.T) {
		f := newMirrorFixture(t, nil)
		c, err := NewClient(
			WithBaseURL(f.upstream.URL),
			WithURLReplacements(f.rules()),
			WithCredentialResolver(credFor(t, f.upstream, nil)),
		)
		require.NoError(t, err)

		rsp, err := c.makeRequest(t.Context(), f.upstream.URL+"/index.yaml")
		require.NoError(t, err)
		assert.Equal(t, "mirror", readBody(t, rsp))

		assert.Zero(t, f.upstreamHits.Load())
		require.Len(t, f.mirrorReqs, 1)
		req := f.mirrorReqs[0]
		assert.Equal(t, "/prefix/index.yaml", req.URL.Path)
		assert.Empty(t, req.URL.RawQuery, "no mid/uid")
		assert.Empty(t, req.Header.Get("Authorization"))
		assert.Equal(t, "application/yaml", req.Header.Get("Accept"))
		assert.Equal(t, c.Registries()[0].BaseURL.String(), f.upstream.URL, "registry identity stays canonical")
	})

	t.Run("WithCredential is not applied", func(t *testing.T) {
		f := newMirrorFixture(t, nil)
		u, _ := url.Parse(f.upstream.URL)
		c, err := NewClient(
			WithURLReplacements(f.rules()),
			WithBaseURL(f.upstream.URL),
			WithCredential(&auth.Credential{Type: auth.TypeApiKey, ApiKey: "k", Token: "secret", RegistryURL: auth.Uri(*u)}),
		)
		require.NoError(t, err)
		rsp, err := c.makeRequest(t.Context(), f.upstream.URL+"/pkg.tar.gz")
		require.NoError(t, err)
		rsp.Body.Close()
		require.Len(t, f.mirrorReqs, 1)
		assert.Empty(t, f.mirrorReqs[0].Header.Get("Authorization"))
		assert.Empty(t, f.mirrorReqs[0].Header.Get("Accept"))
	})

	t.Run("401 does not ask the resolver or refresh", func(t *testing.T) {
		f := newMirrorFixture(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
		var lookups atomic.Int32
		c, err := NewClient(
			WithBaseURL(f.upstream.URL),
			WithURLReplacements(f.rules()),
			WithCredentialResolver(func(*url.URL) (*auth.Credential, error) {
				lookups.Add(1)
				return nil, nil
			}),
		)
		require.NoError(t, err)
		_, err = c.makeRequest(t.Context(), f.upstream.URL+"/index.yaml")
		require.ErrorIs(t, err, ErrUnauthorized)
		assert.Contains(t, err.Error(), strings.TrimPrefix(f.mirror.URL, "http://")+"/prefix/index.yaml")
		assert.Zero(t, lookups.Load())
		assert.Equal(t, int32(1), f.mirrorHits.Load(), "no retry")
		assert.Zero(t, f.upstreamHits.Load())
	})

	t.Run("canonical query is refused before sending", func(t *testing.T) {
		f := newMirrorFixture(t, nil)
		c, err := NewClient(WithBaseURL(f.upstream.URL), WithURLReplacements(f.rules()))
		require.NoError(t, err)
		_, err = c.makeRequest(t.Context(), f.upstream.URL+"/index.yaml?tenant=t1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "query")
		assert.Zero(t, f.upstreamHits.Load())
		assert.Zero(t, f.mirrorHits.Load())
	})

	t.Run("server error names the mirror, no fallback", func(t *testing.T) {
		f := newMirrorFixture(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		})
		c, err := NewClient(WithBaseURL(f.upstream.URL), WithURLReplacements(f.rules()))
		require.NoError(t, err)
		_, err = c.Search(t.Context(), "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), strings.TrimPrefix(f.mirror.URL, "http://")+"/prefix/index.yaml")
		assert.Contains(t, err.Error(), "500")
		assert.Zero(t, f.upstreamHits.Load())

		_, err = c.Download(t.Context(), PkgInfo{Path: mustParseURL(f.upstream.URL + "/a.tar.gz")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "/prefix/a.tar.gz")
		assert.Zero(t, f.upstreamHits.Load())
	})

	t.Run("connection refused names the mirror, no fallback", func(t *testing.T) {
		f := newMirrorFixture(t, nil)
		f.mirror.Close()
		c, err := NewClient(WithBaseURL(f.upstream.URL), WithURLReplacements(f.rules()))
		require.NoError(t, err)
		_, err = c.makeRequest(t.Context(), f.upstream.URL+"/index.yaml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), strings.TrimPrefix(f.mirror.URL, "http://")+"/prefix/index.yaml")
		assert.Zero(t, f.upstreamHits.Load())
	})

	t.Run("unmatched URL is unchanged", func(t *testing.T) {
		f := newMirrorFixture(t, nil)
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
			assert.NotEmpty(t, r.URL.Query().Get("uid"))
			assert.Contains(t, r.URL.Query(), "mid")
			io.WriteString(w, "other")
		}))
		defer other.Close()
		c, err := NewClient(
			WithBaseURL(other.URL),
			WithURLReplacements(f.rules()),
			WithCredentialResolver(credFor(t, other, nil)),
		)
		require.NoError(t, err)
		rsp, err := c.makeRequest(t.Context(), other.URL+"/index.yaml")
		require.NoError(t, err)
		assert.Equal(t, "other", readBody(t, rsp))
		assert.Zero(t, f.mirrorHits.Load())
	})

	t.Run("relative redirect is followed on the mirror", func(t *testing.T) {
		f := newMirrorFixture(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/prefix/start" {
				http.Redirect(w, r, "final", http.StatusFound)
				return
			}
			io.WriteString(w, "final:"+r.URL.Path)
		})
		c, err := NewClient(WithBaseURL(f.upstream.URL), WithURLReplacements(f.rules()))
		require.NoError(t, err)
		rsp, err := c.makeRequest(t.Context(), f.upstream.URL+"/start")
		require.NoError(t, err)
		assert.Equal(t, "final:/prefix/final", readBody(t, rsp))
		assert.Zero(t, f.upstreamHits.Load())
	})

	t.Run("explicit rules override the global config", func(t *testing.T) {
		f := newMirrorFixture(t, nil)
		g := &GlobalConfig{Network: NetworkConfig{URLReplacements: []URLReplacement{
			{Prefix: f.upstream.URL, Replacement: "http://127.0.0.1:1"},
		}}}
		c, err := NewClient(WithGlobalConfig(g), WithBaseURL(f.upstream.URL), WithURLReplacements(f.rules()))
		require.NoError(t, err)
		rsp, err := c.makeRequest(t.Context(), f.upstream.URL+"/index.yaml")
		require.NoError(t, err)
		rsp.Body.Close()
		assert.Equal(t, int32(1), f.mirrorHits.Load())
	})
}

func TestGlobalConfigURLReplacementsWithBaseURLAndRegistries(t *testing.T) {
	f := newMirrorFixture(t, nil)
	g := &GlobalConfig{Network: NetworkConfig{URLReplacements: f.rules()}}

	t.Run("WithBaseURL", func(t *testing.T) {
		c, err := NewClient(WithGlobalConfig(g), WithBaseURL(f.upstream.URL))
		require.NoError(t, err)
		rsp, err := c.makeRequest(t.Context(), f.upstream.URL+"/index.yaml")
		require.NoError(t, err)
		rsp.Body.Close()
	})
	t.Run("WithRegistries", func(t *testing.T) {
		c, err := NewClient(WithGlobalConfig(g), WithRegistries([]Registry{{BaseURL: mustParseURL(f.upstream.URL)}}))
		require.NoError(t, err)
		rsp, err := c.makeRequest(t.Context(), f.upstream.URL+"/index.yaml")
		require.NoError(t, err)
		rsp.Body.Close()
	})
	assert.Equal(t, int32(2), f.mirrorHits.Load())
	assert.Zero(t, f.upstreamHits.Load())
}

func TestURLReplacementHTTPSDowngradeRefused(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "plain")
	}))
	defer plain.Close()
	mirror := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/x", http.StatusFound)
	}))
	defer mirror.Close()

	hc := mirror.Client()
	hc.CheckRedirect = checkRedirect
	c, err := NewClient(
		WithHTTPClient(hc),
		WithBaseURL("https://upstream.example"),
		WithURLReplacements([]URLReplacement{{Prefix: "https://upstream.example", Replacement: mirror.URL}}),
	)
	require.NoError(t, err)
	_, err = c.makeRequest(t.Context(), "https://upstream.example/index.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing HTTPS to HTTP redirect")
}

func TestLegacyDownloadPackageURLReplacement(t *testing.T) {
	f := newMirrorFixture(t, nil)
	t.Cleanup(func() { _ = SetDefaultURLReplacements(nil) })
	require.NoError(t, SetDefaultURLReplacements(f.rules()))
	// TestMain points DefaultClient at a fixed test server; talk to the
	// fixture's mirror directly instead.
	origClient := DefaultClient
	DefaultClient = &http.Client{CheckRedirect: checkRedirect}
	t.Cleanup(func() { DefaultClient = origClient })

	pkg := PkgInfo{Path: mustParseURL(f.upstream.URL + "/pkg.tar.gz")}
	file, err := pkg.DownloadPackage(nil)
	require.NoError(t, err)
	defer os.RemoveAll(filepath.Dir(file.Name()))
	file.Close()

	assert.Zero(t, f.upstreamHits.Load())
	require.Len(t, f.mirrorReqs, 1)
	assert.Equal(t, "/prefix/pkg.tar.gz", f.mirrorReqs[0].URL.Path)
	assert.Empty(t, f.mirrorReqs[0].URL.RawQuery)
	assert.Empty(t, f.mirrorReqs[0].Header.Get("Authorization"))

	// A canonical query is refused before anything is sent.
	pkg = PkgInfo{Path: mustParseURL(f.upstream.URL + "/pkg.tar.gz?t=1")}
	_, err = pkg.DownloadPackage(nil)
	require.Error(t, err)
	assert.Equal(t, int32(1), f.mirrorHits.Load())
	assert.Zero(t, f.upstreamHits.Load())
}
