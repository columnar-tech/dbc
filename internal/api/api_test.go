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

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/auth"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Keep config discovery, credential lookup/refresh, and license writes away
// from the user's files. These tests do not run in parallel.
func isolateUserState(t *testing.T) (configDir, credPath string) {
	t.Helper()
	dir := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME"} {
		t.Setenv(key, dir)
	}
	switch runtime.GOOS {
	case "darwin":
		configDir = filepath.Join(dir, "Library", "Application Support", "Columnar", "dbc")
	case "windows":
		configDir = filepath.Join(dir, "Columnar", "dbc")
	default:
		configDir = filepath.Join(dir, "columnar", "dbc")
	}
	credPath = filepath.Join(dir, "credentials", "credentials.toml")
	restore := auth.SetCredPathForTesting(credPath)
	auth.ResetCredentialsForTesting()
	t.Cleanup(func() {
		restore()
		auth.ResetCredentialsForTesting()
	})
	return configDir, credPath
}

func writeConfigFile(t *testing.T, path, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
}

func clientRegistries(c *dbc.Client) []api.Registry {
	registries := c.Registries()
	out := make([]api.Registry, len(registries))
	for i, r := range registries {
		out[i] = api.Registry{URL: r.BaseURL.String(), Name: r.Name}
	}
	return out
}

func fixtureServer(t *testing.T) string {
	t.Helper()
	testdata := filepath.Join("..", "..", "cmd", "dbc", "testdata")
	index, err := os.ReadFile(filepath.Join(testdata, "test_index.yaml"))
	require.NoError(t, err)
	tarball, err := os.ReadFile(filepath.Join(testdata, "test-driver-1.tar.gz"))
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/index.yaml":
			w.Write(index)
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			w.Write(tarball)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func newClient(t *testing.T) *dbc.Client {
	t.Helper()
	isolateUserState(t)
	c, err := api.NewClient(api.ClientConfig{BaseURL: fixtureServer(t)}, &http.Client{})
	require.NoError(t, err)
	return c
}

// A malformed baseURL must be an error, not a panic that takes down the
// JS or C host.
func TestNewClientRejectsInvalidBaseURL(t *testing.T) {
	isolateUserState(t)
	_, err := api.NewClient(api.ClientConfig{BaseURL: "http://\x00"}, nil)
	require.ErrorContains(t, err, "invalid baseURL")
}

func TestNewClientRejectsInvalidCredentialURL(t *testing.T) {
	isolateUserState(t)
	_, err := api.NewClient(api.ClientConfig{Credential: &api.Credential{
		RegistryURL: "http://\x00",
		AuthURI:     "http://example.test",
		Token:       "t",
	}}, nil)
	require.ErrorContains(t, err, "invalid credential registryURL")
}

// The JSON names are consumed by @columnar-tech/dbc-wasm and the columnar-dbc
// crate; a renamed tag compiles fine in Go and silently breaks both.
func TestSearchWireFormat(t *testing.T) {
	res, err := api.Search(t.Context(), newClient(t), "test-driver-1")
	require.NoError(t, err)

	b, err := json.Marshal(res)
	require.NoError(t, err)
	var wire struct {
		Drivers []map[string]any `json:"drivers"`
	}
	require.NoError(t, json.Unmarshal(b, &wire))
	require.NotEmpty(t, wire.Drivers)
	assert.Equal(t, "test-driver-1", wire.Drivers[0]["path"])
	for _, key := range []string{"title", "license", "description"} {
		assert.Contains(t, wire.Drivers[0], key)
	}
	assert.NotContains(t, string(b), `"warning"`, "warning must be omitted when every registry answered")
}

func TestResolve(t *testing.T) {
	c := newClient(t)

	t.Run("explicit platform", func(t *testing.T) {
		res, err := api.Resolve(t.Context(), c, "test-driver-1", "linux_amd64")
		require.NoError(t, err)
		assert.Equal(t, "test-driver-1", res.Path)
		assert.Equal(t, "linux_amd64", res.Platform)
		assert.NotEmpty(t, res.Versions)
		require.NotNil(t, res.Latest)
		assert.True(t, strings.HasSuffix(res.Latest.URL, ".tar.gz"), res.Latest.URL)
	})

	t.Run("unknown driver", func(t *testing.T) {
		_, err := api.Resolve(t.Context(), c, "nonexistent-driver", "linux_amd64")
		require.EqualError(t, err, `driver "nonexistent-driver" not found`)
	})
}

func TestInstallListUninstall(t *testing.T) {
	c := newClient(t)
	target := api.Target{Location: t.TempDir()}

	m, err := api.Install(t.Context(), c, "test-driver-1", target, api.InstallOptions{})
	require.NoError(t, err)
	assert.Equal(t, "test-driver-1", m.ID)
	assert.FileExists(t, m.DriverPath)

	listed, err := api.ListInstalled(target)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "test-driver-1", listed[0].ID)

	require.NoError(t, api.Uninstall("test-driver-1", target))
	listed, err = api.ListInstalled(target)
	require.NoError(t, err)
	assert.Empty(t, listed)
}

