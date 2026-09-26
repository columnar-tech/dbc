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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/columnar-tech/dbc/internal/packslip"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type addPackslipResolverFunc func(context.Context, packslip.PackslipSource, packslip.Request) (resolution.ResolvedRelease, error)

func (f addPackslipResolverFunc) Resolve(ctx context.Context, source packslip.PackslipSource, request packslip.Request) (resolution.ResolvedRelease, error) {
	return f(ctx, source, request)
}

func TestAdd(t *testing.T) {
	dir := t.TempDir()
	var err error
	{
		m := InitCmd{Path: filepath.Join(dir, "dbc.toml")}.GetModel()

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		m, err = p.Run()

		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())

		assert.FileExists(t, filepath.Join(dir, "dbc.toml"))
	}

	{
		m := AddCmd{Path: filepath.Join(dir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModelCustom(
			testBaseModel())

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		var err error
		m, err = p.Run()
		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())

		data, err := os.ReadFile(filepath.Join(dir, "dbc.toml"))
		require.NoError(t, err)
		assert.Equal(t, `# dbc driver list
[drivers]
[drivers.test-driver-1]
`, string(data))
	}
}

func TestAddRepeatedNewWithConstraint(t *testing.T) {
	// Test what happens when we `add` without a constraint and then add with a
	// constraint. This specifically tests the bubbletea output
	defer func(fn func() ([]dbc.Driver, error)) {
		getDriverRegistry = fn
	}(getDriverRegistry)
	getDriverRegistry = getTestDriverRegistry

	dir := t.TempDir()
	var err error
	{
		m := InitCmd{Path: filepath.Join(dir, "dbc.toml")}.GetModel()

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		m, err = p.Run()

		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())

		assert.FileExists(t, filepath.Join(dir, "dbc.toml"))
	}

	{
		m := AddCmd{Path: filepath.Join(dir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModelCustom(
			testBaseModel())

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		var err error
		m, err = p.Run()
		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())

		data, err := os.ReadFile(filepath.Join(dir, "dbc.toml"))
		require.NoError(t, err)
		assert.Equal(t, `# dbc driver list
[drivers]
[drivers.test-driver-1]
`, string(data))
	}

	{
		m := AddCmd{Path: filepath.Join(dir, "dbc.toml"), Driver: []string{"test-driver-1>=1.0.0"}}.GetModelCustom(
			testBaseModel())

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		var err error
		m, err = p.Run()
		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())
		if fo, ok := m.(HasFinalOutput); ok {
			assert.Contains(t, fo.FinalOutput(), "old constraint: any; new constraint: >=1.0.0")
		}

		data, err := os.ReadFile(filepath.Join(dir, "dbc.toml"))
		require.NoError(t, err)
		assert.Equal(t, `# dbc driver list
[drivers]
[drivers.test-driver-1]
version = '>=1.0.0'
`, string(data))
	}
}

func TestAddUpdatingDriverPreservesSource(t *testing.T) {
	t.Setenv("DBC_BASE_URL", "")

	dir := t.TempDir()
	path := filepath.Join(dir, "dbc.toml")
	const initial = "[drivers]\n" +
		"[drivers.test-driver-1]\n" +
		"version = '<=1.8.0'\n" +
		"[drivers.test-driver-1.source]\n" +
		"type = 'registry'\n" +
		"url = 'https://registry.example.test/custom'\n"
	require.NoError(t, os.WriteFile(path, []byte(initial), 0o644))
	expectedSource := &dbc.DriverSource{
		Type: dbc.DriverSourceRegistry,
		URL:  "https://registry.example.test/custom",
	}

	runAdd := func(cmd AddCmd) DriversList {
		t.Helper()
		base := testBaseModel()
		base.getDriverRegistry = func() ([]dbc.Driver, error) {
			return []dbc.Driver{addTestRegistryDriver(t, expectedSource.URL, "1.0.0", "1.1.0")}, nil
		}
		msg := runTeaCmdToCompletion(t, cmd.GetModelCustom(base).(interface {
			Init() tea.Cmd
			Update(tea.Msg) (tea.Model, tea.Cmd)
		}))
		_, failed := msg.(error)
		require.False(t, failed, "add failed: %v", msg)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var list DriversList
		require.NoError(t, toml.Unmarshal(data, &list))
		require.NoError(t, list.validateSources())
		return list
	}

	list := runAdd(AddCmd{Path: path, Driver: []string{"test-driver-1>=1.0.0"}})
	got := list.Drivers["test-driver-1"]
	require.NotNil(t, got.Source)
	assert.Equal(t, *expectedSource, *got.Source)
	require.NotNil(t, got.Version)
	assert.Equal(t, ">=1.0.0", got.Version.String())
	assert.Empty(t, got.Prerelease)

	prereleaseList := runAdd(AddCmd{Path: path, Driver: []string{"test-driver-1"}, Pre: true})
	got = prereleaseList.Drivers["test-driver-1"]
	require.NotNil(t, got.Source)
	assert.Equal(t, *expectedSource, *got.Source)
	assert.Nil(t, got.Version)
	assert.Equal(t, "allow", got.Prerelease)
}

