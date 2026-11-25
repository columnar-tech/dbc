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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	firstRegistryURL  = "https://first.example.com"
	secondRegistryURL = "https://second.example.com"
)

// twoRegistries serves the test index from two registries. The first, which
// has priority, has only version 1.0.0 of test-driver-1; the second has all
// versions. merged counts fetches of the merged index.
type twoRegistries struct {
	first, second dbc.Registry
	merged        int
	failSecond    bool
}

func newTwoRegistries() *twoRegistries {
	return &twoRegistries{
		first:  dbc.Registry{BaseURL: must(url.Parse(firstRegistryURL))},
		second: dbc.Registry{BaseURL: must(url.Parse(secondRegistryURL))},
	}
}

func (r *twoRegistries) index(reg *dbc.Registry, onlyFirstVersion bool) ([]dbc.Driver, error) {
	drivers, err := getTestDriverRegistry()
	if err != nil {
		return nil, err
	}
	for i := range drivers {
		drivers[i].Registry = reg
		if onlyFirstVersion && drivers[i].Path == "test-driver-1" {
			drivers[i].PkgInfo = drivers[i].PkgInfo[:1]
		}
	}
	return drivers, nil
}

func (r *twoRegistries) base() baseModel {
	return baseModel{
		downloadPkg: downloadTestPkg,
		getDriverRegistry: func() ([]dbc.Driver, error) {
			r.merged++
			first, err := r.index(&r.first, true)
			if err != nil {
				return nil, err
			}
			second, err := r.index(&r.second, false)
			if err != nil {
				return nil, err
			}
			return append(first, second...), nil
		},
		getRegistries: func() ([]dbc.Registry, error) { return []dbc.Registry{r.first, r.second}, nil },
		getRegistryIndex: func(reg dbc.Registry) ([]dbc.Driver, error) {
			switch reg.BaseURL.String() {
			case firstRegistryURL:
				return r.index(&r.first, true)
			case secondRegistryURL:
				if r.failSecond {
					return nil, errors.New("second registry is down")
				}
				return r.index(&r.second, false)
			}
			return nil, errors.New("unexpected registry " + reg.BaseURL.String())
		},
	}
}

func (suite *SubcommandTestSuite) writeList(content string) string {
	p := filepath.Join(suite.tempdir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(p, []byte(content), 0o644))
	return p
}

const secondByName = "[[registries]]\nurl = '" + secondRegistryURL + "'\nname = 'second'\n\n"

func (suite *SubcommandTestSuite) TestSyncResolvesFromTheSourceRegistryOnly() {
	for name, source := range map[string]string{
		"name": "{ type = 'registry', name = 'second' }",
		"url":  "{ type = 'registry', url = '" + secondRegistryURL + "/' }",
	} {
		suite.Run(name, func() {
			regs := newTwoRegistries()
			listPath := suite.writeList(secondByName + "[drivers]\n[drivers.test-driver-1]\nsource = " + source + "\n")
			suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(regs.base()))

			// Without a source the first registry would win with 1.0.0.
			suite.driverIsInstalledWithVersion("test-driver-1", "1.1.0", true)
			lock := suite.readV2Lock()
			suite.Require().Len(lock.Drivers, 1)
			suite.Equal(secondRegistryURL, lock.Drivers[0].Source.URL)
			suite.Zero(regs.merged, "a driver with a source must not use the merged index")
			suite.Require().NoError(os.Remove(suite.lockPath()))
		})
	}
}

func (suite *SubcommandTestSuite) TestSyncSourceDoesNotFallBack() {
	regs := newTwoRegistries()
	listPath := suite.writeList("[drivers]\n[drivers.test-driver-1]\nversion = '>=1.1'\nsource = { type = 'registry', url = '" + firstRegistryURL + "' }\n")
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(regs.base()))
	suite.Contains(out, "registry "+firstRegistryURL)
	suite.NoFileExists(suite.lockPath())

	listPath = suite.writeList("[drivers]\n[drivers.test-driver-3]\nsource = { type = 'registry', url = '" + firstRegistryURL + "' }\n")
	out = suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(regs.base()))
	suite.Contains(out, "driver test-driver-3 is not in registry "+firstRegistryURL)
}

