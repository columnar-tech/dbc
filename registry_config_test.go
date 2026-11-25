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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/config"
	"github.com/go-faster/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func urls(regs []Registry) []string {
	out := make([]string, len(regs))
	for i, r := range regs {
		if r.BaseURL != nil {
			out[i] = r.BaseURL.String()
		}
	}
	return out
}

func TestLoadGlobalConfig(t *testing.T) {
	tests := []struct {
		name        string
		toml        string
		omitFile    bool // if true, don't write config.toml (tests missing-file path)
		wantNil     bool
		wantErr     string          // substring; "" means no error
		wantEntries []RegistryEntry // expected ordered contents of cfg.Registries on success
		wantExclude bool
	}{
		{
			name: "two registries, no exclude_builtin_registries",
			toml: `
[[registries]]
url = "https://example.com/registry"
name = "example"

[[registries]]
url = "https://other.example.org"
`,
			wantEntries: []RegistryEntry{
				{URL: "https://example.com/registry", Name: "example"},
				{URL: "https://other.example.org"},
			},
		},
		{
			name: "exclude_builtin_registries true with one registry",
			toml: `
exclude_builtin_registries = true

[[registries]]
url = "https://custom.registry.io"
`,
			wantEntries: []RegistryEntry{{URL: "https://custom.registry.io"}},
			wantExclude: true,
		},
		{
			name:     "missing config.toml returns nil,nil",
			omitFile: true,
			wantNil:  true,
		},
		{
			name:        "exclude_builtin_registries=true with no entries accepted (project may supply)",
			toml:        "exclude_builtin_registries = true\n",
			wantExclude: true,
		},
		{
			name:    "invalid URL rejected",
			toml:    "[[registries]]\nurl = \"http://bad url with spaces\"\n",
			wantErr: "invalid registry URL",
		},
		{
			name:    "empty URL rejected",
			toml:    "[[registries]]\nurl = \"\"\n",
			wantErr: "empty url",
		},
		{
			name:    "non-http scheme rejected",
			toml:    "[[registries]]\nurl = \"ftp://example.com\"\n",
			wantErr: "scheme must be http or https",
		},
		{
			name:    "missing host rejected",
			toml:    "[[registries]]\nurl = \"https:///onlypath\"\n",
			wantErr: "missing host",
		},
		{
			name:    "malformed TOML rejected",
			toml:    "[[registries\nurl = \"https://example.com\"\n",
			wantErr: "invalid",
		},
		{
			name:    "duplicate registry name rejected",
			toml:    "[[registries]]\nurl = \"https://a.example.com\"\nname = \"acme\"\n[[registries]]\nurl = \"https://b.example.com\"\nname = \"acme\"\n",
			wantErr: `registry name "acme" is declared more than once`,
		},
		{
			name: "unnamed registries do not collide",
			toml: "[[registries]]\nurl = \"https://a.example.com\"\n[[registries]]\nurl = \"https://b.example.com\"\n",
			wantEntries: []RegistryEntry{
				{URL: "https://a.example.com"},
				{URL: "https://b.example.com"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if !tc.omitFile {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tc.toml), 0600))
			}
			cfg, err := LoadGlobalConfig(dir)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				assert.Nil(t, cfg)
				return
			}
			require.NoError(t, err)
			if tc.wantNil {
				assert.Nil(t, cfg)
				return
			}
			require.NotNil(t, cfg)
			assert.Equal(t, tc.wantEntries, cfg.Registries)
			assert.Equal(t, tc.wantExclude, cfg.ExcludeBuiltinRegistries)
		})
	}
}

func TestLoadProjectConfig(t *testing.T) {
	for _, tc := range []struct {
		name        string
		toml        string
		wantEntries []RegistryEntry
		wantExclude bool
		wantErr     string
	}{
		{
			name: "reads registries without decoding driver specifications",
			toml: `
[[registries]]
url = "https://project.test"
name = "Project"
[[registries]]
url = "https://second.test"
[drivers.example]
version = "not a version constraint"
prerelease = ["not", "a", "string"]
`,
			wantEntries: []RegistryEntry{{URL: "https://project.test", Name: "Project"}, {URL: "https://second.test"}},
		},
		{name: "empty project keeps built-in registries", toml: ""},
		{name: "explicit false", toml: "exclude_builtin_registries = false\n"},
		{name: "true without registries is resolved after merging", toml: "exclude_builtin_registries = true\n", wantExclude: true},
		{name: "malformed TOML", toml: "[[registries", wantErr: "invalid"},
		{name: "wrong exclusion type", toml: "exclude_builtin_registries = \"false\"\n", wantErr: "invalid"},
		{name: "empty registry URL", toml: "[[registries]]\nurl = \"\"\n", wantErr: "empty url"},
		{name: "malformed registry URL", toml: "[[registries]]\nurl = \"http://bad host\"\n", wantErr: "invalid registry URL"},
		{name: "missing host", toml: "[[registries]]\nurl = \"https:///onlypath\"\n", wantErr: "missing host"},
		{name: "unsupported registry scheme", toml: "[[registries]]\nurl = \"ftp://example.test\"\n", wantErr: "scheme must be http or https"},
		{name: "duplicate registry name", toml: "[[registries]]\nurl = \"https://a.test\"\nname = \"x\"\n[[registries]]\nurl = \"https://b.test\"\nname = \"x\"\n", wantErr: "declared more than once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dbc.toml")
			require.NoError(t, os.WriteFile(path, []byte(tc.toml), 0o600))
			cfg, err := LoadProjectConfig(path)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				assert.ErrorContains(t, err, path)
				assert.Nil(t, cfg)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantEntries, cfg.Registries)
			assert.Equal(t, tc.wantExclude, cfg.ExcludeBuiltinRegistries)
			assert.Equal(t, path, cfg.Path)
		})
	}
}

func TestLoadProjectConfigMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbc.toml")
	cfg, err := LoadProjectConfig(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.ErrorContains(t, err, path)
	assert.Nil(t, cfg)
}

// entryRegistries converts entries without signing keys to registries.
func entryRegistries(t *testing.T, entries []RegistryEntry) []Registry {
	t.Helper()
	registries, err := registriesFromEntries(entries, nil)
	require.NoError(t, err)
	return registries
}

func TestMergeRegistries(t *testing.T) {
	defaults := []Registry{
		{BaseURL: mustParseURL("https://default-a.example.com")},
		{BaseURL: mustParseURL("https://default-b.example.com")},
	}

	tests := []struct {
		name            string
		project         []RegistryEntry
		global          []RegistryEntry
		excludeBuiltin  bool
		defaults        []Registry
		wantURLsInOrder []string
		wantFirstName   string // optional: assert Name of result[0]
	}{
		{
			name:     "no entries returns defaults",
			defaults: defaults,
			wantURLsInOrder: []string{
				"https://default-a.example.com",
				"https://default-b.example.com",
			},
		},
		{
			name:     "global prepended before defaults",
			global:   []RegistryEntry{{URL: "https://global.example.com", Name: "g"}},
			defaults: defaults,
			wantURLsInOrder: []string{
				"https://global.example.com",
				"https://default-a.example.com",
				"https://default-b.example.com",
			},
			wantFirstName: "g",
		},
		{
			name:     "project before global before defaults",
			project:  []RegistryEntry{{URL: "https://proj.example.com"}},
			global:   []RegistryEntry{{URL: "https://glob.example.com"}},
			defaults: defaults,
			wantURLsInOrder: []string{
				"https://proj.example.com",
				"https://glob.example.com",
				"https://default-a.example.com",
				"https://default-b.example.com",
			},
		},
		{
			name:           "excluding built-in registries keeps project and global entries",
			project:        []RegistryEntry{{URL: "https://proj.example.com"}},
			global:         []RegistryEntry{{URL: "https://glob.example.com"}},
			excludeBuiltin: true,
			defaults:       defaults,
			wantURLsInOrder: []string{
				"https://proj.example.com",
				"https://glob.example.com",
			},
		},
		{
			name:           "excluding built-in registries with only global entries",
			global:         []RegistryEntry{{URL: "https://only.example.com"}},
			excludeBuiltin: true,
			defaults:       defaults,
			wantURLsInOrder: []string{
				"https://only.example.com",
			},
		},
		{
			name:           "an entry with a built-in URL is kept when built-in registries are excluded",
			global:         []RegistryEntry{{URL: "https://default-a.example.com", Name: "explicit"}},
			excludeBuiltin: true,
			defaults:       defaults,
			wantURLsInOrder: []string{
				"https://default-a.example.com",
			},
			wantFirstName: "explicit",
		},
		{
			name:     "duplicate URLs dedupe, first wins",
			project:  []RegistryEntry{{URL: "https://dup.example.com", Name: "project"}},
			global:   []RegistryEntry{{URL: "https://dup.example.com", Name: "global"}},
			defaults: defaults,
			wantURLsInOrder: []string{
				"https://dup.example.com",
				"https://default-a.example.com",
				"https://default-b.example.com",
			},
			wantFirstName: "project",
		},
		{
			name:     "project URL collides with default URL — project wins, default URL dedup'd",
			project:  []RegistryEntry{{URL: "https://default-a.example.com", Name: "project-name"}},
			defaults: defaults,
			wantURLsInOrder: []string{
				"https://default-a.example.com",
				"https://default-b.example.com",
			},
			wantFirstName: "project-name",
		},
		{
			name:     "global URL collides with default URL — global wins, default URL dedup'd",
			global:   []RegistryEntry{{URL: "https://default-a.example.com", Name: "global-name"}},
			defaults: defaults,
			wantURLsInOrder: []string{
				"https://default-a.example.com",
				"https://default-b.example.com",
			},
			wantFirstName: "global-name",
		},
		{
			name:     "all three tiers share a URL — project wins, others dedup'd",
			project:  []RegistryEntry{{URL: "https://shared.example.com", Name: "p"}},
			global:   []RegistryEntry{{URL: "https://shared.example.com", Name: "g"}},
			defaults: []Registry{{Name: "d", BaseURL: mustParseURL("https://shared.example.com")}},
			wantURLsInOrder: []string{
				"https://shared.example.com",
			},
			wantFirstName: "p",
		},
		{
			name:     "project URL matches second default — project wins, only that default URL dedup'd",
			project:  []RegistryEntry{{URL: "https://default-b.example.com", Name: "project-shadows-b"}},
			global:   []RegistryEntry{{URL: "https://glob.example.com"}},
			defaults: defaults,
			wantURLsInOrder: []string{
				"https://default-b.example.com",
				"https://glob.example.com",
				"https://default-a.example.com",
			},
			wantFirstName: "project-shadows-b",
		},
		{
			name: "distinct queries not deduped (tenant selectors)",
			project: []RegistryEntry{
				{URL: "https://r.example.com?tenant=a"},
				{URL: "https://r.example.com?tenant=b"},
			},
			excludeBuiltin: true,
			wantURLsInOrder: []string{
				"https://r.example.com?tenant=a",
				"https://r.example.com?tenant=b",
			},
		},
		{
			name: "distinct userinfo not deduped",
			project: []RegistryEntry{
				{URL: "https://u1@r.example.com"},
				{URL: "https://u2@r.example.com"},
			},
			excludeBuiltin: true,
			wantURLsInOrder: []string{
				"https://u1@r.example.com",
				"https://u2@r.example.com",
			},
		},
		{
			name: "distinct paths not deduped",
			project: []RegistryEntry{
				{URL: "https://r.example.com/tenant-a"},
				{URL: "https://r.example.com/tenant-b"},
			},
			excludeBuiltin: true,
			wantURLsInOrder: []string{
				"https://r.example.com/tenant-a",
				"https://r.example.com/tenant-b",
			},
		},
		{
			name: "casing + trailing slash dedupe",
			project: []RegistryEntry{
				{URL: "https://R.Example.COM/path/"},
				{URL: "HTTPS://r.example.com/path"},
			},
			excludeBuiltin: true,
			wantURLsInOrder: []string{
				"https://R.Example.COM/path/", // first wins; kept verbatim
			},
		},
		{
			name: "fragment-only differences dedupe",
			project: []RegistryEntry{
				{URL: "https://r.example.com/#a"},
				{URL: "https://r.example.com/#b"},
			},
			excludeBuiltin: true,
			wantURLsInOrder: []string{
				"https://r.example.com/#a",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeRegistries(entryRegistries(t, tc.project), entryRegistries(t, tc.global), tc.excludeBuiltin, tc.defaults)
			assert.Equal(t, tc.wantURLsInOrder, urls(got))
			if tc.wantFirstName != "" {
				require.NotEmpty(t, got)
				assert.Equal(t, tc.wantFirstName, got[0].Name)
			}
		})
	}
}

