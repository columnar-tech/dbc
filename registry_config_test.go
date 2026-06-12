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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/columnar-tech/dbc/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool { return &b }

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
		wantReplace bool
	}{
		{
			name: "two registries, no replace_defaults",
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
			name: "replace_defaults true with one registry",
			toml: `
replace_defaults = true

[[registries]]
url = "https://custom.registry.io"
`,
			wantEntries: []RegistryEntry{{URL: "https://custom.registry.io"}},
			wantReplace: true,
		},
		{
			name:     "missing config.toml returns nil,nil",
			omitFile: true,
			wantNil:  true,
		},
		{
			name:        "replace_defaults=true with no entries accepted (project may supply)",
			toml:        "replace_defaults = true\n",
			wantReplace: true,
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
			assert.Equal(t, tc.wantReplace, cfg.ReplaceDefaults)
		})
	}
}

func TestMergeRegistries(t *testing.T) {
	defaults := []Registry{
		{BaseURL: mustParseURL("https://default-a.example.com")},
		{BaseURL: mustParseURL("https://default-b.example.com")},
	}

	tests := []struct {
		name            string
		project         []RegistryEntry
		projectReplace  *bool
		global          []RegistryEntry
		globalReplace   bool
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
			name:           "project replace_defaults drops defaults",
			project:        []RegistryEntry{{URL: "https://proj.example.com"}},
			projectReplace: boolPtr(true),
			global:         []RegistryEntry{{URL: "https://glob.example.com"}},
			defaults:       defaults,
			wantURLsInOrder: []string{
				"https://proj.example.com",
				"https://glob.example.com",
			},
		},
		{
			name:          "global replace_defaults honored when project nil",
			global:        []RegistryEntry{{URL: "https://only.example.com"}},
			globalReplace: true,
			defaults:      defaults,
			wantURLsInOrder: []string{
				"https://only.example.com",
			},
		},
		{
			name:           "project replace_defaults=false overrides global true",
			projectReplace: boolPtr(false),
			global:         []RegistryEntry{{URL: "https://g.example.com"}},
			globalReplace:  true,
			defaults:       defaults,
			wantURLsInOrder: []string{
				"https://g.example.com",
				"https://default-a.example.com",
				"https://default-b.example.com",
			},
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
			name:    "project URL collides with default URL — project wins, default URL dedup'd",
			project: []RegistryEntry{{URL: "https://default-a.example.com", Name: "project-name"}},
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
			name:    "project URL matches second default — project wins, only that default URL dedup'd",
			project: []RegistryEntry{{URL: "https://default-b.example.com", Name: "project-shadows-b"}},
			global:  []RegistryEntry{{URL: "https://glob.example.com"}},
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
			projectReplace: boolPtr(true),
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
			projectReplace: boolPtr(true),
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
			projectReplace: boolPtr(true),
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
			projectReplace: boolPtr(true),
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
			projectReplace: boolPtr(true),
			wantURLsInOrder: []string{
				"https://r.example.com/#a",
			},
		},
		{
			name: "invalid entries silently skipped",
			project: []RegistryEntry{
				{URL: "not a url"},
				{URL: "ftp://example.com"},
				{URL: "https://good.example.com"},
			},
			defaults: defaults,
			wantURLsInOrder: []string{
				"https://good.example.com",
				"https://default-a.example.com",
				"https://default-b.example.com",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeRegistries(tc.project, tc.projectReplace, tc.global, tc.globalReplace, tc.defaults)
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
	emptyReplaceAllGlobal := &GlobalConfig{ReplaceDefaults: true}

	tests := []struct {
		name    string
		opts    []Option
		envBase string // when non-empty, sets DBC_BASE_URL for the test
		wantErr string // substring; "" means no error
		// wantRegs is the expected first-entry URLs (in order) when no error.
		// An empty slice means "any non-empty list is fine".
		wantRegs  []string
		wantCount int // 0 => any count >= 1 accepted
	}{
		{
			name:      "WithProjectRegistries merges with defaults",
			opts:      []Option{WithProjectRegistries([]RegistryEntry{{URL: "https://proj.example.com"}}, nil)},
			wantRegs:  []string{"https://proj.example.com"},
			wantCount: 0,
		},
		{
			name:      "WithProjectRegistries + replace_defaults returns only project",
			opts:      []Option{WithProjectRegistries([]RegistryEntry{{URL: "https://only.example.com"}}, boolPtr(true))},
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
				WithProjectRegistries([]RegistryEntry{{URL: "https://ignored2.example.com"}}, nil),
			},
			wantRegs:  []string{"https://only-base.example.com"},
			wantCount: 1,
		},
		{
			name: "WithRegistries wins over WithGlobalConfig and WithProjectRegistries",
			opts: []Option{
				WithRegistries([]Registry{{BaseURL: mustParseURL("https://explicit.example.com")}}),
				WithGlobalConfig(nonEmptyGlobal),
				WithProjectRegistries([]RegistryEntry{{URL: "https://proj.example.com"}}, nil),
			},
			wantRegs:  []string{"https://explicit.example.com"},
			wantCount: 1,
		},
		{
			name: "project replace_defaults=true with no project entries keeps global entries",
			opts: []Option{
				WithGlobalConfig(nonEmptyGlobal),
				WithProjectRegistries(nil, boolPtr(true)),
			},
			wantRegs:  []string{"https://glob.example.com"},
			wantCount: 1,
		},
		{
			name: "project replace_defaults=false overrides global true; defaults restored",
			opts: []Option{
				WithGlobalConfig(emptyReplaceAllGlobal),
				WithProjectRegistries(nil, boolPtr(false)),
			},
			// Only assert non-empty — defaults content can change.
		},
		{
			name: "global replace_defaults=true + no project entries → empty merge rejected",
			opts: []Option{WithGlobalConfig(emptyReplaceAllGlobal)},
			wantErr: "empty registry list",
		},
		{
			name: "global replace_defaults=true + project supplies entries → accepted",
			opts: []Option{
				WithGlobalConfig(emptyReplaceAllGlobal),
				WithProjectRegistries([]RegistryEntry{{URL: "https://proj.example.com"}}, nil),
			},
			wantRegs:  []string{"https://proj.example.com"},
			wantCount: 1,
		},
		{
			name: "WithProjectRegistries rejects empty URL",
			opts: []Option{WithProjectRegistries([]RegistryEntry{{URL: ""}}, nil)},
			wantErr: "empty url",
		},
		{
			name: "WithProjectRegistries rejects non-http scheme",
			opts: []Option{WithProjectRegistries([]RegistryEntry{{URL: "ftp://example.com"}}, nil)},
			wantErr: "scheme must be http or https",
		},
		{
			name: "WithProjectRegistries rejects truly-empty merge",
			opts: []Option{WithProjectRegistries(nil, boolPtr(true))},
			wantErr: "empty registry list",
		},
		{
			name: "WithGlobalConfig rejects empty URL",
			opts: []Option{WithGlobalConfig(&GlobalConfig{Registries: []RegistryEntry{{URL: ""}}})},
			wantErr: "empty url",
		},
		{
			name: "WithGlobalConfig rejects non-http scheme",
			opts: []Option{WithGlobalConfig(&GlobalConfig{Registries: []RegistryEntry{{URL: "ftp://example.com"}}})},
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
// Going through NewClient + WithGlobalConfig + WithProjectRegistries would
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