func (suite *SubcommandTestSuite) TestSyncSourceIndexFailureIsAnError() {
	regs := newTwoRegistries()
	regs.failSecond = true
	listPath := suite.writeList("[drivers]\n[drivers.test-driver-1]\nsource = { type = 'registry', url = '" + secondRegistryURL + "' }\n")
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(regs.base()))
	suite.Contains(out, "could not fetch the index of registry "+secondRegistryURL)
	suite.Contains(out, "second registry is down")
	suite.Zero(regs.merged)
}

func (suite *SubcommandTestSuite) TestSyncStopsWhenTheSourceNoLongerMatchesTheLock() {
	regs := newTwoRegistries()
	listPath := suite.writeList(secondByName + "[drivers]\n[drivers.test-driver-1]\nsource = { type = 'registry', name = 'second' }\n")
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(regs.base()))
	suite.Equal(secondRegistryURL, suite.readV2Lock().Drivers[0].Source.URL)

	// The same registry by URL instead of by name reuses the lock.
	suite.writeList(secondByName + "[drivers]\n[drivers.test-driver-1]\nsource = { type = 'registry', url = '" + secondRegistryURL + "' }\n")
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(regs.base()))
	suite.driverIsInstalledWithVersion("test-driver-1", "1.1.0", true)

	// Another registry needs dbc lock.
	suite.writeList(secondByName + "[drivers]\n[drivers.test-driver-1]\nsource = { type = 'registry', url = '" + firstRegistryURL + "' }\n")
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(regs.base()))
	suite.Contains(out, "run `dbc lock`")
	suite.Equal(secondRegistryURL, suite.readV2Lock().Drivers[0].Source.URL)

	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(regs.base()))
	lock := suite.readV2Lock()
	suite.Equal(firstRegistryURL, lock.Drivers[0].Source.URL)
	suite.Equal("1.0.0", lock.Drivers[0].Version)

	// Without a source the lock entry is replayed from where it was locked.
	suite.writeList("[drivers]\n[drivers.test-driver-1]\n")
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(regs.base()))
	suite.Equal(firstRegistryURL, suite.readV2Lock().Drivers[0].Source.URL)
}

func (suite *SubcommandTestSuite) TestSyncRejectsInvalidSources() {
	query := "https://second.example.com?tenant=a"
	for name, tc := range map[string]struct{ list, want string }{
		"name and url":  {secondByName + "[drivers.test-driver-1]\nsource = { type = 'registry', name = 'second', url = '" + secondRegistryURL + "' }\n", "either name or url, not both"},
		"neither":       {"[drivers.test-driver-1]\nsource = { type = 'registry' }\n", "must have name or url"},
		"unknown type":  {"[drivers.test-driver-1]\nsource = { type = 'http', url = 'https://x.example.com' }\n", `source type "http" is not supported`},
		"no type":       {"[drivers.test-driver-1]\nsource = { url = '" + secondRegistryURL + "' }\n", `source type "" is not supported`},
		"unknown name":  {"[drivers.test-driver-1]\nsource = { type = 'registry', name = 'second' }\n", `registry "second", which [[registries]] in the same file does not declare`},
		"empty name":    {secondByName + "[drivers.test-driver-1]\nsource = { type = 'registry', name = '' }\n", `registry "", which`},
		"query in url":  {"[drivers.test-driver-1]\nsource = { type = 'registry', url = '" + query + "' }\n", "must not contain user information, a query or a fragment"},
		"user in url":   {"[drivers.test-driver-1]\nsource = { type = 'registry', url = 'https://u:p@second.example.com' }\n", "must not contain user information"},
		"fragment":      {"[drivers.test-driver-1]\nsource = { type = 'registry', url = 'https://second.example.com#' }\n", "a fragment"},
		"not a url":     {"[drivers.test-driver-1]\nsource = { type = 'registry', url = 'second' }\n", "absolute http or https URL"},
		"not in config": {"[drivers.test-driver-1]\nsource = { type = 'registry', url = 'https://third.example.com' }\n", "registry https://third.example.com (https://third.example.com) is not among the configured registries"},
	} {
		suite.Run(name, func() {
			listPath := suite.writeList(tc.list)
			out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(newTwoRegistries().base()))
			suite.Contains(out, tc.want)
			suite.NotContains(out, "u:p@")
			suite.NoFileExists(suite.lockPath())
		})
	}
}

