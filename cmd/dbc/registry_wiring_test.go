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
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyProjectRegistriesRoutesLookup proves that a project dbc.toml with
// [[registries]] changes where the real client fetches driver indexes — i.e.
// applyProjectRegistries actually rewires the lookup path, not just a package
// global. This intentionally bypasses the test harness's getDriverRegistry
// override so the assertions fail if add/sync stop using dbcClient.
func TestApplyProjectRegistriesRoutesLookup(t *testing.T) {
	indexYAML := `drivers:
  - name: Custom Driver
    description: served by the project-config test server
    license: MIT
    path: custom-driver
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            url: custom-driver/1.0.0/custom-driver.tar.gz
`

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/index.yaml") {
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "application/yaml")
			w.Write([]byte(indexYAML))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	// DBC_BASE_URL short-circuits registry merging. Unset it so the project
	// registry path is exercised.
	t.Setenv("DBC_BASE_URL", "")

	savedClient, savedGetter := dbcClient, getDriverRegistry
	t.Cleanup(func() {
		dbcClient = savedClient
		getDriverRegistry = savedGetter
	})

	// Rewire getDriverRegistry to the real dbcClient.Search path so the
	// assertion actually exercises the client built by applyProjectRegistries
	// instead of the suite-wide getTestDriverRegistry stub.
	getDriverRegistry = func() ([]dbc.Driver, error) {
		require.NotNil(t, dbcClient, "applyProjectRegistries must have built a client")
		return dbcClient.Search("")
	}

	// Seed a client with empty-but-valid baseline state.
	initial, err := dbc.NewClient(dbc.WithBaseURL("https://unreachable.example.invalid"))
	require.NoError(t, err)
	dbcClient = initial

	replace := true
	list := DriversList{
		Registries:      []dbc.RegistryEntry{{URL: server.URL, Name: "test"}},
		ReplaceDefaults: &replace,
	}
	require.NoError(t, applyProjectRegistries(list))
	require.NotSame(t, initial, dbcClient, "expected applyProjectRegistries to swap the client")
	require.NotEmpty(t, dbcClient.Registries())
	assert.Equal(t, server.URL, dbcClient.Registries()[0].BaseURL.String())

	drivers, err := getDriverRegistry()
	require.NoError(t, err)
	require.Len(t, drivers, 1)
	assert.Equal(t, "custom-driver", drivers[0].Path)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&hits), int32(1),
		"the test registry server should have been hit by the rebuilt client")
}

// TestApplyProjectRegistriesReplaceDefaultsFalseOverridesGlobal proves that a
// project dbc.toml with explicit replace_defaults=false overrides a global
// config's replace_defaults=true — the tri-state the reviewer flagged.
func TestApplyProjectRegistriesReplaceDefaultsFalseOverridesGlobal(t *testing.T) {
	t.Setenv("DBC_BASE_URL", "")

	savedGlobal, savedClient := globalRegistryConfig, dbcClient
	t.Cleanup(func() {
		globalRegistryConfig = savedGlobal
		dbcClient = savedClient
	})

	// Global config asks to replace defaults, providing one entry.
	globalRegistryConfig = &dbc.GlobalConfig{
		Registries:      []dbc.RegistryEntry{{URL: "https://global.example.com"}},
		ReplaceDefaults: true,
	}

	// Project config explicitly sets replace_defaults=false and supplies no
	// entries of its own — the built-in defaults MUST come back.
	replace := false
	list := DriversList{ReplaceDefaults: &replace}
	require.NoError(t, applyProjectRegistries(list))
	require.NotNil(t, dbcClient)

	regs := dbcClient.Registries()
	// Expect at least: one global + the two built-in defaults = 3 entries.
	assert.GreaterOrEqual(t, len(regs), 3, "defaults should be restored when project replace_defaults=false")
	var urls []string
	for _, r := range regs {
		if r.BaseURL != nil {
			urls = append(urls, r.BaseURL.String())
		}
	}
	assert.Contains(t, urls, "https://global.example.com")
	assert.Contains(t, urls, "https://dbc-cdn.columnar.tech")
}