// Resolution only: writing to the real levels here would race with the CLI's
// level suites, which RemoveAll those directories (see the Decisions table).
func TestTarget(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		cfg, err := api.Target{Location: dir}.Config()
		require.NoError(t, err)
		assert.Equal(t, config.ConfigEnv, cfg.Level)
		assert.Equal(t, dir, cfg.Location)
	})

	t.Run("levels resolve to the CLI's locations", func(t *testing.T) {
		for _, lvl := range []config.ConfigLevel{config.ConfigUser, config.ConfigSystem} {
			cfg, err := api.Target{Level: lvl.String()}.Config()
			require.NoError(t, err, lvl)
			assert.Equal(t, lvl, cfg.Level)
			assert.Equal(t, lvl.ConfigLocation(), cfg.Location)
		}
	})

	for _, tc := range []struct {
		name   string
		target api.Target
		want   string
	}{
		{"both", api.Target{Level: "user", Location: "/tmp/x"}, "set either level or location, not both"},
		{"neither", api.Target{}, "either level or location is required"},
		{"unknown level", api.Target{Level: "env"}, `unknown config level "env", valid values are: user, system`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.target.Config()
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestClientsHandles(t *testing.T) {
	var reg api.Clients
	c := newClient(t)

	h := reg.Add(c)
	got, err := reg.Get(h)
	require.NoError(t, err)
	assert.Same(t, c, got)

	reg.Remove(h)
	_, err = reg.Get(h)
	require.EqualError(t, err, fmt.Sprintf("invalid client handle %d", h))
	assert.NotEqual(t, h, reg.Add(c), "handles must never be reused")
}

func TestSearchPartialFailureNoMatches(t *testing.T) {
	isolateUserState(t)
	down := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(down.Close)
	var regs []dbc.Registry
	for _, u := range []string{fixtureServer(t), down.URL} {
		base, err := url.Parse(u)
		require.NoError(t, err)
		regs = append(regs, dbc.Registry{BaseURL: base})
	}
	c, err := dbc.NewClient(dbc.WithRegistries(regs))
	require.NoError(t, err)

	res, err := api.Search(t.Context(), c, "no-such-driver")
	require.NoError(t, err)
	assert.Empty(t, res.Drivers)
	assert.NotEmpty(t, res.Warning)
}

func TestSearchPartialFailureEmptyIndex(t *testing.T) {
	isolateUserState(t)
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.yaml" {
			w.Write([]byte("drivers: []\n"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(empty.Close)
	down := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(down.Close)
	var regs []dbc.Registry
	for _, u := range []string{empty.URL, down.URL} {
		base, err := url.Parse(u)
		require.NoError(t, err)
		regs = append(regs, dbc.Registry{BaseURL: base})
	}
	c, err := dbc.NewClient(dbc.WithRegistries(regs))
	require.NoError(t, err)

	res, err := api.Search(t.Context(), c, "")
	require.NoError(t, err)
	assert.Empty(t, res.Drivers)
	assert.NotEmpty(t, res.Warning)
}

// latest is the package Install would fetch: the newest stable version
// overall. When that version has no package for the platform, Install fails,
// so latest is absent even though an older version lists the platform.
func TestResolveLatestMatchesInstall(t *testing.T) {
	isolateUserState(t)
	const index = `drivers:
  - name: Test Driver
    path: test-driver
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            url: test-driver/1.0.0/linux.tar.gz
      - version: v2.0.0
        packages:
          - platform: windows_amd64
            url: test-driver/2.0.0/windows.tar.gz
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.yaml" {
			w.Write([]byte(index))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	c, err := api.NewClient(api.ClientConfig{BaseURL: srv.URL}, nil)
	require.NoError(t, err)

	res, err := api.Resolve(t.Context(), c, "test-driver", "linux_amd64")
	require.NoError(t, err)
	assert.Equal(t, []string{"1.0.0"}, res.Versions)
	assert.Nil(t, res.Latest)
}

func TestClientConfigWireFormat(t *testing.T) {
	var cfg api.ClientConfig
	require.NoError(t, json.Unmarshal([]byte(`{
		"registries": [{"url": "https://first.test", "name": "First"}, {"url": "https://second.test"}],
		"registryConfig": "none",
		"projectFile": "/project/dbc.toml",
		"credentialStore": "none"
	}`), &cfg))
	assert.Equal(t, api.ClientConfig{
		Registries:      []api.Registry{{URL: "https://first.test", Name: "First"}, {URL: "https://second.test"}},
		RegistryConfig:  "none",
		ProjectFile:     "/project/dbc.toml",
		CredentialStore: "none",
	}, cfg)
	b, err := json.Marshal(api.Registry{URL: "https://first.test"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"url":"https://first.test"}`, string(b))
}

func TestNewClientExplicitRegistries(t *testing.T) {
	isolateUserState(t)
	registries := []api.Registry{
		{URL: "https://second.test", Name: "Second"},
		{URL: "https://first.test", Name: "First"},
		{URL: "https://second.test", Name: "Duplicate kept"},
	}
	c, err := api.NewClient(api.ClientConfig{Registries: registries}, nil)
	require.NoError(t, err)
	assert.Equal(t, registries, clientRegistries(c))

	_, err = api.NewClient(api.ClientConfig{BaseURL: "https://base.test", Registries: registries}, nil)
	require.ErrorContains(t, err, "baseURL")
	assert.ErrorContains(t, err, "registries")
}

// Explicit registries follow the same URL rules as config-file registries, so
// a bad URL fails at construction rather than on the first request.
func TestNewClientRejectsInvalidExplicitRegistryURLs(t *testing.T) {
	isolateUserState(t)
	for url, want := range map[string]string{
		"":                      "empty url",
		"ftp://example.test":    "scheme must be http or https",
		"https:///missing-host": "missing host",
		"relative/path":         "missing host",
	} {
		t.Run(url, func(t *testing.T) {
			_, err := api.NewClient(api.ClientConfig{Registries: []api.Registry{
				{URL: "https://ok.test"},
				{URL: url},
			}}, nil)
			require.ErrorContains(t, err, "registries[1].url")
			assert.ErrorContains(t, err, want)
		})
	}
}

func TestNewClientRegistryConfigPrecedence(t *testing.T) {
	const globalTOML = "[[registries]]\nurl = \"https://global.test\"\nname = \"Global\"\n"
	const projectTOML = "[[registries]]\nurl = \"https://project.test\"\nname = \"Project\"\n"
	const replaceDefaults = "replace_defaults = true\n"
	defaults := []api.Registry{
		{URL: "https://dbc-cdn.columnar.tech"},
		{URL: "https://" + auth.DefaultOauthURI()},
	}
	global := api.Registry{URL: "https://global.test", Name: "Global"}
	project := api.Registry{URL: "https://project.test", Name: "Project"}
	for _, tc := range []struct {
		name           string
		registryConfig string
		globalTOML     string
		projectTOML    string
		want           []api.Registry
		wantErr        string
	}{
		{
			name: "missing global uses defaults",
			want: defaults,
		},
		{
			name:       "zero value loads global",
			globalTOML: globalTOML,
			want:       append([]api.Registry{global}, defaults...),
		},
		{
			name:           "explicit default loads global",
			registryConfig: "default",
			globalTOML:     replaceDefaults + globalTOML,
			want:           []api.Registry{global},
		},
		{
			name:        "project before global and defaults",
			globalTOML:  globalTOML,
			projectTOML: projectTOML,
			want:        append([]api.Registry{project, global}, defaults...),
		},
		{
			name:        "project inherits global replacement",
			globalTOML:  replaceDefaults + globalTOML,
			projectTOML: projectTOML,
			want:        []api.Registry{project, global},
		},
		{
			name:        "project false restores defaults",
			globalTOML:  replaceDefaults + globalTOML,
			projectTOML: "replace_defaults = false\n" + projectTOML,
			want:        append([]api.Registry{project, global}, defaults...),
		},
		{
			name:        "project true removes defaults",
			globalTOML:  globalTOML,
			projectTOML: replaceDefaults + projectTOML,
			want:        []api.Registry{project, global},
		},
		{
			name:        "project name wins duplicate",
			globalTOML:  replaceDefaults + "[[registries]]\nurl = \"https://project.test/\"\nname = \"Global\"\n",
			projectTOML: projectTOML,
			want:        []api.Registry{project},
		},
		{
			name:           "none skips broken global",
			registryConfig: "none",
			globalTOML:     "[[broken",
			want:           defaults,
		},
		{
			name:           "none still loads project",
			registryConfig: "none",
			globalTOML:     "[[broken",
			projectTOML:    replaceDefaults + projectTOML,
			want:           []api.Registry{project},
		},
		{
			name:        "project fills empty global replacement",
			globalTOML:  replaceDefaults,
			projectTOML: projectTOML,
			want:        []api.Registry{project},
		},
		{
			name:       "empty replacement errors",
			globalTOML: replaceDefaults,
			wantErr:    "empty registry list",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := isolateUserState(t)
			if tc.globalTOML != "" {
				writeConfigFile(t, filepath.Join(dir, "config.toml"), tc.globalTOML)
			}
			cfg := api.ClientConfig{RegistryConfig: tc.registryConfig}
			if tc.projectTOML != "" {
				cfg.ProjectFile = filepath.Join(t.TempDir(), "dbc.toml")
				writeConfigFile(t, cfg.ProjectFile, tc.projectTOML)
			}
			c, err := api.NewClient(cfg, nil)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				assert.Nil(t, c)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, clientRegistries(c))
		})
	}
}

func TestNewClientExplicitRegistriesIgnoreFiles(t *testing.T) {
	for _, explicit := range []string{"baseURL", "registries"} {
		for _, projectExists := range []bool{false, true} {
			for _, mode := range []string{"default", "invalid-but-ignored"} {
				t.Run(fmt.Sprintf("%s/project-exists=%t/%s", explicit, projectExists, mode), func(t *testing.T) {
					dir, _ := isolateUserState(t)
					writeConfigFile(t, filepath.Join(dir, "config.toml"), "[[broken")
					cfg := api.ClientConfig{
						RegistryConfig: mode,
						ProjectFile:    filepath.Join(t.TempDir(), "dbc.toml"),
					}
					if projectExists {
						writeConfigFile(t, cfg.ProjectFile, "[[also-broken")
					}
					if explicit == "baseURL" {
						cfg.BaseURL = "https://explicit.test"
					} else {
						cfg.Registries = []api.Registry{{URL: "https://explicit.test"}}
					}
					c, err := api.NewClient(cfg, nil)
					require.NoError(t, err)
					assert.Equal(t, []api.Registry{{URL: "https://explicit.test"}}, clientRegistries(c))
				})
			}
		}
	}
}

func TestNewClientRejectsInvalidOptions(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  api.ClientConfig
		want string
	}{
		{"registry config", api.ClientConfig{RegistryConfig: "other"}, "registryConfig"},
		{"credential store", api.ClientConfig{CredentialStore: "other"}, "credentialStore"},
		{"credential store with explicit registry", api.ClientConfig{BaseURL: "https://registry.test", CredentialStore: "other"}, "credentialStore"},
		{"registry URL", api.ClientConfig{Registries: []api.Registry{{URL: "http://\x00"}}}, "registries[0].url"},
		{"credential auth URI", api.ClientConfig{Credential: &api.Credential{RegistryURL: "https://registry.test", AuthURI: "http://\x00"}}, "credential authURI"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateUserState(t)
			c, err := api.NewClient(tc.cfg, nil)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, c)
		})
	}
}