func TestNewClientWithRegistryOptions(t *testing.T) {
	nonEmptyGlobal := &GlobalConfig{Registries: []RegistryEntry{{URL: "https://glob.example.com"}}}
	emptyExcludeGlobal := &GlobalConfig{ExcludeBuiltinRegistries: true}
	excludeGlobal := &GlobalConfig{
		Registries:               []RegistryEntry{{URL: "https://glob.example.com"}},
		ExcludeBuiltinRegistries: true,
	}

	tests := []struct {
		name    string
		opts    []Option
		envBase string // when non-empty, sets DBC_BASE_URL for the test
		wantErr string // substring; "" means no error
		// wantRegs is the expected first-entry URLs (in order) when no error.
		// An empty slice means "any non-empty list is fine".
		wantRegs  []string
		wantCount int // 0 => any count >= 1 accepted
		// wantBuiltin, when set, asserts whether the built-in registries
		// are in the list.
		wantBuiltin *bool
	}{
		{
			name:        "WithProjectConfig merges with defaults",
			opts:        []Option{WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "https://proj.example.com"}}})},
			wantRegs:    []string{"https://proj.example.com"},
			wantCount:   0,
			wantBuiltin: new(true),
		},
		{
			name:      "WithProjectConfig + exclude_builtin_registries returns only project",
			opts:      []Option{WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "https://only.example.com"}}, ExcludeBuiltinRegistries: true})},
			wantRegs:  []string{"https://only.example.com"},
			wantCount: 1,
		},
		{
			name:      "WithGlobalConfig merges global entries",
			opts:      []Option{WithGlobalConfig(nonEmptyGlobal)},
			wantRegs:  []string{"https://glob.example.com"},
			wantCount: 0,
		},
		{
			name: "WithBaseURL overrides registry options",
			opts: []Option{
				WithBaseURL("https://only-base.example.com"),
				WithGlobalConfig(&GlobalConfig{Registries: []RegistryEntry{{URL: "https://ignored.example.com"}}}),
				WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "https://ignored2.example.com"}}}),
			},
			wantRegs:  []string{"https://only-base.example.com"},
			wantCount: 1,
		},
		{
			name: "WithRegistries wins over WithGlobalConfig and WithProjectConfig",
			opts: []Option{
				WithRegistries([]Registry{{BaseURL: mustParseURL("https://explicit.example.com")}}),
				WithGlobalConfig(nonEmptyGlobal),
				WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "https://proj.example.com"}}}),
			},
			wantRegs:  []string{"https://explicit.example.com"},
			wantCount: 1,
		},
		{
			name: "project exclude_builtin_registries=true with no project entries keeps global entries",
			opts: []Option{
				WithGlobalConfig(nonEmptyGlobal),
				WithProjectConfig(&ProjectConfig{Registries: nil, ExcludeBuiltinRegistries: true}),
			},
			wantRegs:  []string{"https://glob.example.com"},
			wantCount: 1,
		},
		{
			name: "global true + project false keeps the built-in registries excluded",
			opts: []Option{
				WithGlobalConfig(excludeGlobal),
				WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "https://proj.example.com"}}}),
			},
			wantRegs:    []string{"https://proj.example.com", "https://glob.example.com"},
			wantCount:   2,
			wantBuiltin: new(false),
		},
		{
			name: "global false + project true excludes the built-in registries",
			opts: []Option{
				WithGlobalConfig(nonEmptyGlobal),
				WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "https://proj.example.com"}}, ExcludeBuiltinRegistries: true}),
			},
			wantRegs:    []string{"https://proj.example.com", "https://glob.example.com"},
			wantCount:   2,
			wantBuiltin: new(false),
		},
		{
			name: "global false + project false keeps the built-in registries",
			opts: []Option{
				WithGlobalConfig(nonEmptyGlobal),
				WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "https://proj.example.com"}}}),
			},
			wantRegs:    []string{"https://proj.example.com", "https://glob.example.com"},
			wantBuiltin: new(true),
		},
		{
			name:    "global true + project false with no entries → empty merge rejected",
			opts:    []Option{WithGlobalConfig(emptyExcludeGlobal), WithProjectConfig(&ProjectConfig{Registries: nil})},
			wantErr: "empty registry list",
		},
		{
			name:    "global exclude_builtin_registries=true + no project entries → empty merge rejected",
			opts:    []Option{WithGlobalConfig(emptyExcludeGlobal)},
			wantErr: "empty registry list",
		},
		{
			name: "global exclude_builtin_registries=true + project supplies entries → accepted",
			opts: []Option{
				WithGlobalConfig(emptyExcludeGlobal),
				WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "https://proj.example.com"}}}),
			},
			wantRegs:  []string{"https://proj.example.com"},
			wantCount: 1,
		},
		{
			name:    "WithProjectConfig rejects empty URL",
			opts:    []Option{WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: ""}}})},
			wantErr: "empty url",
		},
		{
			name:    "WithProjectConfig rejects non-http scheme",
			opts:    []Option{WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "ftp://example.com"}}})},
			wantErr: "scheme must be http or https",
		},
		{
			name:    "WithProjectConfig rejects truly-empty merge",
			opts:    []Option{WithProjectConfig(&ProjectConfig{Registries: nil, ExcludeBuiltinRegistries: true})},
			wantErr: "empty registry list",
		},
		{
			name: "WithProjectConfig rejects a repeated name",
			opts: []Option{WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{
				{URL: "https://a.example.com", Name: "acme"},
				{URL: "https://b.example.com", Name: "acme"},
			}})},
			wantErr: "declared more than once",
		},
		{
			name: "the same name in the global config and the project is allowed",
			opts: []Option{
				WithGlobalConfig(&GlobalConfig{Registries: []RegistryEntry{{URL: "https://glob.example.com", Name: "acme"}}}),
				WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "https://proj.example.com", Name: "acme"}}}),
			},
			wantRegs: []string{"https://proj.example.com", "https://glob.example.com"},
		},
		{
			name:    "WithGlobalConfig rejects empty URL",
			opts:    []Option{WithGlobalConfig(&GlobalConfig{Registries: []RegistryEntry{{URL: ""}}})},
			wantErr: "empty url",
		},
		{
			name:    "WithGlobalConfig rejects non-http scheme",
			opts:    []Option{WithGlobalConfig(&GlobalConfig{Registries: []RegistryEntry{{URL: "ftp://example.com"}}})},
			wantErr: "scheme must be http or https",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DBC_BASE_URL", tc.envBase)
			c, err := NewClient(tc.opts...)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			regs := c.Registries()
			require.NotEmpty(t, regs)
			if tc.wantCount > 0 {
				require.Len(t, regs, tc.wantCount)
			}
			for i, want := range tc.wantRegs {
				require.Greater(t, len(regs), i)
				assert.Equal(t, want, regs[i].BaseURL.String())
			}
			if tc.wantBuiltin != nil {
				got := slices.ContainsFunc(regs, func(r Registry) bool {
					return r.BaseURL.String() == "https://dbc-cdn.columnar.tech"
				})
				assert.Equal(t, *tc.wantBuiltin, got)
			}
		})
	}
}

