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
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
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
	ReplaceDefaults *bool `toml:"replace_defaults,omitempty"`
	// SigningKeys are the keys that Registries can name in signing_key.
	SigningKeys map[string]dbc.SigningKeyDefinition `toml:"signing_keys,omitempty"`
	Drivers     map[string]driverSpec               `toml:"drivers" comment:"dbc driver list"`
	// Network is decoded only to reject it: network settings are read from
	// the user config, never from a project's dbc.toml.
	Network map[string]any `toml:"network,omitempty"`

	// path is the absolute path of the file the list was decoded from. A
	// relative public_key_file is resolved against its directory.
	path string
}

// decodeDriverList decodes a driver list read from the file at path and
// checks its registry configuration, so that a command never works with,
// or writes back, registries or signing keys that are invalid.
func decodeDriverList(r io.Reader, path string) (DriversList, error) {
	var list DriversList
	if err := toml.NewDecoder(r).Decode(&list); err != nil {
		return DriversList{}, err
	}
	list.path = path
	if abs, err := filepath.Abs(path); err == nil {
		list.path = abs
	}
	if project := list.projectConfig(); project != nil {
		if err := project.Validate(); err != nil {
			return DriversList{}, fmt.Errorf("%s: %w", path, err)
		}
	}
	return list, nil
}

// projectConfig returns the registry configuration of the list, or nil when
// it declares none, so that the global config and the defaults apply as is.
func (l DriversList) projectConfig() *dbc.ProjectConfig {
	if len(l.Registries) == 0 && l.ReplaceDefaults == nil && len(l.SigningKeys) == 0 {
		return nil
	}
	return &dbc.ProjectConfig{
		Registries:      l.Registries,
		ReplaceDefaults: l.ReplaceDefaults,
		SigningKeys:     l.SigningKeys,
		Path:            l.path,
	}
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
// path and comparing the resulting normalized URLs and the signing policy
// of each registry. Display-only fields like RegistryEntry.Name and signing
// key definitions that no registry names are ignored.
func registriesChanged(a, b DriversList) bool {
	regsA, errA := effectiveRegistries(a)
	regsB, errB := effectiveRegistries(b)
	// If either side fails to produce a merged set (e.g. invalid config),
	// treat as changed so the command aborts rather than writing against
	// a config we can't analyze.
	if errA != nil || errB != nil {
		return true
	}
	if len(regsA) != len(regsB) {
		return true
	}
	for i := range regsA {
		if regsA[i].url != regsB[i].url || !regsA[i].signing.Equal(regsB[i].signing) {
			return true
		}
	}
	return false
}

type effectiveRegistry struct {
	url     string
	signing dbc.RegistrySigningPolicy
}

// effectiveRegistries returns the registries, with normalized URLs, that the
// client would use after merging the given DriversList with the current
// global config and built-in defaults. It builds a throwaway client via
// newDBCClient so merge semantics stay in sync with what NewClient actually
// uses.
func effectiveRegistries(list DriversList) ([]effectiveRegistry, error) {
	c, err := newDBCClient(list.projectConfig())
	if err != nil {
		return nil, err
	}
	regs := c.Registries()
	out := make([]effectiveRegistry, len(regs))
	for i, r := range regs {
		out[i].signing = r.Signing
		if r.BaseURL != nil {
			out[i].url = normalizeRegistryURL(r.BaseURL.String())
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
	if list.Network != nil {
		return dbc.ErrProjectNetwork
	}
	project := list.projectConfig()
	if project == nil {
		return nil
	}
	c, err := newDBCClient(project)
	if err != nil {
		return fmt.Errorf("error configuring project registries: %w", err)
	}
	setDBCClient(c)
	return nil
}

// applyProjectRegistriesFromCWD loads the registry overrides from a dbc.toml in
// the current working directory (if present) and applies them to the
// process-wide client, so read-only discovery commands (search, info, docs)
// resolve drivers against the same registry set that `dbc add`/`dbc sync` use
// in the same project. This keeps behavior consistent between project-level and
// global config: a project that adds registries or sets replace_defaults
// affects what those commands can see, not just add/sync.
//
// A missing dbc.toml is not an error — these commands must still work outside a
// project, falling back to the global + built-in default registries. A dbc.toml
// that exists but can't be decoded is a hard error (with its path) so the user
// isn't silently shown the wrong registry set. Unlike add/sync, this read is
// not taken under the project lock: these commands never mutate dbc.toml, so a
// best-effort snapshot is acceptable.
//
// DBC_BASE_URL overrides all registry configuration, so when it's set this is a
// no-op that never touches dbc.toml — otherwise a malformed project file would
// block these commands even though DBC_BASE_URL is the documented escape hatch
// for recovering from broken registry config.
func applyProjectRegistriesFromCWD() error {
	if os.Getenv("DBC_BASE_URL") != "" {
		return nil
	}

	p, err := driverListPath("./dbc.toml")
	if err != nil {
		return err
	}

	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("error opening driver list at %s: %w", p, err)
	}
	defer f.Close()

	list, err := decodeDriverList(f, p)
	if err != nil {
		return fmt.Errorf("error decoding driver list at %s: %w", p, err)
	}
	return applyProjectRegistries(list)
}

type driverSpec struct {
	Prerelease string              `toml:"prerelease,omitempty"`
	Version    *semver.Constraints `toml:"version"`
}

func (spec driverSpec) withPrerelease() driverSpec {
	if spec.Version != nil && spec.Prerelease == "allow" {
		constraint := *spec.Version
		constraint.IncludePrerelease = true
		spec.Version = &constraint
	}
	return spec
}

func GetDriverList(fname string) ([]dbc.PkgInfo, error) {
	f, err := os.Open(fname)
	if err != nil {
		return nil, fmt.Errorf("error opening driver list %s: %w", fname, err)
	}
	defer f.Close()
	m, err := decodeDriverList(f, fname)
	if err != nil {
		return nil, fmt.Errorf("error decoding driver list %s: %w", fname, err)
	}

	if m.Network != nil {
		return nil, fmt.Errorf("error decoding driver list %s: %w", fname, dbc.ErrProjectNetwork)
	}

	// Build a per-call client scoped to this list's registry overrides so
	// repeated calls in the same process don't leak configuration from one
	// dbc.toml to another. Unlike add/sync (which own the process for one
	// command), GetDriverList is a library helper that may be called
	// multiple times.
	client, err := newDBCClient(m.projectConfig())
	if err != nil {
		return nil, fmt.Errorf("error configuring project registries: %w", err)
	}
	drivers, err := client.Search(context.Background(), "")
	if err != nil {
		return nil, err
	}

	var pkgs []dbc.PkgInfo
	for name, spec := range m.Drivers {
		spec = spec.withPrerelease()
		pkg, err := dbc.ResolvePackage(drivers, nil, name, func(candidate *dbc.Driver) (dbc.PkgInfo, error) {
			if spec.Version != nil {
				return candidate.GetWithConstraint(spec.Version, config.PlatformTuple())
			}
			return candidate.GetLatestPackage(config.PlatformTuple(), spec.Prerelease == "allow")
		})
		if err != nil {
			if pkg.Driver.Path == "" {
				return nil, fmt.Errorf("driver `%s` not found", name)
			}
			return nil, fmt.Errorf("error finding version for driver %s: %w", name, err)
		}

		pkgs = append(pkgs, pkg)
	}

	return pkgs, nil
}