func addTestRegistryDriver(t *testing.T, registryURL string, versions ...string) dbc.Driver {
	t.Helper()

	drivers, err := getTestDriverRegistry()
	require.NoError(t, err)
	for _, driver := range drivers {
		if driver.Path != "test-driver-1" {
			continue
		}

		allowed := make(map[string]bool, len(versions))
		for _, version := range versions {
			allowed[version] = true
		}
		filtered := driver.PkgInfo[:0]
		for _, pkg := range driver.PkgInfo {
			if allowed[pkg.Version.String()] {
				filtered = append(filtered, pkg)
			}
		}
		driver.PkgInfo = filtered

		baseURL, err := url.Parse(registryURL)
		require.NoError(t, err)
		driver.Registry = &dbc.Registry{BaseURL: baseURL}
		return driver
	}
	t.Fatal("test driver test-driver-1 is missing from the fixture registry")
	return dbc.Driver{}
}

func TestAddExplicitRegistryUsesDeclaredRegistryForVersionValidation(t *testing.T) {
	t.Setenv("DBC_BASE_URL", "")

	const registryA = "https://registry-a.example.test"
	const registryB = "https://registry-b.example.test"
	tests := []struct {
		name        string
		declaredURL string
		drivers     []dbc.Driver
		wantSuccess bool
	}{
		{
			name:        "explicit B does not accept a version found only in A",
			declaredURL: registryB,
			drivers: []dbc.Driver{
				addTestRegistryDriver(t, registryA, "1.1.0"),
				addTestRegistryDriver(t, registryB, "1.0.0"),
			},
		},
		{
			name:        "explicit B accepts its version and preserves source",
			declaredURL: registryB,
			drivers: []dbc.Driver{
				addTestRegistryDriver(t, registryA, "1.0.0"),
				addTestRegistryDriver(t, registryB, "1.1.0"),
			},
			wantSuccess: true,
		},
		{
			name:        "explicit B does not fall back when the driver is absent",
			declaredURL: registryB,
			drivers: []dbc.Driver{
				addTestRegistryDriver(t, registryA, "1.1.0"),
			},
		},
		{
			name: "omitted source keeps A precedence",
			drivers: []dbc.Driver{
				addTestRegistryDriver(t, registryA, "1.0.0"),
				addTestRegistryDriver(t, registryB, "1.1.0"),
			},
		},
		{
			name:        "canonical equivalent URL selects B",
			declaredURL: "HTTPS://REGISTRY-B.EXAMPLE.TEST/#client-fragment",
			drivers: []dbc.Driver{
				addTestRegistryDriver(t, registryA, "1.0.0"),
				addTestRegistryDriver(t, registryB, "1.1.0"),
			},
			wantSuccess: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "dbc.toml")
			initial := "[drivers.test-driver-1]\n"
			if tt.declaredURL != "" {
				initial += "[drivers.test-driver-1.source]\n" +
					"type = 'registry'\n" +
					"url = '" + tt.declaredURL + "'\n"
			}
			require.NoError(t, os.WriteFile(path, []byte(initial), 0o644))

			model := AddCmd{Path: path, Driver: []string{"test-driver-1=1.1.0"}}.GetModelCustom(baseModel{
				getDriverRegistry: func() ([]dbc.Driver, error) { return tt.drivers, nil },
			})
			msg := runTeaCmdToCompletion(t, model.(interface {
				Init() tea.Cmd
				Update(tea.Msg) (tea.Model, tea.Cmd)
			}))
			_, failed := msg.(error)
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			if !tt.wantSuccess {
				require.True(t, failed, "add unexpectedly succeeded: %v", msg)
				assert.Equal(t, initial, string(data), "failed add must leave dbc.toml unchanged")
				return
			}

			require.False(t, failed, "add failed: %v", msg)
			var updated DriversList
			require.NoError(t, toml.Unmarshal(data, &updated))
			require.NoError(t, updated.validateSources())
			driver := updated.Drivers["test-driver-1"]
			require.NotNil(t, driver.Version)
			assert.Equal(t, "=1.1.0", driver.Version.String())
			if tt.declaredURL == "" {
				assert.Nil(t, driver.Source)
				return
			}
			require.NotNil(t, driver.Source)
			assert.Equal(t, tt.declaredURL, driver.Source.URL)
		})
	}
}