// TestSearchPriorityAcrossRegistryTiers pins the cross-tier driver-name
// conflict resolution rule: when registries at different tiers (project,
// global, default) all publish a driver with the same path, Client.Search
// returns the entries in registry-priority order, so first-match-wins
// semantics in cmd/dbc.findDriver and Client.Install resolve the conflict
// to the highest-priority tier.
//
// The merge function itself is exhaustively covered in TestMergeRegistries
// with arbitrary defaults; this test exercises Client.Search end-to-end
// against three httptest servers acting as the three tiers. The tier
// identity is encoded in the package URL so the resolved PkgInfo's Path
// reveals which tier won.
//
// Going through NewClient + WithGlobalConfig + WithProjectConfig would
// require the real built-in defaults (which point at production servers)
// to also be present in the merge, making this test flaky. Constructing
// the Client directly with the merged registry list is the in-package
// equivalent of "after merge succeeded".
func TestSearchPriorityAcrossRegistryTiers(t *testing.T) {
	const driverPath = "shared-driver"

	indexFor := func(packageRelURL string) string {
		platforms := []string{
			"linux_amd64", "linux_arm64",
			"macos_amd64", "macos_arm64",
			"windows_amd64", "windows_arm64",
		}
		var b strings.Builder
		fmt.Fprintf(&b, "drivers:\n  - name: Shared\n    description: shared driver\n    license: MIT\n    path: %s\n    pkginfo:\n      - version: v1.0.0\n        packages:\n", driverPath)
		for _, p := range platforms {
			fmt.Fprintf(&b, "          - platform: %s\n            url: %s\n", p, packageRelURL)
		}
		return b.String()
	}

	makeServer := func(packageRelURL string) *httptest.Server {
		body := indexFor(packageRelURL)
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/index.yaml") {
				w.Header().Set("Content-Type", "application/yaml")
				_, _ = fmt.Fprint(w, body)
				return
			}
			http.NotFound(w, r)
		}))
	}

	projectTier := makeServer("project-pkg.tar.gz")
	defer projectTier.Close()
	globalTier := makeServer("global-pkg.tar.gz")
	defer globalTier.Close()
	defaultTier := makeServer("default-pkg.tar.gz")
	defer defaultTier.Close()

	c := &Client{
		httpClient: http.DefaultClient,
		registries: []Registry{
			{Name: "tier-project", BaseURL: mustParseURL(projectTier.URL)},
			{Name: "tier-global", BaseURL: mustParseURL(globalTier.URL)},
			{Name: "tier-default", BaseURL: mustParseURL(defaultTier.URL)},
		},
	}

	drivers, err := c.Search(t.Context(), "")
	require.NoError(t, err)
	require.Len(t, drivers, 3, "each tier should contribute one driver entry")

	assert.Equal(t, projectTier.URL, drivers[0].Registry.BaseURL.String(),
		"project-tier driver must come first in Search results")
	assert.Equal(t, globalTier.URL, drivers[1].Registry.BaseURL.String(),
		"global-tier driver must come second in Search results")
	assert.Equal(t, defaultTier.URL, drivers[2].Registry.BaseURL.String(),
		"default-tier driver must come last in Search results")

	var first *Driver
	for i := range drivers {
		if drivers[i].Path == driverPath {
			first = &drivers[i]
			break
		}
	}
	require.NotNil(t, first)
	assert.Equal(t, projectTier.URL, first.Registry.BaseURL.String(),
		"first-match-wins must resolve the cross-tier name conflict to the project tier")

	pkg, err := first.GetPackage(nil, config.PlatformTuple(), false)
	require.NoError(t, err)
	require.NotNil(t, pkg.Path)
	assert.Contains(t, pkg.Path.String(), "project-pkg.tar.gz",
		"resolved package URL must come from the project tier; got %q", pkg.Path.String())
	assert.True(t, strings.HasPrefix(pkg.Path.String(), projectTier.URL),
		"resolved package URL must be rooted at the project tier; got %q", pkg.Path.String())
}