func TestNewClientConfigErrorsAreFatal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		project bool
		toml    string
		missing bool
	}{
		{name: "global malformed TOML", toml: "[[broken"},
		{name: "global invalid URL", toml: "[[registries]]\nurl = \"https:///missing-host\"\n"},
		{name: "project malformed TOML", project: true, toml: "[[broken"},
		{name: "project invalid URL", project: true, toml: "[[registries]]\nurl = \"ftp://example.test\"\n"},
		{name: "project missing", project: true, missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := isolateUserState(t)
			cfg := api.ClientConfig{}
			path := filepath.Join(dir, "config.toml")
			if tc.project {
				path = filepath.Join(t.TempDir(), "dbc.toml")
				cfg.ProjectFile = path
			}
			if !tc.missing {
				writeConfigFile(t, path, tc.toml)
			}
			c, err := api.NewClient(cfg, nil)
			require.ErrorContains(t, err, path)
			assert.Nil(t, c)
			if tc.missing {
				assert.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestNewClientMissingConfigDirectory(t *testing.T) {
	isolateUserState(t)
	for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "XDG_CONFIG_HOME"} {
		t.Setenv(key, "")
	}
	_, err := api.NewClient(api.ClientConfig{}, nil)
	require.ErrorContains(t, err, "config directory")
	for _, cfg := range []api.ClientConfig{
		{RegistryConfig: "none"},
		{BaseURL: "https://registry.test"},
		{Registries: []api.Registry{{URL: "https://registry.test"}}},
	} {
		_, err := api.NewClient(cfg, nil)
		require.NoError(t, err)
	}
}

func tokenRegistry(t *testing.T) (string, <-chan string) {
	t.Helper()
	headers := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Get("Authorization")
		w.Write([]byte("drivers: []\n"))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, headers
}

func TestNewClientCredentialStoreFallback(t *testing.T) {
	for _, mode := range []string{"", "default", "none"} {
		for _, injected := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/injected=%t", mode, injected), func(t *testing.T) {
				isolateUserState(t)
				first, firstHeaders := tokenRegistry(t)
				second, secondHeaders := tokenRegistry(t)
				require.NoError(t, api.Login(api.Credential{RegistryURL: first, AuthURI: first, Token: "stored-first"}))
				require.NoError(t, api.Login(api.Credential{RegistryURL: second, AuthURI: second, Token: "stored-second"}))
				auth.ResetCredentialsForTesting()
				cfg := api.ClientConfig{
					Registries:      []api.Registry{{URL: first}, {URL: second}},
					CredentialStore: mode,
				}
				if injected {
					cfg.Credential = &api.Credential{RegistryURL: first + "/private", AuthURI: first, Token: "injected"}
				}
				c, err := api.NewClient(cfg, nil)
				require.NoError(t, err)
				_, err = api.Search(t.Context(), c, "")
				require.NoError(t, err)
				wantFirst, wantSecond := "Bearer stored-first", "Bearer stored-second"
				if mode == "none" {
					wantFirst, wantSecond = "", ""
				}
				if injected {
					wantFirst = "Bearer injected"
				}
				assert.Equal(t, wantFirst, <-firstHeaders)
				assert.Equal(t, wantSecond, <-secondHeaders)
			})
		}
	}
}