func TestAddUpdatesPackslipVersionWithoutRegistryLookup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dbc.toml")
	initial := "[drivers.test-driver-1]\n" +
		"version = '1.2.3'\n" +
		"[drivers.test-driver-1.source]\n" +
		"type = 'packslip'\n" +
		"project = 'github.com/example/driver'\n"
	require.NoError(t, os.WriteFile(path, []byte(initial), 0o644))

	release := makeSyncPackslipRelease(
		"test-driver-1", "1.2.4", "https://assets.example.test/driver.tgz",
		"sha256:"+strings.Repeat("a", 64), 10)
	resolver := &syncPackslipResolverStub{release: release}
	registryCalls := 0
	base := testBaseModel()
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		registryCalls++
		return nil, errors.New("Packslip add must not query registries")
	}
	base.newPackslipResolver = func() (packslip.Resolver, error) { return resolver, nil }
	msg := runTeaCmdToCompletion(t, AddCmd{
		Path:   path,
		Driver: []string{"test-driver-1=1.2.4"},
	}.GetModelCustom(base).(interface {
		Init() tea.Cmd
		Update(tea.Msg) (tea.Model, tea.Cmd)
	}))
	_, failed := msg.(error)
	require.False(t, failed, "Packslip add failed: %v", msg)
	assert.Equal(t, 0, registryCalls)
	assert.Equal(t, 1, resolver.calls)
	assert.Equal(t, "github.com/example/driver", resolver.project)
	assert.Equal(t, packslip.Request{DriverID: "test-driver-1", Version: "1.2.4"}, resolver.request)

	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	var updated DriversList
	require.NoError(t, toml.Unmarshal(data, &updated))
	require.NoError(t, updated.validateSources())
	got := updated.Drivers["test-driver-1"]
	assert.Equal(t, "1.2.4", got.Version.String())
	require.NotNil(t, got.Source)
	assert.Equal(t, dbc.DriverSource{Type: dbc.DriverSourcePackslip, Project: "github.com/example/driver"}, *got.Source)
}

func TestAddPathSourceUsesProjectDirectoryAndCanDeriveVersion(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project")
	otherDir := filepath.Join(dir, "elsewhere")
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, "packages"), 0o755))
	require.NoError(t, os.MkdirAll(otherDir, 0o755))
	archive, err := os.ReadFile(filepath.Join("testdata", "test-driver-1.tar.gz"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "packages", "driver.tar.gz"), archive, 0o644))
	path := filepath.Join(projectDir, "dbc.toml")
	initial := "[drivers.test-driver-1]\nversion = '1.0.0'\n" +
		"[drivers.test-driver-1.source]\n" +
		"type = 'path'\n" +
		"path = './packages/driver.tar.gz'\n"
	require.NoError(t, os.WriteFile(path, []byte(initial), 0o644))
	t.Chdir(otherDir)

	registryCalls := 0
	base := testBaseModel()
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		registryCalls++
		return nil, errors.New("path add must not query registries")
	}
	msg := runTeaCmdToCompletion(t, AddCmd{
		Path: path, Driver: []string{"test-driver-1"},
	}.GetModelCustom(base).(interface {
		Init() tea.Cmd
		Update(tea.Msg) (tea.Model, tea.Cmd)
	}))
	_, failed := msg.(error)
	require.False(t, failed, "path add failed: %v", msg)
	assert.Equal(t, 0, registryCalls)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var updated DriversList
	require.NoError(t, toml.Unmarshal(data, &updated))
	require.NoError(t, updated.validateSources())
	got := updated.Drivers["test-driver-1"]
	assert.Nil(t, got.Version, "omitted path version should return to metadata-derived selection")
	require.NotNil(t, got.Source)
	assert.Equal(t, dbc.DriverSource{Type: dbc.DriverSourcePath, Path: "./packages/driver.tar.gz"}, *got.Source)
}