// Guard: DBC_BASE_URL must still short-circuit project registry merging,
// otherwise users relying on the env var for local dev would be overridden by
// a dbc.toml section.
func TestApplyProjectRegistriesNoopWhenBaseURLSet(t *testing.T) {
	t.Setenv("DBC_BASE_URL", "https://envvar.example.com")

	savedClient := dbcClient
	t.Cleanup(func() { dbcClient = savedClient })

	// newDBCClient honors DBC_BASE_URL and returns a client with just that URL.
	c, err := newDBCClient(nil, nil)
	require.NoError(t, err)
	dbcClient = c

	replace := true
	list := DriversList{
		Registries:      []dbc.RegistryEntry{{URL: "https://should-be-ignored.example.com"}},
		ReplaceDefaults: &replace,
	}
	require.NoError(t, applyProjectRegistries(list))

	regs := dbcClient.Registries()
	require.Len(t, regs, 1)
	assert.Equal(t, "https://envvar.example.com", regs[0].BaseURL.String())
}

// TestGlobalReplaceDefaultsWithProjectSuppliedEntries exercises the CLI path
// where the global config.toml declares replace_defaults=true with no entries
// of its own, and the project dbc.toml supplies the registries. This used to
// be rejected by LoadGlobalConfig, which made the CLI drop the global config
// and silently diverge from the library's NewClient semantics.
func TestGlobalReplaceDefaultsWithProjectSuppliedEntries(t *testing.T) {
	t.Setenv("DBC_BASE_URL", "")

	savedGlobal, savedClient := globalRegistryConfig, dbcClient
	t.Cleanup(func() {
		globalRegistryConfig = savedGlobal
		dbcClient = savedClient
	})

	globalRegistryConfig = &dbc.GlobalConfig{ReplaceDefaults: true}

	list := DriversList{
		Registries: []dbc.RegistryEntry{{URL: "https://proj.example.com"}},
	}
	require.NoError(t, applyProjectRegistries(list))
	require.NotNil(t, dbcClient)

	regs := dbcClient.Registries()
	require.Len(t, regs, 1, "global replace_defaults drops built-in defaults; only project entry remains")
	assert.Equal(t, "https://proj.example.com", regs[0].BaseURL.String())
}

// TestStartupDeferredClientInit simulates the CLI startup path where the
// global config asks to replace defaults but supplies no entries. Eager
// NewClient construction would fail, so main() must defer it until project
// config has been merged. This test exercises the order:
//
//  1. Load global config (replace_defaults=true, no entries).
//  2. Do NOT construct the client yet.
//  3. A project command reads dbc.toml with [[registries]].
//  4. applyProjectRegistries builds the client successfully.
//
// If anyone re-adds an eager initDBCClient() call in main() before step 3,
// this test still passes — but TestStartupEagerInitRejectsEmptyGlobal below
// pins the rejection we'd get in that broken ordering, so a regression is
// caught by the combination.
func TestStartupDeferredClientInit(t *testing.T) {
	t.Setenv("DBC_BASE_URL", "")

	savedGlobal, savedClient, savedOnce := globalRegistryConfig, dbcClient, dbcClientOnce
	savedErr := dbcClientErr
	t.Cleanup(func() {
		globalRegistryConfig = savedGlobal
		dbcClient = savedClient
		dbcClientOnce = savedOnce
		dbcClientErr = savedErr
	})

	// Simulate fresh process state: no client yet, sync.Once unused.
	dbcClient = nil
	dbcClientErr = nil
	dbcClientOnce = &sync.Once{}

	globalRegistryConfig = &dbc.GlobalConfig{ReplaceDefaults: true}

	// Step 2 invariant: main() must not have built a client yet.
	require.Nil(t, dbcClient, "main() must defer dbcClient construction until after argument parsing")

	// Step 3 & 4: project command reads dbc.toml and applies registries.
	list := DriversList{
		Registries: []dbc.RegistryEntry{{URL: "https://proj.example.com"}},
	}
	require.NoError(t, applyProjectRegistries(list))
	require.NotNil(t, dbcClient)
	require.Len(t, dbcClient.Registries(), 1)
}