func (suite *SubcommandTestSuite) TestSyncRejectsAnAmbiguousSource() {
	regs := newTwoRegistries()
	base := regs.base()
	tenantA := dbc.Registry{BaseURL: must(url.Parse(secondRegistryURL + "?tenant=a"))}
	tenantB := dbc.Registry{BaseURL: must(url.Parse(secondRegistryURL + "?tenant=b"))}
	base.getRegistries = func() ([]dbc.Registry, error) { return []dbc.Registry{tenantA, tenantB}, nil }
	listPath := suite.writeList("[[registries]]\nurl = '" + secondRegistryURL + "?tenant=a'\nname = 'a'\n" +
		"[[registries]]\nurl = '" + secondRegistryURL + "?tenant=b'\nname = 'b'\n\n" +
		"[drivers.test-driver-1]\nsource = { type = 'registry', name = 'a' }\n")
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(base))
	suite.Contains(out, "matches 2 configured registries")
	suite.NotContains(out, "tenant=")
}

func (suite *SubcommandTestSuite) TestAddRecordsAndKeepsTheSource() {
	regs := newTwoRegistries()
	listPath := suite.writeList(secondByName + "[drivers]\n")

	suite.runCmd(AddCmd{Path: listPath, Driver: []string{"test-driver-1"}, Registry: "second"}.GetModelCustom(regs.base()))
	data, err := os.ReadFile(listPath)
	suite.Require().NoError(err)
	suite.Contains(string(data), "[drivers.test-driver-1]\nsource = {type = 'registry', name = 'second'}\n")
	suite.Zero(regs.merged)

	// A version constraint that only the second registry satisfies shows
	// that the kept source is also used to resolve.
	suite.runCmd(AddCmd{Path: listPath, Driver: []string{"test-driver-1>=1.1"}}.GetModelCustom(regs.base()))
	data, err = os.ReadFile(listPath)
	suite.Require().NoError(err)
	suite.Contains(string(data), "source = {type = 'registry', name = 'second'}")
	suite.Contains(string(data), "version = '>=1.1'")

	suite.runCmd(AddCmd{Path: listPath, Driver: []string{"test-driver-1"}, Registry: firstRegistryURL}.GetModelCustom(regs.base()))
	data, err = os.ReadFile(listPath)
	suite.Require().NoError(err)
	suite.Contains(string(data), "source = {type = 'registry', url = '"+firstRegistryURL+"'}")

	// Adding another driver and removing one keep the source.
	suite.runCmd(AddCmd{Path: listPath, Driver: []string{"test-driver-2"}}.GetModelCustom(regs.base()))
	suite.runCmd(RemoveCmd{Path: listPath, Driver: "test-driver-2"}.GetModel())
	data, err = os.ReadFile(listPath)
	suite.Require().NoError(err)
	suite.Contains(string(data), "source = {type = 'registry', url = '"+firstRegistryURL+"'}")

	var list DriversList
	suite.Require().NoError(decodeTOMLFile(listPath, &list))
	suite.Require().NoError(validateDriverSources(list))
}

func (suite *SubcommandTestSuite) TestAddSourceErrors() {
	regs := newTwoRegistries()
	listPath := suite.writeList(secondByName + "[drivers]\n")
	out := suite.runCmdErr(AddCmd{Path: listPath, Driver: []string{"test-driver-1"}, Registry: "nope"}.GetModelCustom(regs.base()))
	suite.Contains(out, `--registry: source refers to registry "nope"`)

	out = suite.runCmdErr(AddCmd{Path: listPath, Driver: []string{"test-driver-1>=1.1"}, Registry: firstRegistryURL}.GetModelCustom(regs.base()))
	suite.Contains(out, "registry "+firstRegistryURL)
	data, err := os.ReadFile(listPath)
	suite.Require().NoError(err)
	suite.NotContains(string(data), "test-driver-1")
}