func TestAddPathSourceRequiresExactVersionToMatchArchive(t *testing.T) {
	dir := t.TempDir()
	archive, err := os.ReadFile(filepath.Join("testdata", "test-driver-1.tar.gz"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "driver.tar.gz"), archive, 0o644))
	path := filepath.Join(dir, "dbc.toml")
	initial := "[drivers.test-driver-1.source]\n" +
		"type = 'path'\n" +
		"path = './driver.tar.gz'\n"
	require.NoError(t, os.WriteFile(path, []byte(initial), 0o644))
	base := testBaseModel()
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		return nil, errors.New("path add must not query registries")
	}
	matching := runTeaCmdToCompletion(t, AddCmd{
		Path: path, Driver: []string{"test-driver-1=1.0.0"},
	}.GetModelCustom(base).(interface {
		Init() tea.Cmd
		Update(tea.Msg) (tea.Model, tea.Cmd)
	}))
	_, failed := matching.(error)
	require.False(t, failed, "path source exact version should match archive metadata: %v", matching)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var updated DriversList
	require.NoError(t, toml.Unmarshal(data, &updated))
	require.NoError(t, updated.validateSources())
	assert.Equal(t, "1.0.0", updated.Drivers["test-driver-1"].Version.String())
	require.NotNil(t, updated.Drivers["test-driver-1"].Source)
	assert.Equal(t, "./driver.tar.gz", updated.Drivers["test-driver-1"].Source.Path)

	// Run the mismatch against the original versionless entry to prove failure
	// does not modify the project configuration.
	require.NoError(t, os.WriteFile(path, []byte(initial), 0o644))

	msg := runTeaCmdToCompletion(t, AddCmd{
		Path: path, Driver: []string{"test-driver-1=1.1.0"},
	}.GetModelCustom(base).(interface {
		Init() tea.Cmd
		Update(tea.Msg) (tea.Model, tea.Cmd)
	}))
	err, ok := msg.(error)
	require.True(t, ok, "path source with mismatched version must fail")
	assert.ErrorContains(t, err, `local package version "1.0.0" does not match requested version "1.1.0"`)

	data, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, initial, string(data), "version mismatch must leave dbc.toml byte-for-byte unchanged")
}

func TestAddRejectsPackslipVersionMismatchWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dbc.toml")
	initial := "[drivers.test-driver-1]\nversion = '1.2.3'\n" +
		"[drivers.test-driver-1.source]\ntype = 'packslip'\n" +
		"project = 'github.com/example/driver'\n"
	require.NoError(t, os.WriteFile(path, []byte(initial), 0o644))
	release := makeSyncPackslipRelease(
		"test-driver-1", "1.2.5", "https://assets.example.test/driver.tgz",
		"sha256:"+strings.Repeat("a", 64), 10)
	resolver := &syncPackslipResolverStub{release: release}
	base := testBaseModel()
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		return nil, errors.New("Packslip add must not query registries")
	}
	base.newPackslipResolver = func() (packslip.Resolver, error) { return resolver, nil }
	msg := runTeaCmdToCompletion(t, AddCmd{
		Path: path, Driver: []string{"test-driver-1=1.2.4"},
	}.GetModelCustom(base).(interface {
		Init() tea.Cmd
		Update(tea.Msg) (tea.Model, tea.Cmd)
	}))
	err, ok := msg.(error)
	require.True(t, ok, "invalid Packslip result must fail before mutation")
	assert.ErrorContains(t, err, `packslip release version "1.2.5" does not match requested version "1.2.4"`)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, initial, string(data))
}