// TestAddCmdEndToEndThroughRealClient runs AddCmd.GetModel() — the production
// path that uses defaultBaseModel and the real getDriverRegistry closure —
// against an httptest registry. A project dbc.toml with [[registries]] must
// cause the add command's driver lookup to actually hit that server. If
// applyProjectRegistries ever stops running before driver resolution, or if
// getDriverRegistry stops routing through dbcClient, this test fails.
func TestAddCmdEndToEndThroughRealClient(t *testing.T) {
	indexYAML := `drivers:
  - name: E2E Driver
    description: end-to-end test driver
    license: MIT
    path: e2e-driver
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            url: e2e-driver/1.0.0/e2e.tar.gz
          - platform: linux_arm64
            url: e2e-driver/1.0.0/e2e.tar.gz
          - platform: macos_amd64
            url: e2e-driver/1.0.0/e2e.tar.gz
          - platform: macos_arm64
            url: e2e-driver/1.0.0/e2e.tar.gz
          - platform: windows_amd64
            url: e2e-driver/1.0.0/e2e.tar.gz
          - platform: windows_arm64
            url: e2e-driver/1.0.0/e2e.tar.gz
`

	var indexHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/index.yaml") {
			atomic.AddInt32(&indexHits, 1)
			w.Header().Set("Content-Type", "application/yaml")
			w.Write([]byte(indexYAML))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	t.Setenv("DBC_BASE_URL", "")

	savedClient, savedErr, savedOnce := dbcClient, dbcClientErr, dbcClientOnce
	savedGetter, savedGlobal := getDriverRegistry, globalRegistryConfig
	t.Cleanup(func() {
		dbcClient = savedClient
		dbcClientErr = savedErr
		dbcClientOnce = savedOnce
		getDriverRegistry = savedGetter
		globalRegistryConfig = savedGlobal
	})

	dbcClient = nil
	dbcClientErr = nil
	dbcClientOnce = &sync.Once{}
	globalRegistryConfig = nil

	// Restore the production getDriverRegistry (the test suite's SetupSuite
	// in the subcommand tests replaces it with a stub; this test runs
	// outside the suite, but be explicit).
	getDriverRegistry = func() ([]dbc.Driver, error) {
		if err := initDBCClient(); err != nil {
			return nil, err
		}
		return dbcClient.Search("")
	}

	tempdir := t.TempDir()
	tomlPath := tempdir + "/dbc.toml"
	replace := true
	content := "replace_defaults = true\n\n" +
		"[[registries]]\nurl = '" + server.URL + "'\nname = 'e2e'\n\n" +
		"[drivers]\n"
	require.NoError(t, writeFile(tomlPath, content))

	// Run AddCmd.GetModel() — the production entry point.
	m := AddCmd{Path: tomlPath, Driver: []string{"e2e-driver"}}.GetModel()

	// Run the tea.Cmd chain to completion synchronously.
	msg := runTeaCmdToCompletion(t, m)
	if errMsg, ok := msg.(error); ok {
		t.Fatalf("AddCmd failed: %v", errMsg)
	}

	assert.GreaterOrEqual(t, atomic.LoadInt32(&indexHits), int32(1),
		"AddCmd.GetModel() should have hit the project-declared registry")

	// And the dbc.toml should have gained the driver entry.
	assert.FileExists(t, tomlPath)
	_ = replace
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// runTeaCmdToCompletion drives a tea.Model's Init() command chain far enough
// to reach the terminal message (addDoneMsg or an error). This sidesteps
// bubbletea's event loop so we can unit-test end-to-end without a terminal.
func runTeaCmdToCompletion(t *testing.T, m interface {
	Init() tea.Cmd
	Update(tea.Msg) (tea.Model, tea.Cmd)
}) tea.Msg {
	t.Helper()
	cmd := m.Init()
	for i := 0; i < 10 && cmd != nil; i++ {
		msg := cmd()
		if msg == nil {
			return nil
		}
		if _, ok := msg.(addDoneMsg); ok {
			return msg
		}
		if _, ok := msg.(error); ok {
			return msg
		}
		var nm tea.Model
		nm, cmd = m.Update(msg)
		if hs, ok := nm.(HasStatus); ok && hs.Err() != nil {
			return hs.Err()
		}
		m = nm.(interface {
			Init() tea.Cmd
			Update(tea.Msg) (tea.Model, tea.Cmd)
		})
	}
	return nil
}

// TestAuthHTTPClientDoesNotRequireRegistryConfig pins the invariant that
// OAuth/device-code login does NOT require valid registry configuration.
// Previously device-code login routed through dbcClient, so a global
// replace_defaults=true with no entries broke `dbc auth login` — the exact
// recovery path a user would take to fix the config.
func TestAuthHTTPClientDoesNotRequireRegistryConfig(t *testing.T) {
	t.Setenv("DBC_BASE_URL", "")

	savedGlobal := globalRegistryConfig
	t.Cleanup(func() { globalRegistryConfig = savedGlobal })

	// A global config that would make NewClient(WithGlobalConfig(...)) fail.
	globalRegistryConfig = &dbc.GlobalConfig{ReplaceDefaults: true}

	// authHTTPClient must still return a usable client.
	c := authHTTPClient()
	require.NotNil(t, c)
}

// TestGetDriverListHonorsProjectRegistries proves GetDriverList — a helper
// used by library consumers that parse dbc.toml directly — honors the
// project's [[registries]] section when resolving driver packages, AND
// does not leak that configuration across calls in the same process.
func TestGetDriverListHonorsProjectRegistries(t *testing.T) {
	indexFor := func(drvName string) string {
		return `drivers:
  - name: ` + drvName + `
    description: test
    license: MIT
    path: ` + drvName + `
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            url: ` + drvName + `/1.0.0/x.tar.gz
          - platform: linux_arm64
            url: ` + drvName + `/1.0.0/x.tar.gz
          - platform: macos_amd64
            url: ` + drvName + `/1.0.0/x.tar.gz
          - platform: macos_arm64
            url: ` + drvName + `/1.0.0/x.tar.gz
          - platform: windows_amd64
            url: ` + drvName + `/1.0.0/x.tar.gz
          - platform: windows_arm64
            url: ` + drvName + `/1.0.0/x.tar.gz
`
	}

	makeServer := func(drvName string, hits *int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/index.yaml") {
				atomic.AddInt32(hits, 1)
				w.Header().Set("Content-Type", "application/yaml")
				w.Write([]byte(indexFor(drvName)))
				return
			}
			http.NotFound(w, r)
		}))
	}

	t.Setenv("DBC_BASE_URL", "")

	savedGlobal := globalRegistryConfig
	t.Cleanup(func() { globalRegistryConfig = savedGlobal })
	globalRegistryConfig = nil

	var hitsA, hitsB int32
	serverA := makeServer("driver-a", &hitsA)
	defer serverA.Close()
	serverB := makeServer("driver-b", &hitsB)
	defer serverB.Close()

	writeList := func(t *testing.T, registryURL, drvName string) string {
		t.Helper()
		p := t.TempDir() + "/dbc.toml"
		content := "replace_defaults = true\n\n" +
			"[[registries]]\nurl = '" + registryURL + "'\n\n" +
			"[drivers]\n[drivers." + drvName + "]\nversion = '>=1.0.0'\n"
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		return p
	}

	// Call 1: dbc.toml pointing at server A.
	pkgs, err := GetDriverList(writeList(t, serverA.URL, "driver-a"))
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "driver-a", pkgs[0].Driver.Path)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&hitsA), int32(1))

	// Call 2: a DIFFERENT dbc.toml pointing at server B. If GetDriverList
	// leaked registry state from call 1, server B wouldn't be hit.
	pkgs, err = GetDriverList(writeList(t, serverB.URL, "driver-b"))
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "driver-b", pkgs[0].Driver.Path)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&hitsB), int32(1),
		"GetDriverList must not leak registry state from a previous call")
}