func (suite *SubcommandTestSuite) TestInstallFromARegistry() {
	regs := newTwoRegistries()
	saved := globalRegistryConfig
	suite.T().Cleanup(func() { globalRegistryConfig = saved })
	globalRegistryConfig = &dbc.GlobalConfig{Registries: []dbc.RegistryEntry{{URL: secondRegistryURL, Name: "second"}}}

	for _, registry := range []string{"second", secondRegistryURL} {
		suite.runCmd(InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, Registry: registry}.GetModelCustom(regs.base()))
		suite.driverIsInstalledWithVersion("test-driver-1", "1.1.0", true)
		suite.runCmd(UninstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.GetModel())
	}
	suite.Zero(regs.merged)

	out := suite.runCmdErr(InstallCmd{Driver: "test-driver-1>=1.1", Level: suite.configLevel, Registry: firstRegistryURL}.GetModelCustom(regs.base()))
	suite.Contains(out, "test-driver-1")
	suite.driverIsNotInstalled("test-driver-1")

	out = suite.runCmdErr(InstallCmd{Driver: "test-driver-3", Level: suite.configLevel, Registry: firstRegistryURL}.GetModelCustom(regs.base()))
	suite.Contains(out, "driver `test-driver-3` is not in registry "+firstRegistryURL)

	out = suite.runCmdErr(InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, Registry: "first"}.GetModelCustom(regs.base()))
	suite.Contains(out, "a name refers to the user config")

	regs.failSecond = true
	out = suite.runCmdErr(InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, Registry: "second"}.GetModelCustom(regs.base()))
	suite.Contains(out, "second registry is down")

	out = suite.runCmdErr(InstallCmd{Driver: "driver.tar.gz", Level: suite.configLevel, Registry: "second"}.GetModelCustom(regs.base()))
	suite.Contains(out, "--registry cannot be used to install a local archive")
}

func TestDriverSourceTOMLRoundTrip(t *testing.T) {
	name, u := "acme", "https://acme.example.com"
	for _, src := range []*driverSource{
		{Type: sourceTypeRegistry, Name: &name},
		{Type: sourceTypeRegistry, URL: &u},
	} {
		list := DriversList{Drivers: map[string]driverSpec{"x": {Source: src}}}
		p := filepath.Join(t.TempDir(), "dbc.toml")
		require.NoError(t, writeTOMLFile(p, list))
		var got DriversList
		require.NoError(t, decodeTOMLFile(p, &got))
		assert.True(t, src.equal(got.Drivers["x"].Source))
	}
}

func TestSameSourceTarget(t *testing.T) {
	name := "acme"
	src := driverSource{Type: sourceTypeRegistry, Name: &name}
	before := []dbc.RegistryEntry{{URL: "https://acme.example.com", Name: "acme"}}
	assert.True(t, sameSourceTarget(src, before, slices.Clone(before)))
	assert.False(t, sameSourceTarget(src, before, []dbc.RegistryEntry{{URL: "https://other.example.com", Name: "acme"}}))
	assert.False(t, sameSourceTarget(src, before, []dbc.RegistryEntry{{URL: "https://acme.example.com?tenant=b", Name: "acme"}}))
	assert.False(t, sameSourceTarget(src, before, nil))
}

func writeTOMLFile(p string, v any) error {
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(v)
}

func decodeTOMLFile(p string, v any) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewDecoder(f).Decode(v)
}

