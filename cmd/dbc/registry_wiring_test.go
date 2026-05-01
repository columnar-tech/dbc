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
	"strings"
	"sync/atomic"
	"testing"

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