// TestStartupEndToEndGlobalReplaceDefaultsWithProjectEntries runs the full
// CLI startup sequence (loadStartupRegistryConfig + project-command dispatch
// via applyProjectRegistries) against a temp global config.toml declaring
// replace_defaults=true with no entries, and a project dbc.toml supplying
// the registries. This is the exact scenario that an eager initDBCClient()
// in main() would break. If anyone reintroduces eager init this test fails.
func TestStartupEndToEndGlobalReplaceDefaultsWithProjectEntries(t *testing.T) {
	indexYAML := `drivers:
  - name: Startup Driver
    description: startup e2e
    license: MIT
    path: startup-driver
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            url: startup-driver/1.0.0/x.tar.gz
          - platform: linux_arm64
            url: startup-driver/1.0.0/x.tar.gz
          - platform: macos_amd64
            url: startup-driver/1.0.0/x.tar.gz
          - platform: macos_arm64
            url: startup-driver/1.0.0/x.tar.gz
          - platform: windows_amd64
            url: startup-driver/1.0.0/x.tar.gz
          - platform: windows_arm64
            url: startup-driver/1.0.0/x.tar.gz
`

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/index.yaml") {
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "application/yaml")
			w.Write([]byte(indexYAML))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	t.Setenv("DBC_BASE_URL", "")

	savedClient, savedErr, savedOnce := dbcClient, dbcClientErr, dbcClientOnce
	savedGetter, savedGlobal := getDriverRegistry, globalRegistryConfig
	t.Cleanup(func() {
		dbcClient = savedClient
		dbcClientErr = savedErr
		dbcClientOnce = savedOnce
		getDriverRegistry = savedGetter
		globalRegistryConfig = savedGlobal
	})
	dbcClient = nil
	dbcClientErr = nil
	dbcClientOnce = &sync.Once{}
	globalRegistryConfig = nil

	// Write the global config.toml with replace_defaults=true and no entries.
	globalDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		globalDir+"/config.toml",
		[]byte("replace_defaults = true\n"),
		0o644,
	))

	// Write the project dbc.toml with a registry that satisfies the merge.
	projectPath := t.TempDir() + "/dbc.toml"
	require.NoError(t, os.WriteFile(projectPath, []byte(
		"[[registries]]\nurl = '"+server.URL+"'\n\n[drivers]\n[drivers.startup-driver]\nversion = '>=1.0.0'\n",
	), 0o644))

	// Rewire getDriverRegistry so the eventual AddCmd lookup goes through
	// the real dbcClient instead of the test-suite stub.
	getDriverRegistry = func() ([]dbc.Driver, error) {
		if err := initDBCClient(); err != nil {
			return nil, err
		}
		return dbcClient.Search("")
	}

	// Run the real startup sequence end-to-end by invoking the shared
	// helper main() also uses — runStartup covers config load, argv parse,
	// and subcommand dispatch (including GetModel). If anyone
	// reintroduces eager initDBCClient() anywhere in that path, dbcClient
	// would be non-nil here and runStartup would have already failed with
	// "empty registry list" for this global config.
	res := runStartup(globalDir, []string{"add", "--path", projectPath, "startup-driver"})
	require.Equal(t, startupModel, res.kind, "runStartup must reach the model branch without error")
	require.NotNil(t, res.model)
	require.NotNil(t, globalRegistryConfig, "runStartup must stash the global config")
	require.Nil(t, dbcClient, "runStartup must defer dbcClient construction until applyProjectRegistries")

	msgOut := runTeaCmdToCompletion(t, res.model.(interface {
		Init() tea.Cmd
		Update(tea.Msg) (tea.Model, tea.Cmd)
	}))
	if errMsg, ok := msgOut.(error); ok {
		t.Fatalf("AddCmd failed: %v", errMsg)
	}

	require.NotNil(t, dbcClient, "applyProjectRegistries must have built a client")
	assert.GreaterOrEqual(t, atomic.LoadInt32(&hits), int32(1),
		"project-declared registry should have been hit end-to-end")
}