func TestAddResolvesPackslipOutsideProjectLockAndRejectsSourceDrift(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dbc.toml")
	initial := "[drivers.test-driver-1]\nversion = '1.2.3'\n" +
		"[drivers.test-driver-1.source]\ntype = 'packslip'\n" +
		"project = 'github.com/example/driver'\n"
	require.NoError(t, os.WriteFile(path, []byte(initial), 0o644))
	started := make(chan struct{})
	unblock := make(chan struct{})
	release := makeSyncPackslipRelease(
		"test-driver-1", "1.2.4", "https://assets.example.test/driver.tgz",
		"sha256:"+strings.Repeat("a", 64), 10)
	base := testBaseModel()
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		return nil, errors.New("Packslip add must not query registries")
	}
	base.newPackslipResolver = func() (packslip.Resolver, error) {
		return addPackslipResolverFunc(func(_ context.Context, _ packslip.PackslipSource, _ packslip.Request) (resolution.ResolvedRelease, error) {
			close(started)
			<-unblock
			return release, nil
		}), nil
	}
	done := make(chan tea.Msg, 1)
	go func() {
		model := AddCmd{Path: path, Driver: []string{"test-driver-1=1.2.4"}}.GetModelCustom(base)
		done <- runTeaCmdToCompletion(t, model.(interface {
			Init() tea.Cmd
			Update(tea.Msg) (tea.Model, tea.Cmd)
		}))
	}()
	<-started

	lockPath := filepath.Join(dir, ".dbc.project.lock")
	lock, err := acquireLock(lockPath, time.Second)
	require.NoError(t, err, "the Packslip resolver must run without holding the project lock")
	concurrent := "[drivers.test-driver-1]\nversion = '1.2.3'\n" +
		"[drivers.test-driver-1.source]\ntype = 'packslip'\n" +
		"project = 'github.com/example/other'\n"
	require.NoError(t, os.WriteFile(path, []byte(concurrent), 0o644))
	require.NoError(t, lock.Release())
	close(unblock)

	msg := <-done
	err, ok := msg.(error)
	require.True(t, ok, "source drift must abort the add operation")
	assert.ErrorContains(t, err, "driver \"test-driver-1\" source changed while resolving drivers")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, concurrent, string(data), "concurrent source edit must remain intact")
}

func TestAddNonRegistrySourcePreservesConcurrentRegistryChanges(t *testing.T) {
	t.Setenv("DBC_BASE_URL", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "dbc.toml")
	initial := "[drivers.test-driver-1]\nversion = '1.2.3'\n" +
		"[drivers.test-driver-1.source]\ntype = 'packslip'\n" +
		"project = 'github.com/example/driver'\n"
	require.NoError(t, os.WriteFile(path, []byte(initial), 0o644))
	started := make(chan struct{})
	unblock := make(chan struct{})
	release := makeSyncPackslipRelease(
		"test-driver-1", "1.2.4", "https://assets.example.test/driver.tgz",
		"sha256:"+strings.Repeat("a", 64), 10)
	base := testBaseModel()
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		return nil, errors.New("Packslip add must not query registries")
	}
	base.newPackslipResolver = func() (packslip.Resolver, error) {
		return addPackslipResolverFunc(func(_ context.Context, _ packslip.PackslipSource, _ packslip.Request) (resolution.ResolvedRelease, error) {
			close(started)
			<-unblock
			return release, nil
		}), nil
	}
	done := make(chan tea.Msg, 1)
	go func() {
		model := AddCmd{Path: path, Driver: []string{"test-driver-1=1.2.4"}}.GetModelCustom(base)
		done <- runTeaCmdToCompletion(t, model.(interface {
			Init() tea.Cmd
			Update(tea.Msg) (tea.Model, tea.Cmd)
		}))
	}()
	<-started

	lock, err := acquireLock(filepath.Join(dir, ".dbc.project.lock"), time.Second)
	require.NoError(t, err)
	concurrent := "[[registries]]\nurl = 'https://registry.example.test'\n\n" + initial
	require.NoError(t, os.WriteFile(path, []byte(concurrent), 0o644))
	require.NoError(t, lock.Release())
	close(unblock)

	msg := <-done
	_, failed := msg.(error)
	require.False(t, failed, "Packslip add should not depend on concurrent registry changes: %v", msg)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var updated DriversList
	require.NoError(t, toml.Unmarshal(data, &updated))
	require.NoError(t, updated.validateSources())
	require.Len(t, updated.Registries, 1)
	assert.Equal(t, "https://registry.example.test", updated.Registries[0].URL)
	assert.Equal(t, "1.2.4", updated.Drivers["test-driver-1"].Version.String())
}

