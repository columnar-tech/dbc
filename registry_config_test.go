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

func TestLoadProjectConfig(t *testing.T) {
	for _, tc := range []struct {
		name        string
		toml        string
		wantEntries []RegistryEntry
		wantReplace *bool
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
		{name: "empty project inherits replacement", toml: ""},
		{name: "explicit false", toml: "replace_defaults = false\n", wantReplace: new(false)},
		{name: "true without registries is resolved after merging", toml: "replace_defaults = true\n", wantReplace: new(true)},
		{name: "malformed TOML", toml: "[[registries", wantErr: "invalid"},
		{name: "wrong replacement type", toml: "replace_defaults = \"false\"\n", wantErr: "invalid"},
		{name: "empty registry URL", toml: "[[registries]]\nurl = \"\"\n", wantErr: "empty url"},
		{name: "malformed registry URL", toml: "[[registries]]\nurl = \"http://bad host\"\n", wantErr: "invalid registry URL"},
		{name: "missing host", toml: "[[registries]]\nurl = \"https:///onlypath\"\n", wantErr: "missing host"},
		{name: "unsupported registry scheme", toml: "[[registries]]\nurl = \"ftp://example.test\"\n", wantErr: "scheme must be http or https"},
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
			assert.Equal(t, tc.wantReplace, cfg.ReplaceDefaults)
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
	registries, err := registriesFromEntries(entries, nil, "", false)
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
			projectReplace: new(true),
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
			projectReplace: new(false),
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
			projectReplace: new(true),
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
			projectReplace: new(true),
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
			projectReplace: new(true),
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
			projectReplace: new(true),
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
			projectReplace: new(true),
			wantURLsInOrder: []string{
				"https://r.example.com/#a",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeRegistries(entryRegistries(t, tc.project), tc.projectReplace, entryRegistries(t, tc.global), tc.globalReplace, tc.defaults)
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
			name:      "WithProjectConfig merges with defaults",
			opts:      []Option{WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "https://proj.example.com"}}, ReplaceDefaults: nil})},
			wantRegs:  []string{"https://proj.example.com"},
			wantCount: 0,
		},
		{
			name:      "WithProjectConfig + replace_defaults returns only project",
			opts:      []Option{WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "https://only.example.com"}}, ReplaceDefaults: new(true)})},
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
				WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "https://ignored2.example.com"}}, ReplaceDefaults: nil}),
			},
			wantRegs:  []string{"https://only-base.example.com"},
			wantCount: 1,
		},
		{
			name: "WithRegistries wins over WithGlobalConfig and WithProjectConfig",
			opts: []Option{
				WithRegistries([]Registry{{BaseURL: mustParseURL("https://explicit.example.com")}}),
				WithGlobalConfig(nonEmptyGlobal),
				WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "https://proj.example.com"}}, ReplaceDefaults: nil}),
			},
			wantRegs:  []string{"https://explicit.example.com"},
			wantCount: 1,
		},
		{
			name: "project replace_defaults=true with no project entries keeps global entries",
			opts: []Option{
				WithGlobalConfig(nonEmptyGlobal),
				WithProjectConfig(&ProjectConfig{Registries: nil, ReplaceDefaults: new(true)}),
			},
			wantRegs:  []string{"https://glob.example.com"},
			wantCount: 1,
		},
		{
			name: "project replace_defaults=false overrides global true; defaults restored",
			opts: []Option{
				WithGlobalConfig(emptyReplaceAllGlobal),
				WithProjectConfig(&ProjectConfig{Registries: nil, ReplaceDefaults: new(false)}),
			},
			// Only assert non-empty — defaults content can change.
		},
		{
			name:    "global replace_defaults=true + no project entries → empty merge rejected",
			opts:    []Option{WithGlobalConfig(emptyReplaceAllGlobal)},
			wantErr: "empty registry list",
		},
		{
			name: "global replace_defaults=true + project supplies entries → accepted",
			opts: []Option{
				WithGlobalConfig(emptyReplaceAllGlobal),
				WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "https://proj.example.com"}}, ReplaceDefaults: nil}),
			},
			wantRegs:  []string{"https://proj.example.com"},
			wantCount: 1,
		},
		{
			name:    "WithProjectConfig rejects empty URL",
			opts:    []Option{WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: ""}}, ReplaceDefaults: nil})},
			wantErr: "empty url",
		},
		{
			name:    "WithProjectConfig rejects non-http scheme",
			opts:    []Option{WithProjectConfig(&ProjectConfig{Registries: []RegistryEntry{{URL: "ftp://example.com"}}, ReplaceDefaults: nil})},
			wantErr: "scheme must be http or https",
		},
		{
			name:    "WithProjectConfig rejects truly-empty merge",
			opts:    []Option{WithProjectConfig(&ProjectConfig{Registries: nil, ReplaceDefaults: new(true)})},
			wantErr: "empty registry list",
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
	pkg, err := ResolvePackage([]Driver{
		decode("2.0.0-beta.1"),
		decode("0.7.0"),
	}, nil, "shared", stable)
	require.NoError(t, err)
	assert.Equal(t, "0.7.0", pkg.Version.String())
	prerelease := decode("2.0.0-beta.1")
	_, err = prerelease.GetLatestPackage("linux_amd64", false)
	require.ErrorIs(t, err, ErrPackageMiss)
	pkg, err = ResolvePackage([]Driver{prerelease, decode("0.7.0")}, nil, "shared", func(d *Driver) (PkgInfo, error) {
		return d.GetLatestPackage("linux_amd64", true)
	})
	require.NoError(t, err)
	assert.Equal(t, "2.0.0-beta.1", pkg.Version.String())
	pkg, err = ResolvePackage([]Driver{decode("0.7.0"), decode("1.0.0")}, nil, "shared", stable)
	require.NoError(t, err)
	assert.Equal(t, "0.7.0", pkg.Version.String())
	constraint, err := semver.NewConstraint("=0.7.0")
	require.NoError(t, err)
	pkg, err = ResolvePackage([]Driver{decode("1.0.0"), decode("0.7.0")}, nil, "shared", func(d *Driver) (PkgInfo, error) { return d.GetWithConstraint(constraint, "linux_amd64") })
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
						pkg, err = ResolvePackage(drivers, nil, "shared", func(d *Driver) (PkgInfo, error) {
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
				pkg, err := ResolvePackage(drivers, nil, "shared", selectPackage)
				require.NoError(t, err)
				assert.Equal(t, "1.0.0", pkg.Version.String())
				assert.Same(t, first.Registry, pkg.Driver.Registry)
			}

			latest, err := first.GetLatestVersionAnyPlatform(c, false)
			require.NoError(t, err)
			assert.Equal(t, "2.0.0", latest.String())
			exact, err := semver.NewConstraint("=2.0.0")
			require.NoError(t, err)
			pkg, err := ResolvePackage(drivers, nil, "shared", func(d *Driver) (PkgInfo, error) {
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
			pkg, err := ResolvePackage([]Driver{first, fallback}, nil, "shared", selectPackage)
			require.EqualError(t, err, "invalid package metadata for driver shared: missing version")
			assert.NotErrorIs(t, err, ErrPackageMiss)
			assert.Same(t, first.Registry, pkg.Driver.Registry)
		})
	}
}