func TestNewClientCredentialStoreNoneNeverReadsStore(t *testing.T) {
	_, path := isolateUserState(t)
	writeConfigFile(t, path, "[[broken")
	server, headers := tokenRegistry(t)
	c, err := api.NewClient(api.ClientConfig{BaseURL: server, CredentialStore: "none"}, nil)
	require.NoError(t, err)
	_, err = api.Search(t.Context(), c, "")
	require.NoError(t, err)
	assert.Empty(t, <-headers)

	c, err = api.NewClient(api.ClientConfig{
		BaseURL:    server,
		Credential: &api.Credential{RegistryURL: server, AuthURI: server, Token: "injected"},
	}, nil)
	require.NoError(t, err)
	_, err = api.Search(t.Context(), c, "")
	require.NoError(t, err)
	assert.Equal(t, "Bearer injected", <-headers)

	c, err = api.NewClient(api.ClientConfig{
		BaseURL:    server,
		Credential: &api.Credential{RegistryURL: "https://other.test", AuthURI: server, Token: "injected"},
	}, nil)
	require.NoError(t, err)
	_, err = api.Search(t.Context(), c, "")
	require.ErrorContains(t, err, "failed to read credentials")
}

func TestNewClientKeepsRefreshedInjectedCredential(t *testing.T) {
	isolateUserState(t)
	var refreshes, staleRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{"token_endpoint": "http://" + r.Host + "/token"})
		case "/token":
			assert.NoError(t, r.ParseForm())
			assert.Equal(t, "refresh-token", r.Form.Get("refresh_token"))
			assert.Equal(t, "client-id", r.Form.Get("client_id"))
			refreshes.Add(1)
			w.Write([]byte(`{"access_token":"fresh"}`))
		case "/index.yaml":
			if r.Header.Get("Authorization") != "Bearer fresh" {
				staleRequests.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Write([]byte("drivers: []\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	c, err := api.NewClient(api.ClientConfig{
		BaseURL:         server.URL,
		CredentialStore: "none",
		Credential: &api.Credential{
			RegistryURL: server.URL, AuthURI: server.URL, Token: "stale",
			RefreshToken: "refresh-token", ClientID: "client-id",
		},
	}, nil)
	require.NoError(t, err)
	for range 2 {
		_, err := api.Search(t.Context(), c, "")
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), refreshes.Load())
	assert.Equal(t, int32(1), staleRequests.Load())
}