// TestGetDriverListHonorsTheSource serves the same driver from two
// registries in different versions and selects the second by name.
func TestGetDriverListHonorsTheSource(t *testing.T) {
	index := func(versions ...string) string {
		s := "drivers:\n  - name: X\n    path: x\n    pkginfo:\n"
		for _, v := range versions {
			s += "      - version: v" + v + "\n        packages:\n"
			for _, p := range []string{"linux_amd64", "linux_arm64", "macos_amd64", "macos_arm64", "windows_amd64", "windows_arm64"} {
				s += "          - platform: " + p + "\n            url: x/" + v + "/x.tar.gz\n"
			}
		}
		return s
	}
	serve := func(body string, hits *int) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/index.yaml" {
				http.NotFound(w, r)
				return
			}
			*hits++
			w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	t.Setenv("DBC_BASE_URL", "")
	saved := globalRegistryConfig
	t.Cleanup(func() { globalRegistryConfig = saved })
	globalRegistryConfig = nil

	var hitsFirst, hitsSecond int
	first := serve(index("1.0.0"), &hitsFirst)
	second := serve(index("1.0.0", "2.0.0"), &hitsSecond)
	p := filepath.Join(t.TempDir(), "dbc.toml")
	require.NoError(t, os.WriteFile(p, []byte("exclude_builtin_registries = true\n\n"+
		"[[registries]]\nurl = '"+first.URL+"'\n[[registries]]\nurl = '"+second.URL+"'\nname = 'second'\n\n"+
		"[drivers.x]\nsource = { type = 'registry', name = 'second' }\n"), 0o644))

	pkgs, err := GetDriverList(p)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "2.0.0", pkgs[0].Version.String())
	assert.Zero(t, hitsFirst, "only the source registry is fetched")
	assert.Equal(t, 1, hitsSecond)
}

func (suite *SubcommandTestSuite) TestAddDetectsAConcurrentChangeOfTheSourceRegistry() {
	regs := newTwoRegistries()
	both := func(firstName, secondName string) string {
		return "[[registries]]\nurl = '" + firstRegistryURL + "'\nname = '" + firstName + "'\n" +
			"[[registries]]\nurl = '" + secondRegistryURL + "'\nname = '" + secondName + "'\n\n[drivers]\n"
	}
	listPath := suite.writeList(both("first", "second"))
	base := regs.base()
	fetch := base.getRegistryIndex
	base.getRegistryIndex = func(reg dbc.Registry) ([]dbc.Driver, error) {
		// Another process swaps the names meanwhile; the registry URLs and
		// their order stay the same.
		suite.writeList(both("second", "first"))
		return fetch(reg)
	}
	out := suite.runCmdErr(AddCmd{Path: listPath, Driver: []string{"test-driver-1"}, Registry: "second"}.GetModelCustom(base))
	suite.Contains(out, "the registry that driver test-driver-1 is taken from changed")
	data, err := os.ReadFile(listPath)
	suite.Require().NoError(err)
	suite.NotContains(string(data), "test-driver-1")
}

// TestInstallFromARegistryReplacesTheSameVersion pins that --registry
// installs the selected registry's package even when the same version is
// installed already, since the installed driver does not record its registry.
func (suite *SubcommandTestSuite) TestInstallFromARegistryReplacesTheSameVersion() {
	regs := newTwoRegistries()
	var downloads []string
	base := regs.base()
	base.downloadPkg = func(pkg dbc.PkgInfo) (*os.File, error) {
		downloads = append(downloads, pkg.Driver.Registry.BaseURL.String())
		return downloadTestPkg(pkg)
	}

	suite.runCmd(InstallCmd{Driver: "test-driver-1=1.0.0", Level: suite.configLevel, Registry: firstRegistryURL}.GetModelCustom(base))
	suite.runCmd(InstallCmd{Driver: "test-driver-1=1.0.0", Level: suite.configLevel, Registry: secondRegistryURL}.GetModelCustom(base))
	suite.Equal([]string{firstRegistryURL, secondRegistryURL}, downloads)
	suite.driverIsInstalledWithVersion("test-driver-1", "1.0.0", true)

	// Without --registry an installed version is still kept.
	suite.runCmd(InstallCmd{Driver: "test-driver-1=1.0.0", Level: suite.configLevel}.GetModelCustom(base))
	suite.Len(downloads, 2)
}

// TestIsSelectionError covers the hint for an error that only an explicit
// registry avoids. signer_conflict_test.go tests these errors end to end.
func TestIsSelectionError(t *testing.T) {
	conflict := &dbc.SignerConflictError{Name: "x"}
	assert.True(t, isSelectionError(fmt.Errorf("wrapped: %w", conflict)))
	assert.True(t, isSelectionError(fmt.Errorf("wrapped: %w", dbc.ErrIncompleteIndex)))
	assert.False(t, isSelectionError(dbc.ErrPackageMiss))
	assert.False(t, isSelectionError(errors.New("driver `x` not found")))
}