func TestResolvePackageFirstSuitable(t *testing.T) {
	decode := func(version string) Driver {
		d := Driver{
			Path:     "shared",
			Registry: &Registry{BaseURL: mustParseURL("http://registry.test")},
		}
		require.NoError(t, yaml.Unmarshal([]byte("pkginfo:\n - version: "+version+"\n   packages:\n    - platform: linux_amd64\n"), &d))
		return d
	}
	stable := func(d *Driver) (PkgInfo, error) {
		return d.GetLatestPackage("linux_amd64", false)
	}
	pkg, err := (&SigningOperation{}).ResolvePackage([]Driver{
		decode("2.0.0-beta.1"),
		decode("0.7.0"),
	}, nil, "shared", stable)
	require.NoError(t, err)
	assert.Equal(t, "0.7.0", pkg.Version.String())
	prerelease := decode("2.0.0-beta.1")
	_, err = prerelease.GetLatestPackage("linux_amd64", false)
	require.ErrorIs(t, err, ErrPackageMiss)
	pkg, err = (&SigningOperation{}).ResolvePackage([]Driver{prerelease, decode("0.7.0")}, nil, "shared", func(d *Driver) (PkgInfo, error) {
		return d.GetLatestPackage("linux_amd64", true)
	})
	require.NoError(t, err)
	assert.Equal(t, "2.0.0-beta.1", pkg.Version.String())
	pkg, err = (&SigningOperation{}).ResolvePackage([]Driver{decode("0.7.0"), decode("1.0.0")}, nil, "shared", stable)
	require.NoError(t, err)
	assert.Equal(t, "0.7.0", pkg.Version.String())
	constraint, err := semver.NewConstraint("=0.7.0")
	require.NoError(t, err)
	pkg, err = (&SigningOperation{}).ResolvePackage([]Driver{decode("1.0.0"), decode("0.7.0")}, nil, "shared", func(d *Driver) (PkgInfo, error) { return d.GetWithConstraint(constraint, "linux_amd64") })
	require.NoError(t, err)
	assert.Equal(t, "0.7.0", pkg.Version.String())
}