func TestAddNonRegistrySourcesRejectInvalidVersionAndPreWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		source string
		input  string
		pre    bool
	}{
		{name: "packslip missing", source: "packslip", input: "test-driver-1"},
		{name: "packslip prerelease flag", source: "packslip", input: "test-driver-1=1.2.3", pre: true},
		{name: "path range", source: "path", input: "test-driver-1>=1.0.0"},
		{name: "path coercible", source: "path", input: "test-driver-1=01.2.3"},
		{name: "path prerelease flag", source: "path", input: "test-driver-1", pre: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "dbc.toml")
			initial := "[drivers.test-driver-1]\nversion = '1.2.3'\n" +
				"[drivers.test-driver-1.source]\n"
			if tt.source == "packslip" {
				initial += "type = 'packslip'\nproject = 'github.com/example/test-driver'\n"
			} else {
				initial += "type = 'path'\npath = './driver.tar.gz'\n"
			}
			require.NoError(t, os.WriteFile(path, []byte(initial), 0o644))
			base := testBaseModel()
			base.getDriverRegistry = func() ([]dbc.Driver, error) {
				return nil, errors.New("non-registry add must not query registries")
			}
			resolverConstructed := false
			base.newPackslipResolver = func() (packslip.Resolver, error) {
				resolverConstructed = true
				return nil, errors.New("invalid Packslip request must fail before resolver construction")
			}
			msg := runTeaCmdToCompletion(t, AddCmd{
				Path: path, Driver: []string{tt.input}, Pre: tt.pre,
			}.GetModelCustom(base).(interface {
				Init() tea.Cmd
				Update(tea.Msg) (tea.Model, tea.Cmd)
			}))
			_, failed := msg.(error)
			require.True(t, failed, "expected validation error, got %v", msg)
			assert.False(t, resolverConstructed)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, initial, string(data), "invalid add must leave dbc.toml byte-for-byte unchanged")
		})
	}
}

func TestAddMultiple(t *testing.T) {
	// Test what happens when we `add` without a constraint and then add with a
	// constraint. This specifically tests the bubbletea output
	defer func(fn func() ([]dbc.Driver, error)) {
		getDriverRegistry = fn
	}(getDriverRegistry)
	getDriverRegistry = getTestDriverRegistry

	dir := t.TempDir()
	var err error
	{
		m := InitCmd{Path: filepath.Join(dir, "dbc.toml")}.GetModel()

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		m, err = p.Run()

		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())

		assert.FileExists(t, filepath.Join(dir, "dbc.toml"))
	}
	{
		m := AddCmd{Path: filepath.Join(dir, "dbc.toml"), Driver: []string{"test-driver-2", "test-driver-1>=1.0.0"}}.
			GetModelCustom(
				testBaseModel())

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		var err error
		m, err = p.Run()
		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())

		data, err := os.ReadFile(filepath.Join(dir, "dbc.toml"))
		require.NoError(t, err)
		assert.Equal(t, `# dbc driver list
[drivers]
[drivers.test-driver-1]
version = '>=1.0.0'

[drivers.test-driver-2]
`, string(data))
	}
}

func (suite *SubcommandTestSuite) TestAddWithPre() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Add driver with --pre flag
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-2"},
		Pre:    true,
	}.GetModelCustom(
		testBaseModel())

	suite.runCmd(m)

	// Verify the file contents
	data, err := os.ReadFile(filepath.Join(suite.tempdir, "dbc.toml"))
	suite.Require().NoError(err)
	suite.Equal(`# dbc driver list
[drivers]
[drivers.test-driver-2]
prerelease = 'allow'
`, string(data))
}

func (suite *SubcommandTestSuite) TestAddWithPreOnlyPrereleaseDriver() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Add driver that only has prerelease versions with --pre flag
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-only-pre"},
		Pre:    true,
	}.GetModelCustom(
		testBaseModel())

	suite.runCmd(m)

	// Verify the file contents
	data, err := os.ReadFile(filepath.Join(suite.tempdir, "dbc.toml"))
	suite.Require().NoError(err)
	suite.Equal(`# dbc driver list
[drivers]
[drivers.test-driver-only-pre]
prerelease = 'allow'
`, string(data))
}

func (suite *SubcommandTestSuite) TestAddWithoutPreOnlyPrereleaseDriver() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Try to add driver that only has prerelease versions without --pre flag (should fail)
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-only-pre"},
		Pre:    false,
	}.GetModelCustom(
		testBaseModel())

	out := suite.runCmdErr(m)
	suite.Contains(out, "driver `test-driver-only-pre` not found in driver registry index (but prerelease versions filtered out); try: dbc add --pre test-driver-only-pre")
}