// A registry with its own key or unsigned cannot provide a driver name that a
// registry verified with the Columnar key serves. A copy of the Columnar key
// counts as the Columnar key.
func TestResolvePackageDriverNameScope(t *testing.T) {
	inlineColumnar, err := NewArmoredSigningKey(armoredPubKey)
	require.NoError(t, err)
	other, err := NewArmoredSigningKey(publicArmored(t, generateTestKey(t, "acme")))
	require.NoError(t, err)
	driver := func(name string, signing RegistrySigningPolicy, host string) Driver {
		d := Driver{Path: name, Registry: &Registry{BaseURL: mustParseURL("https://" + host), Signing: signing}}
		require.NoError(t, yaml.Unmarshal([]byte("pkginfo:\n - version: 1.0.0\n   packages:\n    - platform: linux_amd64\n"), &d))
		return d
	}
	stable := func(d *Driver) (PkgInfo, error) { return d.GetLatestPackage("linux_amd64", false) }
	columnarIndexDown := fmt.Errorf("wrapped: %w", errors.Join(&RegistryIndexError{Registry: Registry{BaseURL: mustParseURL("https://columnar.test")}, Err: errors.New("503")}))

	tests := []struct {
		name      string
		drivers   []Driver
		searchErr error
		conflict  bool
	}{
		{"own key over a Columnar name", []Driver{driver("x", SignWith(other), "acme.test"), driver("x", RegistrySigningPolicy{}, "columnar.test")}, nil, true},
		{"unsigned over a Columnar name", []Driver{driver("x", Unsigned(), "acme.test"), driver("x", RegistrySigningPolicy{}, "columnar.test")}, nil, true},
		{"own key, other names", []Driver{driver("x", SignWith(other), "acme.test"), driver("y", RegistrySigningPolicy{}, "columnar.test")}, nil, false},
		{"inline Columnar key", []Driver{driver("x", SignWith(inlineColumnar), "mirror.test"), driver("x", RegistrySigningPolicy{}, "columnar.test")}, nil, false},
		{"Columnar registry first", []Driver{driver("x", RegistrySigningPolicy{}, "columnar.test"), driver("x", SignWith(other), "acme.test")}, nil, false},
		{"Columnar index down", []Driver{driver("x", SignWith(other), "acme.test")}, columnarIndexDown, true},
		{"Columnar index down, Columnar selected", []Driver{driver("x", RegistrySigningPolicy{}, "mirror.test")}, columnarIndexDown, false},
		{"other index down", []Driver{driver("x", SignWith(other), "acme.test")}, &RegistryIndexError{Registry: Registry{BaseURL: mustParseURL("https://u.test"), Signing: Unsigned()}, Err: errors.New("503")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkg, err := ResolvePackage(tt.drivers, tt.searchErr, "x", stable)
			if tt.conflict {
				assert.ErrorIs(t, err, ErrDriverNameConflict)
				assert.Equal(t, "x", pkg.Driver.Path)
				return
			}
			require.NoError(t, err)
			assert.Same(t, tt.drivers[0].Registry, pkg.Driver.Registry)
		})
	}
}