// TestRunStartupSkipsLoadWhenConfigDirEmpty pins the invariant that
// runStartup does NOT read ./config.toml from the current working directory
// when the user config directory could not be located (configDir==""). A
// regression here would make an unrelated ./config.toml in the invocation
// CWD silently change registry resolution.
func TestRunStartupSkipsLoadWhenConfigDirEmpty(t *testing.T) {
	t.Setenv("DBC_BASE_URL", "")

	savedGlobal := globalRegistryConfig
	t.Cleanup(func() { globalRegistryConfig = savedGlobal })
	globalRegistryConfig = nil

	// Plant a hostile config.toml in cwd to prove runStartup doesn't touch it.
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(cwd+"/config.toml", []byte("replace_defaults = true\n"), 0o644))
	savedCWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(cwd))
	t.Cleanup(func() { _ = os.Chdir(savedCWD) })

	// configDir == "" means main() failed to resolve the user config dir.
	res := runStartup("", []string{"search"})
	require.Equal(t, startupModel, res.kind)
	assert.Nil(t, globalRegistryConfig,
		"runStartup must not read ./config.toml when configDir is empty")
}

// TestRunStartupClearsStaleClientState pins the invariant that runStartup
// resets ALL cached client state (globalRegistryConfig, dbcClient,
// dbcClientOnce) at the start of every call, so a second in-process startup
// doesn't silently reuse the previous invocation's registries.
func TestRunStartupClearsStaleClientState(t *testing.T) {
	t.Setenv("DBC_BASE_URL", "")

	savedGlobal, savedClient, savedErr, savedOnce := globalRegistryConfig, dbcClient, dbcClientErr, dbcClientOnce
	t.Cleanup(func() {
		globalRegistryConfig = savedGlobal
		dbcClient = savedClient
		dbcClientErr = savedErr
		dbcClientOnce = savedOnce
	})

	// Prime BOTH state paths as if a previous runStartup invocation had
	// loaded a config and built a client. The review specifically called
	// out that clearing only globalRegistryConfig isn't enough because
	// dbcClient is cached via dbcClientOnce.
	staleClient, err := dbc.NewClient(dbc.WithBaseURL("https://stale.example.com"))
	require.NoError(t, err)
	dbcClient = staleClient
	dbcClientErr = nil
	once := &sync.Once{}
	once.Do(func() {}) // mark as "already run" so reinit would be skipped
	dbcClientOnce = once
	globalRegistryConfig = &dbc.GlobalConfig{
		Registries: []dbc.RegistryEntry{{URL: "https://stale.example.com"}},
	}

	res := runStartup("", []string{"search"})
	require.Equal(t, startupModel, res.kind)

	assert.Nil(t, globalRegistryConfig,
		"runStartup must clear globalRegistryConfig")
	assert.Nil(t, dbcClient,
		"runStartup must clear dbcClient so stale registries aren't reused")
	assert.NotSame(t, once, dbcClientOnce,
		"runStartup must reset dbcClientOnce so a fresh init actually runs")

	// Fresh init should now succeed with defaults, not stale state.
	require.NoError(t, initDBCClient())
	require.NotNil(t, dbcClient)
	for _, r := range dbcClient.Registries() {
		require.NotEqual(t, "https://stale.example.com", r.BaseURL.String(),
			"fresh client must not carry the stale registry")
	}
}

// TestStartupEagerInitRejectsEmptyGlobal pins the invariant that justifies
// the deferred-init ordering above: if the CLI ever tries to build the
// default client against a global replace_defaults=true with no entries
// and no project overrides, NewClient must fail. This is the scenario
// non-project commands (search, info, install) will hit, so failing fast
// with a clear message is the correct behavior.
func TestStartupEagerInitRejectsEmptyGlobal(t *testing.T) {
	t.Setenv("DBC_BASE_URL", "")

	savedGlobal := globalRegistryConfig
	t.Cleanup(func() { globalRegistryConfig = savedGlobal })

	globalRegistryConfig = &dbc.GlobalConfig{ReplaceDefaults: true}

	_, err := newDefaultClient()
	assert.ErrorContains(t, err, "empty registry list")
}