func TestResolvePackageEmptyPackageInfo(t *testing.T) {
	for _, packageInfo := range []string{"", "pkginfo: []\n"} {
		for _, allowPrerelease := range []bool{false, true} {
			for _, fallback := range []bool{false, true} {
				t.Run(fmt.Sprintf("pkginfo=%q/pre=%t/fallback=%t", packageInfo, allowPrerelease, fallback), func(t *testing.T) {
					empty := Driver{Path: "shared"}
					require.NoError(t, yaml.Unmarshal([]byte(packageInfo), &empty))
					drivers := []Driver{empty}
					registry := &Registry{BaseURL: mustParseURL("https://fallback.test")}
					if fallback {
						drivers = append(drivers, Driver{
							Path: "shared", Registry: registry,
							PkgInfo: []pkginfo{{
								Version:  semver.New(1, 0, 0, "", ""),
								Packages: []pkgEntry{{PlatformTuple: "linux_amd64"}},
							}},
						})
					}
					var pkg PkgInfo
					var err error
					require.NotPanics(t, func() {
						pkg, err = (&SigningOperation{}).ResolvePackage(drivers, nil, "shared", func(d *Driver) (PkgInfo, error) {
							return d.GetLatestPackage("linux_amd64", allowPrerelease)
						})
					})
					if fallback {
						require.NoError(t, err)
						assert.Same(t, registry, pkg.Driver.Registry)
						assert.Equal(t, "1.0.0", pkg.Version.String())
					} else {
						require.EqualError(t, err, "driver `shared` not found")
						assert.ErrorIs(t, err, ErrPackageMiss)
						assert.ErrorIs(t, fmt.Errorf("wrapped: %w", err), ErrPackageMiss)
					}
				})
			}
		}
	}
}

func TestResolvePackageHostCompatibility(t *testing.T) {
	for _, packages := range []string{"[]", "[{platform: macos_arm64}]"} {
		t.Run(packages, func(t *testing.T) {
			first := Driver{Path: "shared", Registry: &Registry{BaseURL: mustParseURL("https://first.test")}}
			require.NoError(t, yaml.Unmarshal([]byte("pkginfo:\n - version: 2.0.0\n   packages: "+packages+"\n - version: 1.0.0\n   packages: [{platform: linux_amd64}]\n"), &first))
			fallback := Driver{Path: "shared", Registry: &Registry{BaseURL: mustParseURL("https://fallback.test")}}
			require.NoError(t, yaml.Unmarshal([]byte("pkginfo: [{version: 2.0.0, packages: [{platform: linux_amd64}]}]"), &fallback))
			drivers := []Driver{first, fallback}
			c, err := semver.NewConstraint(">=1.0.0")
			require.NoError(t, err)

			for _, selectPackage := range []func(*Driver) (PkgInfo, error){
				func(d *Driver) (PkgInfo, error) { return d.GetLatestPackage("linux_amd64", false) },
				func(d *Driver) (PkgInfo, error) { return d.GetWithConstraint(c, "linux_amd64") },
			} {
				pkg, err := (&SigningOperation{}).ResolvePackage(drivers, nil, "shared", selectPackage)
				require.NoError(t, err)
				assert.Equal(t, "1.0.0", pkg.Version.String())
				assert.Same(t, first.Registry, pkg.Driver.Registry)
			}

			latest, err := first.GetLatestVersionAnyPlatform(c, false)
			require.NoError(t, err)
			assert.Equal(t, "2.0.0", latest.String())
			exact, err := semver.NewConstraint("=2.0.0")
			require.NoError(t, err)
			pkg, err := (&SigningOperation{}).ResolvePackage(drivers, nil, "shared", func(d *Driver) (PkgInfo, error) {
				return d.GetWithConstraint(exact, "linux_amd64")
			})
			require.EqualError(t, err, "no package found for platform 'linux_amd64'")
			assert.NotErrorIs(t, err, ErrPackageMiss)
			assert.Same(t, first.Registry, pkg.Driver.Registry)
		})
	}
}

func TestResolvePackageMissingVersion(t *testing.T) {
	first := Driver{Path: "shared", Registry: &Registry{BaseURL: mustParseURL("https://first.test")}}
	require.NoError(t, yaml.Unmarshal([]byte("pkginfo: [{}]"), &first))
	fallback := Driver{Path: "shared", Registry: &Registry{BaseURL: mustParseURL("https://fallback.test")}}
	require.NoError(t, yaml.Unmarshal([]byte("pkginfo: [{version: 1.0.0, packages: [{platform: linux_amd64}]}]"), &fallback))
	const platform = "linux_amd64"
	c, err := semver.NewConstraint(">=1.0.0")
	require.NoError(t, err)
	for name, selectPackage := range map[string]func(*Driver) (PkgInfo, error){
		"stable":      func(d *Driver) (PkgInfo, error) { return d.GetLatestPackage(platform, false) },
		"prerelease":  func(d *Driver) (PkgInfo, error) { return d.GetLatestPackage(platform, true) },
		"constrained": func(d *Driver) (PkgInfo, error) { return d.GetWithConstraint(c, platform) },
		"exact": func(d *Driver) (PkgInfo, error) {
			return d.GetPackage(semver.MustParse("1.0.0"), platform, true)
		},
		"project": func(d *Driver) (PkgInfo, error) {
			version, err := d.GetLatestVersionAnyPlatform(nil, true)
			return PkgInfo{Version: version}, err
		},
	} {
		t.Run(name, func(t *testing.T) {
			pkg, err := (&SigningOperation{}).ResolvePackage([]Driver{first, fallback}, nil, "shared", selectPackage)
			require.EqualError(t, err, "invalid package metadata for driver shared: missing version")
			assert.NotErrorIs(t, err, ErrPackageMiss)
			assert.Same(t, first.Registry, pkg.Driver.Registry)
		})
	}
}