func TestLoginLogoutPersistence(t *testing.T) {
	isolateUserState(t)
	cred := api.Credential{
		RegistryURL:  "https://registry.test/private",
		AuthURI:      "https://auth.test",
		Token:        "first-token",
		RefreshToken: "refresh-token",
		ClientID:     "client-id",
	}
	require.NoError(t, api.Login(cred))
	other := api.Credential{RegistryURL: "https://other.test", AuthURI: "https://auth.test", Token: "other-token"}
	require.NoError(t, api.Login(other))
	regURL, err := url.Parse(cred.RegistryURL)
	require.NoError(t, err)
	auth.ResetCredentialsForTesting()
	stored, err := auth.GetCredentials(regURL)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, auth.TypeToken, stored.Type)
	assert.Equal(t, cred.RegistryURL, stored.RegistryURL.String())
	assert.Equal(t, cred.AuthURI, stored.AuthURI.String())
	assert.Equal(t, cred.Token, stored.Token)
	assert.Equal(t, cred.RefreshToken, stored.RefreshToken)
	assert.Equal(t, cred.ClientID, stored.ClientID)

	cred.RegistryURL = "https://registry.test/other"
	cred.Token = "replacement-token"
	require.NoError(t, api.Login(cred))
	auth.ResetCredentialsForTesting()
	stored, err = auth.GetCredentials(regURL)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "replacement-token", stored.Token)
	require.NoError(t, api.Logout("https://registry.test/different-path"))
	auth.ResetCredentialsForTesting()
	stored, err = auth.GetCredentials(regURL)
	require.NoError(t, err)
	assert.Nil(t, stored)
	otherURL, err := url.Parse(other.RegistryURL)
	require.NoError(t, err)
	stored, err = auth.GetCredentials(otherURL)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "other-token", stored.Token)
	require.ErrorContains(t, api.Logout(cred.RegistryURL), "no credentials found")
}