func (suite *SubcommandTestSuite) TestAddWithPreAndConstraint() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Add driver with --pre flag and a version constraint
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-2>=2.0.0"},
		Pre:    true,
	}.GetModelCustom(
		testBaseModel())

	suite.runCmd(m)

	// Verify the file contents
	data, err := os.ReadFile(filepath.Join(suite.tempdir, "dbc.toml"))
	suite.Require().NoError(err)
	suite.Equal(`# dbc driver list
[drivers]
[drivers.test-driver-2]
prerelease = 'allow'
version = '>=2.0.0'
`, string(data))
}

func (suite *SubcommandTestSuite) TestAddExplicitPrereleaseWithoutPreFlag() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Add explicit prerelease version WITHOUT --pre flag, should succeed per requirement
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-only-pre=0.9.0-alpha.1"},
		Pre:    false,
	}.GetModelCustom(
		testBaseModel())

	suite.runCmd(m)

	// Verify the file contents - should NOT include prerelease = 'allow' since --pre was not specified
	data, err := os.ReadFile(filepath.Join(suite.tempdir, "dbc.toml"))
	suite.Require().NoError(err)
	suite.Equal(`# dbc driver list
[drivers]
[drivers.test-driver-only-pre]
version = '=0.9.0-alpha.1'
`, string(data))
}

func (suite *SubcommandTestSuite) TestAddPartialRegistryFailure() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Test that add command handles partial registry failure gracefully
	// (one registry succeeds, another fails - returns both drivers and error)
	partialFailingRegistry := func() ([]dbc.Driver, error) {
		// Get drivers from the test registry (simulating one successful registry)
		drivers, _ := getTestDriverRegistry()
		// But also return an error (simulating another registry that failed)
		return drivers, fmt.Errorf("registry https://cdn-fallback.example.com: failed to fetch driver registry: DNS resolution failed")
	}

	// Should succeed if the requested driver is found in the available drivers
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1"},
		Pre:    false,
	}.GetModelCustom(
		baseModel{getDriverRegistry: partialFailingRegistry, downloadPkg: downloadTestPkg})

	suite.runCmd(m)
	// Should succeed without printing the registry error

	// Verify the file was updated correctly
	data, err := os.ReadFile(filepath.Join(suite.tempdir, "dbc.toml"))
	suite.Require().NoError(err)
	suite.Contains(string(data), "[drivers.test-driver-1]")
}

func (suite *SubcommandTestSuite) TestAddPartialRegistryFailureDriverNotFound() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Test that add command shows registry errors when the requested driver is not found
	partialFailingRegistry := func() ([]dbc.Driver, error) {
		// Get drivers from the test registry (simulating one successful registry)
		drivers, _ := getTestDriverRegistry()
		// But also return an error (simulating another registry that failed)
		return drivers, fmt.Errorf("registry https://cdn-fallback.example.com: failed to fetch driver registry: DNS resolution failed")
	}

	// Should fail with enhanced error message if the requested driver is not found
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"nonexistent-driver"},
		Pre:    false,
	}.GetModelCustom(
		baseModel{getDriverRegistry: partialFailingRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmdErr(m)
	// Should show the driver not found error AND the registry error
	suite.Contains(out, "driver `nonexistent-driver` not found")
	suite.Contains(out, "Note: Some driver registries were unavailable")
	suite.Contains(out, "failed to fetch driver registry")
	suite.Contains(out, "DNS resolution failed")
}

func (suite *SubcommandTestSuite) TestAddCompleteRegistryFailure() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Test that add command handles complete registry failure (no drivers returned)
	completeFailingRegistry := func() ([]dbc.Driver, error) {
		return nil, fmt.Errorf("registry https://primary-cdn.example.com: network unreachable")
	}

	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1"},
		Pre:    false,
	}.GetModelCustom(
		baseModel{getDriverRegistry: completeFailingRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmdErr(m)
	suite.Contains(out, "error getting driver list")
	suite.Contains(out, "network unreachable")
}

func (suite *SubcommandTestSuite) TestAddOutput() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1"},
	}.GetModelCustom(
		testBaseModel())

	out := suite.runCmd(m)
	suite.Contains(out, "added test-driver-1 to driver list")
	suite.Contains(out, "use `dbc sync` to install the drivers in the list")
}

func (suite *SubcommandTestSuite) TestAdd_JSON() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1"},
		Json:   true,
	}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmd(m)

	var env jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(out), &env), "output must be valid JSON: %s", out)
	suite.Equal(1, env.SchemaVersion)
	suite.Equal("add.response", env.Kind)

	var resp jsonschema.AddResponse
	suite.Require().NoError(json.Unmarshal(env.Payload, &resp))
	suite.Require().Len(resp.Drivers, 1)
	suite.Equal("test-driver-1", resp.Drivers[0].Name)
	suite.NotEmpty(resp.DriverListPath)
}