func TestSearchPrefersTheConfiguredRegistryName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "name: from-index\ndrivers:\n  - path: x\n    name: X\n")
	}))
	t.Cleanup(server.Close)

	for _, tc := range []struct {
		configured, want string
	}{
		{configured: "from-config", want: "from-config"},
		{configured: "", want: "from-index"},
	} {
		c, err := NewClient(WithRegistries([]Registry{{Name: tc.configured, BaseURL: mustParseURL(server.URL)}}))
		require.NoError(t, err)
		drivers, err := c.Search(t.Context(), "")
		require.NoError(t, err)
		require.Len(t, drivers, 1)
		assert.Equal(t, tc.want, drivers[0].Registry.Name)
	}
}

// identities adapts a signer function for tests whose signer names are both
// compared and shown.
func identities(f func(*Registry) (string, error)) func(*Registry) (signerIdentity, error) {
	return func(r *Registry) (signerIdentity, error) {
		s, err := f(r)
		return signerIdentity{key: s, label: s}, err
	}
}

func TestResolvePackageRequiresTheSameSigner(t *testing.T) {
	first := &Registry{Name: "first", BaseURL: mustParseURL("https://user:secret@first.test/r?token=t")}
	second := &Registry{BaseURL: mustParseURL("https://second.test")}
	third := &Registry{BaseURL: mustParseURL("https://third.test")}
	driver := func(r *Registry, versions ...string) Driver {
		d := Driver{Path: "shared", Registry: r}
		for _, v := range versions {
			d.PkgInfo = append(d.PkgInfo, pkginfo{Version: semver.MustParse(v), Packages: []pkgEntry{{PlatformTuple: "linux_amd64"}}})
		}
		return d
	}
	signers := map[*Registry]string{first: "key A", second: "key A", third: "key B"}
	signerOf := identities(func(r *Registry) (string, error) { return signers[r], nil })
	latest := func(d *Driver) (PkgInfo, error) { return d.GetLatestPackage("linux_amd64", false) }

	// The same signer: the #584 fallback from a registry without a matching
	// version to the next one still works.
	pkg, err := resolvePackage([]Driver{driver(first), driver(second, "1.0.0"), {Path: "other", Registry: third}}, nil, "shared", latest, signerOf)
	require.NoError(t, err)
	assert.Same(t, second, pkg.Driver.Registry)

	// Different signers conflict even when only one registry has a version,
	// and before any package is selected.
	selected := false
	_, err = resolvePackage([]Driver{driver(first, "1.0.0"), driver(third)}, nil, "shared", func(d *Driver) (PkgInfo, error) {
		selected = true
		return latest(d)
	}, signerOf)
	var conflict *SignerConflictError
	require.ErrorAs(t, err, &conflict)
	assert.False(t, selected)
	assert.Equal(t, []*Registry{first, third}, conflict.Registries)
	assert.Equal(t, []string{"key A", "key B"}, conflict.Signers)
	assert.Contains(t, err.Error(), "https://first.test/r [first] (key A), https://third.test (key B)")
	assert.NotContains(t, err.Error(), "secret")
	assert.NotContains(t, err.Error(), "token")

	// A failure to identify a signer is an error, but a single registry
	// without failed ones is not compared, so its key is not read.
	unreadable := identities(func(*Registry) (string, error) { return "", errors.New("cannot read the key") })
	_, err = resolvePackage([]Driver{driver(first, "1.0.0"), driver(second)}, nil, "shared", latest, unreadable)
	assert.ErrorContains(t, err, "cannot read the key")
	pkg, err = resolvePackage([]Driver{driver(first, "1.0.0")}, nil, "shared", latest, unreadable)
	require.NoError(t, err)
	assert.Same(t, first, pkg.Driver.Registry)

	// The built-in identity is the same for every registry.
	pkg, err = (&SigningOperation{}).ResolvePackage([]Driver{driver(first), driver(third, "1.0.0")}, nil, "shared", latest)
	require.NoError(t, err)
	assert.Same(t, third, pkg.Driver.Registry)
}