func TestLoginLogoutRejectInvalidURLs(t *testing.T) {
	_, path := isolateUserState(t)
	for _, cred := range []api.Credential{
		{RegistryURL: "http://\x00", AuthURI: "https://auth.test"},
		{RegistryURL: "https://registry.test", AuthURI: "http://\x00"},
	} {
		require.Error(t, api.Login(cred))
		assert.NoFileExists(t, path)
	}
	require.ErrorContains(t, api.Logout("http://\x00"), "registryURL")
	assert.NoFileExists(t, path)
}

func TestLoginRejectsInvalidInputsWithoutReplacingCredential(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value string
	}{
		{"missing registry URL", "registryURL", ""},
		{"relative registry URL", "registryURL", "registry.test/private"},
		{"hostless registry URL", "registryURL", "https:///private"},
		{"opaque registry URL", "registryURL", "https:registry.test"},
		{"schemeless registry URL", "registryURL", "//registry.test/private"},
		{"missing auth URI", "authURI", ""},
		{"relative auth URI", "authURI", "auth.test/token"},
		{"hostless auth URI", "authURI", "https:///token"},
		{"opaque auth URI", "authURI", "https:auth.test"},
		{"schemeless auth URI", "authURI", "//auth.test/token"},
		{"missing token", "token", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, path := isolateUserState(t)
			cred := api.Credential{
				RegistryURL: "https://registry.test/private",
				AuthURI:     "https://auth.test",
				Token:       "stored-token",
			}
			require.NoError(t, api.Login(cred))
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			regURL, err := url.Parse(cred.RegistryURL)
			require.NoError(t, err)

			cred.Token = "replacement-token"
			switch tc.field {
			case "registryURL":
				cred.RegistryURL = tc.value
			case "authURI":
				cred.AuthURI = tc.value
			case "token":
				cred.Token = tc.value
			}
			assert.ErrorContains(t, api.Login(cred), tc.field)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			auth.ResetCredentialsForTesting()
			stored, err := auth.GetCredentials(regURL)
			require.NoError(t, err)
			require.NotNil(t, stored)
			assert.Equal(t, "stored-token", stored.Token)
		})
	}
}