func (suite *SubcommandTestSuite) TestAdd_JSON_Constraint() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1>=1.0.0"},
		Json:   true,
	}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmd(m)

	var env jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(out), &env), "output must be valid JSON: %s", out)
	suite.Equal(1, env.SchemaVersion)
	suite.Equal("add.response", env.Kind)

	// verify > is not HTML-escaped (>) in the JSON output
	suite.NotContains(string(env.Payload), "\\u003e")

	var resp jsonschema.AddResponse
	suite.Require().NoError(json.Unmarshal(env.Payload, &resp))
	suite.Require().Len(resp.Drivers, 1)
	suite.Equal("test-driver-1", resp.Drivers[0].Name)
	suite.Equal(">=1.0.0", resp.Drivers[0].VersionConstraint)
	suite.NotEmpty(resp.DriverListPath)
}

func (suite *SubcommandTestSuite) TestAddWithProjectRegistries() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	err := os.WriteFile(filepath.Join(suite.tempdir, "dbc.toml"), []byte(`# dbc driver list
[[registries]]
url = 'https://custom-registry.example.com'
name = 'custom'

[drivers]
`), 0644)
	suite.Require().NoError(err)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModelCustom(testBaseModel())
	out := suite.runCmd(m)
	suite.Contains(out, "added test-driver-1 to driver list")

	data, err := os.ReadFile(filepath.Join(suite.tempdir, "dbc.toml"))
	suite.Require().NoError(err)
	suite.Contains(string(data), "[drivers.test-driver-1]")

	if os.Getenv("DBC_BASE_URL") == "" {
		suite.Require().NotNil(dbcClient)
		found := false
		for _, r := range dbcClient.Registries() {
			if r.BaseURL != nil && r.BaseURL.String() == "https://custom-registry.example.com" {
				found = true
				break
			}
		}
		suite.True(found, "expected custom registry in active client registries after add with [[registries]] in dbc.toml")
	}
}

func (suite *SubcommandTestSuite) TestAddWithInvalidProjectRegistryURL() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	err := os.WriteFile(filepath.Join(suite.tempdir, "dbc.toml"), []byte(`# dbc driver list
[[registries]]
url = ''

[drivers]
[drivers.test-driver-1]
`), 0644)
	suite.Require().NoError(err)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)
	suite.Contains(out, "empty url")
}

func (suite *SubcommandTestSuite) TestAddMultipleOutput() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1", "test-driver-2"},
	}.GetModelCustom(
		testBaseModel())

	out := suite.runCmd(m)
	suite.Contains(out, "added test-driver-1 to driver list")
	suite.Contains(out, "added test-driver-2 to driver list")
	suite.Contains(out, "use `dbc sync` to install the drivers in the list")
}

func (suite *SubcommandTestSuite) TestAddReplacingDriverOutput() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Add driver without constraint
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1"},
	}.GetModelCustom(
		testBaseModel())
	suite.runCmd(m)

	// Add same driver with constraint and verify replacement message
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1>=1.0.0"},
	}.GetModelCustom(
		testBaseModel())

	out := suite.runCmd(m)
	suite.Contains(out, "replacing existing driver test-driver-1")
	suite.Contains(out, "old constraint: any; new constraint: >=1.0.0")
	suite.Contains(out, "added test-driver-1 to driver list")
	suite.Contains(out, "with constraint >=1.0.0")
}

func (suite *SubcommandTestSuite) TestAdd_JSON_DriverNotFound() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Use the real test registry but request a driver that doesn't exist,
	// exercising the findDriver path rather than the registry-failure path.
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"nonexistent-driver"},
		Json:   true,
	}.GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmdErr(m)
	suite.assertJSONErrorEnvelope(out, "add_failed")
}

func (suite *SubcommandTestSuite) TestAdd_JSON_RegistryFailure() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Verify that a complete registry failure also emits a structured error envelope
	// and that the underlying registry error detail is preserved in the message.
	failingRegistry := func() ([]dbc.Driver, error) {
		return nil, fmt.Errorf("network unreachable")
	}
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1"},
		Json:   true,
	}.GetModelCustom(baseModel{getDriverRegistry: failingRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmdErr(m)
	suite.assertJSONErrorEnvelope(out, "add_failed", "network unreachable")
}