// TestResolvePackageMissingIndex pins that a missing index stops resolution
// only when its registry might list the driver with another signer.
func TestResolvePackageMissingIndex(t *testing.T) {
	public := &Registry{BaseURL: mustParseURL("https://public.test")}
	private := &Registry{BaseURL: mustParseURL("https://user:secret@private.test/r?token=t")}
	sameKey := &Registry{BaseURL: mustParseURL("https://same.test")}
	otherKey := &Registry{BaseURL: mustParseURL("https://other.test")}
	signers := map[*Registry]string{public: "key A", private: "key A", sameKey: "key A", otherKey: "key B"}
	signerOf := identities(func(r *Registry) (string, error) { return signers[r], nil })
	listed := Driver{Path: "shared", Registry: public,
		PkgInfo: []pkginfo{{Version: semver.MustParse("1.0.0"), Packages: []pkgEntry{{PlatformTuple: "linux_amd64"}}}}}
	latest := func(d *Driver) (PkgInfo, error) { return d.GetLatestPackage("linux_amd64", false) }
	failed := func(r *Registry) error { return &RegistryIndexError{Registry: r, Err: ErrUnauthorized} }

	// The built-in registries share a signer: a failed private registry,
	// even ranked above the public one, does not block a public driver.
	pkg, err := (&SigningOperation{}).ResolvePackage([]Driver{listed}, errors.Join(failed(private)), "shared", latest)
	require.NoError(t, err)
	assert.Same(t, public, pkg.Driver.Registry)

	// The failure may come wrapped, as the CLI redacts it.
	pkg, err = resolvePackage([]Driver{listed}, fmt.Errorf("redacted: %w", errors.Join(failed(private), failed(sameKey))), "shared", latest, signerOf)
	require.NoError(t, err)
	assert.Same(t, public, pkg.Driver.Registry)

	// Without a listing registry, failed registries with one signer leave
	// the driver not found.
	_, err = resolvePackage(nil, errors.Join(failed(private), failed(sameKey)), "shared", latest, signerOf)
	require.ErrorContains(t, err, "not found")
	assert.NotErrorIs(t, err, ErrIncompleteIndex)

	noSelection := func(d *Driver) (PkgInfo, error) {
		t.Fatal("no package may be selected while a registry with another signer is missing")
		return PkgInfo{}, nil
	}
	for _, tc := range []struct {
		name     string
		drivers  []Driver
		indexErr error
		signerOf func(*Registry) (signerIdentity, error)
		want     string
	}{
		{name: "another signer", drivers: []Driver{listed}, indexErr: errors.Join(failed(otherKey))},
		{name: "another signer in a later failure", drivers: []Driver{listed}, indexErr: errors.Join(failed(sameKey), failed(otherKey))},
		{name: "failed registries with different signers", indexErr: errors.Join(failed(private), failed(otherKey))},
		{name: "a failure without a registry", drivers: []Driver{listed}, indexErr: errors.New("network error")},
		{name: "a registry index error without a registry", drivers: []Driver{listed}, indexErr: &RegistryIndexError{Err: ErrUnauthorized}},
		{name: "an unknown signer", drivers: []Driver{listed}, indexErr: errors.Join(failed(private)), want: "cannot read the key",
			signerOf: func(r *Registry) (signerIdentity, error) {
				if r == private {
					return signerIdentity{}, errors.New("cannot read the key")
				}
				return signerOf(r)
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.signerOf == nil {
				tc.signerOf = signerOf
			}
			_, err := resolvePackage(tc.drivers, tc.indexErr, "shared", noSelection, tc.signerOf)
			require.ErrorIs(t, err, ErrIncompleteIndex)
			assert.ErrorContains(t, err, tc.want)
			assert.NotContains(t, err.Error(), "secret")
			assert.NotContains(t, err.Error(), "token")
		})
	}

	// The failure of each registry stays available.
	_, err = resolvePackage([]Driver{listed}, errors.Join(failed(otherKey)), "shared", noSelection, signerOf)
	require.ErrorIs(t, err, ErrUnauthorized)
	var indexError *RegistryIndexError
	require.ErrorAs(t, err, &indexError)
	assert.Same(t, otherKey, indexError.Registry)
}

func TestSearchAndInstallReportEachFailedRegistry(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "drivers:\n  - path: x\n    name: X\n")
	}))
	t.Cleanup(ok.Close)
	for _, status := range []int{http.StatusNotFound, http.StatusGone, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			t.Cleanup(failing.Close)
			c, err := NewClient(WithRegistries([]Registry{
				{BaseURL: mustParseURL(ok.URL)},
				{BaseURL: mustParseURL(failing.URL)},
			}))
			require.NoError(t, err)

			drivers, err := c.Search(t.Context(), "")
			assert.Len(t, drivers, 1, "search keeps the partial result")
			var indexErr *RegistryIndexError
			require.ErrorAs(t, err, &indexErr)
			assert.Equal(t, failing.URL, indexErr.Registry.BaseURL.String())

			// Both registries use the same signer, so the failed one does
			// not stop the resolution of a driver the other one lists: x
			// fails only for having no package.
			_, err = c.Install(t.Context(), config.Config{}, "x")
			require.Error(t, err)
			assert.NotErrorIs(t, err, ErrIncompleteIndex)

			// A driver that no reachable registry lists names the failure.
			_, err = c.Install(t.Context(), config.Config{}, "missing")
			assert.ErrorContains(t, err, failing.URL)
		})
	}
}

// TestRegistryIndexErrorHidesURLSecrets pins that a failed registry's
// credentials do not appear in the error, also where the HTTP client repeats
// the request URL.
func TestRegistryIndexErrorHidesURLSecrets(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	unreachable := "http://user:hunter22@" + server.Listener.Addr().String() + "/r?token=supersecret"
	server.Close()

	c, err := NewClient(WithRegistries([]Registry{{BaseURL: mustParseURL(unreachable)}}))
	require.NoError(t, err)
	_, err = c.Search(t.Context(), "")
	require.Error(t, err)
	var indexErr *RegistryIndexError
	require.ErrorAs(t, err, &indexErr)
	assert.Contains(t, indexErr.Unwrap().Error(), "supersecret", "the cause itself is kept for errors.Is and errors.As")
	for _, secret := range []string{"supersecret", "hunter22", "user:"} {
		assert.NotContains(t, err.Error(), secret)
	}
	assert.Contains(t, err.Error(), "/r")

	_, err = c.Install(t.Context(), config.Config{}, "x")
	require.ErrorAs(t, err, &indexErr)
	assert.NotContains(t, err.Error(), "supersecret")
}
