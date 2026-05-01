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
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/pelletier/go-toml/v2"
)

type DriversList struct {
	Registries []dbc.RegistryEntry `toml:"registries,omitempty"`
	// ReplaceDefaults is a tri-state: nil means "inherit from global config",
	// &true replaces both global and built-in default registries, &false forces
	// defaults back on even when the global config set replace_defaults = true.
	ReplaceDefaults *bool                 `toml:"replace_defaults,omitempty"`
	Drivers         map[string]driverSpec `toml:"drivers" comment:"dbc driver list"`
}

// registriesChanged reports whether two DriversList values would produce
// a different EFFECTIVE registry resolution when combined with the
// current process-wide globalRegistryConfig and built-in defaults. This
// is what the client actually uses to resolve drivers, so changes that
// the merge would collapse (e.g. flipping nil → &false when defaults
// were already inherited, or removing an exact duplicate entry) are
// correctly NOT treated as drift.
//
// Implemented by running both lists through the same newDBCClient merge
// path and comparing the resulting normalized URL sets. Display-only
// fields like RegistryEntry.Name are ignored because they don't appear
// in the merged URL comparison.
func registriesChanged(a, b DriversList) bool {
	urlsA, errA := effectiveRegistryURLs(a)
	urlsB, errB := effectiveRegistryURLs(b)
	// If either side fails to produce a merged set (e.g. invalid config),
	// treat as changed so the command aborts rather than writing against
	// a config we can't analyze.
	if errA != nil || errB != nil {
		return errA != errB
	}
	if len(urlsA) != len(urlsB) {
		return true
	}
	for i := range urlsA {
		if urlsA[i] != urlsB[i] {
			return true
		}
	}
	return false
}

// effectiveRegistryURLs returns the normalized URL list the client would
// use after merging the given DriversList with the current global config
// and built-in defaults. It builds a throwaway client via newDBCClient
// so merge semantics stay in sync with what NewClient actually uses.
func effectiveRegistryURLs(list DriversList) ([]string, error) {
	c, err := newDBCClient(list.Registries, list.ReplaceDefaults)
	if err != nil {
		return nil, err
	}
	regs := c.Registries()
	out := make([]string, len(regs))
	for i, r := range regs {
		if r.BaseURL != nil {
			out[i] = normalizeRegistryURL(r.BaseURL.String())
		}
	}
	return out, nil
}

// normalizeRegistryURL returns a canonical form of a registry URL for
// equality comparisons. Only truly no-op differences are collapsed:
//
//   - scheme and host are lowercased (case-insensitive per RFC 3986)
//   - a trailing slash on the path is stripped
//   - fragments are dropped (not sent on HTTP requests)
//
// Query, userinfo, and path segments are preserved because they can
// change the effective registry endpoint (tenant selector in query,
// credential-bearing userinfo, path-addressed registry mount points).
// A concurrent edit that flips any of those MUST still trigger the
// config-drift abort in dbc add.
func normalizeRegistryURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

// applyProjectRegistries rebuilds the process-wide dbc client with the
// registry overrides declared in the project's dbc.toml, so subsequent calls
// through getDriverRegistry see the merged registry list. No-op when neither
// registries nor replace_defaults are set.
func applyProjectRegistries(list DriversList) error {
	if len(list.Registries) == 0 && list.ReplaceDefaults == nil {
		return nil
	}
	c, err := newDBCClient(list.Registries, list.ReplaceDefaults)
	if err != nil {
		return fmt.Errorf("error configuring project registries: %w", err)
	}
	setDBCClient(c)
	return nil
}

type driverSpec struct {
	Prerelease string              `toml:"prerelease,omitempty"`
	Version    *semver.Constraints `toml:"version"`
}

func GetDriverList(fname string) ([]dbc.PkgInfo, error) {
	var m DriversList
	f, err := os.Open(fname)
	if err != nil {
		return nil, fmt.Errorf("error opening driver list %s: %w", fname, err)
	}
	defer f.Close()
	if err = toml.NewDecoder(f).Decode(&m); err != nil {
		return nil, fmt.Errorf("error decoding driver list %s: %w", fname, err)
	}

	// Build a per-call client scoped to this list's registry overrides so
	// repeated calls in the same process don't leak configuration from one
	// dbc.toml to another. Unlike add/sync (which own the process for one
	// command), GetDriverList is a library helper that may be called
	// multiple times.
	client, err := newDBCClient(m.Registries, m.ReplaceDefaults)
	if err != nil {
		return nil, fmt.Errorf("error configuring project registries: %w", err)
	}
	drivers, err := client.Search("")
	if err != nil {
		return nil, err
	}

	// create mapping to avoid multiple loops through
	dmap := make(map[string]dbc.Driver)
	for _, driver := range drivers {
		dmap[driver.Path] = driver
	}

	var pkgs []dbc.PkgInfo
	for name, spec := range m.Drivers {
		drv, ok := dmap[name]
		if !ok {
			return nil, fmt.Errorf("driver `%s` not found", name)
		}

		pkg, err := drv.GetWithConstraint(spec.Version, config.PlatformTuple())
		if err != nil {
			return nil, fmt.Errorf("error finding version for driver %s: %w", name, err)
		}

		pkgs = append(pkgs, pkg)
	}

	return pkgs, nil
}