func TestLogoutRejectsInvalidURLsWithoutRemovingCredential(t *testing.T) {
	for _, registryURL := range []string{
		"",
		"registry.test/private",
		"https:///private",
		"https:registry.test",
		"//registry.test/private",
	} {
		t.Run(registryURL, func(t *testing.T) {
			_, path := isolateUserState(t)
			cred := api.Credential{
				RegistryURL: "https://registry.test/private",
				AuthURI:     "https://auth.test",
				Token:       "stored-token",
			}
			require.NoError(t, api.Login(cred))
			before, err := os.ReadFile(path)
			require.NoError(t, err)

			assert.ErrorContains(t, api.Logout(registryURL), "registryURL")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			auth.ResetCredentialsForTesting()
			regURL, err := url.Parse(cred.RegistryURL)
			require.NoError(t, err)
			stored, err := auth.GetCredentials(regURL)
			require.NoError(t, err)
			require.NotNil(t, stored)
			assert.Equal(t, "stored-token", stored.Token)
		})
	}
}

func TestInstallLicense(t *testing.T) {
	_, credPath := isolateUserState(t)
	dest, err := api.LicensePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(filepath.Dir(credPath), "columnar.lic"), dest)
	assert.NoFileExists(t, dest)

	src := filepath.Join(t.TempDir(), "columnar.lic")
	writeConfigFile(t, src, "first license")
	require.NoError(t, api.InstallLicense(src, false))
	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "first license", string(data))

	writeConfigFile(t, src, "replacement license")
	err = api.InstallLicense(src, false)
	require.ErrorContains(t, err, "force")
	assert.NotContains(t, err.Error(), "--force")
	data, err = os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "first license", string(data))
	require.NoError(t, api.InstallLicense(src, true))
	data, err = os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "replacement license", string(data))
}

func TestInstallLicenseForceSkipsFilenameCheck(t *testing.T) {
	isolateUserState(t)
	src := filepath.Join(t.TempDir(), "download.lic")
	writeConfigFile(t, src, "license")
	err := api.InstallLicense(src, false)
	require.ErrorContains(t, err, "force")
	assert.NotContains(t, err.Error(), "--force")
	dest, err := api.LicensePath()
	require.NoError(t, err)
	assert.NoFileExists(t, dest)
	require.NoError(t, api.InstallLicense(src, true))
	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "license", string(data))
}

func TestInstallLicensePreservesFilesystemErrors(t *testing.T) {
	isolateUserState(t)
	err := api.InstallLicense(filepath.Join(t.TempDir(), "columnar.lic"), false)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestNewClientEmptyExplicitRegistriesIgnoreConfig(t *testing.T) {
	dir, _ := isolateUserState(t)
	writeConfigFile(t, filepath.Join(dir, "config.toml"), "[[broken")
	cfg := api.ClientConfig{
		Registries:     []api.Registry{},
		RegistryConfig: "invalid-but-ignored",
		ProjectFile:    filepath.Join(t.TempDir(), "missing.toml"),
	}
	c, err := api.NewClient(cfg, nil)
	require.NoError(t, err)
	assert.Empty(t, c.Registries())
	cfg.BaseURL = "https://registry.test"
	_, err = api.NewClient(cfg, nil)
	require.ErrorContains(t, err, "baseURL")
	assert.ErrorContains(t, err, "registries")
}
