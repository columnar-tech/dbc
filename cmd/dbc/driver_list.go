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
	"os"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/sourceidentity"
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

func (m DriversList) validateSources() error {
	for id, spec := range m.Drivers {
		if spec.Source == nil {
			continue
		}
		if err := spec.Source.Validate(); err != nil {
			return fmt.Errorf("driver %q source: %w", id, err)
		}
		switch spec.Source.Type {
		case dbc.DriverSourcePackslip, dbc.DriverSourcePath:
			if spec.Prerelease != "" {
				return fmt.Errorf("driver %q %s source does not support prerelease policy", id, spec.Source.Type)
			}
			if spec.Source.Type == dbc.DriverSourcePackslip && spec.Version == nil {
				return fmt.Errorf("driver %q packslip source requires an exact SemVer 2.0.0 version", id)
			}
			if spec.Version != nil {
				versionText := spec.Version.String()
				parsed, err := semver.StrictNewVersion(versionText)
				if err != nil || parsed.String() != versionText {
					return fmt.Errorf("driver %q %s source requires an exact SemVer 2.0.0 version, got %q", id, spec.Source.Type, versionText)
				}
			}
		}
	}
	return nil
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
// path and comparing the resulting source identity keys. Display-only
// fields like RegistryEntry.Name are ignored because they don't appear
// in the effective registry identity comparison.
func registriesChanged(a, b DriversList) bool {
	keysA, errA := effectiveRegistryKeys(a)
	keysB, errB := effectiveRegistryKeys(b)
	// If either side fails to produce a merged set (e.g. invalid config),
	// treat as changed so the command aborts rather than writing against
	// a config we can't analyze.
	if errA != nil || errB != nil {
		return true
	}
	if len(keysA) != len(keysB) {
		return true
	}
	for i := range keysA {
		if keysA[i] != keysB[i] {
			return true
		}
	}
	return false
}

// effectiveRegistryKeys returns the source identity keys the client would use
// after merging the given DriversList with the current global config and
// built-in defaults. It builds a throwaway client via newDBCClient so merge
// semantics stay in sync with what NewClient actually uses.
func effectiveRegistryKeys(list DriversList) ([]sourceidentity.Key, error) {
	c, err := newDBCClient(list.Registries, list.ReplaceDefaults)
	if err != nil {
		return nil, err
	}
	regs := c.Registries()
	out := make([]sourceidentity.Key, 0, len(regs))
	for _, r := range regs {
		if r.BaseURL == nil {
			continue
		}
		key, err := sourceidentity.Parse(sourceidentity.Registry, r.BaseURL.String())
		if err != nil {
			return nil, fmt.Errorf("invalid effective registry URL %q: %w", r.BaseURL.String(), err)
		}
		out = append(out, key)
	}
	return out, nil
}

func driverSourceIdentity(source *dbc.DriverSource) (sourceidentity.Key, error) {
	if source == nil {
		return sourceidentity.Key{}, errors.New("driver source is omitted")
	}
	switch source.Type {
	case dbc.DriverSourceRegistry:
		return sourceidentity.Parse(sourceidentity.Registry, source.URL)
	case dbc.DriverSourcePackslip:
		return sourceidentity.Parse(sourceidentity.Packslip, source.Project)
	case dbc.DriverSourcePath:
		return sourceidentity.Parse(sourceidentity.Path, source.Path)
	default:
		return sourceidentity.Key{}, fmt.Errorf("unsupported driver source type %q", source.Type)
	}
}

func lockSourceIdentity(source lockSource) (sourceidentity.Key, error) {
	switch source.Type {
	case string(sourceidentity.Registry):
		return sourceidentity.Parse(sourceidentity.Registry, source.URL)
	case string(sourceidentity.Packslip):
		return sourceidentity.Parse(sourceidentity.Packslip, source.Project)
	case string(sourceidentity.Path):
		return sourceidentity.Parse(sourceidentity.Path, source.Path)
	default:
		return sourceidentity.Key{}, fmt.Errorf("unsupported driver source type %q", source.Type)
	}
}

func sameDriverSourceIdentity(a, b *dbc.DriverSource) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	keyA, err := driverSourceIdentity(a)
	if err != nil {
		return false
	}
	keyB, err := driverSourceIdentity(b)
	return err == nil && keyA == keyB
}

func sameLockSourceIdentity(a, b lockSource) bool {
	keyA, err := lockSourceIdentity(a)
	if err != nil {
		return false
	}
	keyB, err := lockSourceIdentity(b)
	return err == nil && keyA == keyB
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

	var list DriversList
	if err := toml.NewDecoder(f).Decode(&list); err != nil {
		return fmt.Errorf("error decoding driver list at %s: %w", p, err)
	}
	if err := list.validateSources(); err != nil {
		return fmt.Errorf("error decoding driver list at %s: %w", p, err)
	}
	return applyProjectRegistries(list)
}

type driverSpec struct {
	Prerelease string              `toml:"prerelease,omitempty"`
	Version    *semver.Constraints `toml:"version"`
	Source     *dbc.DriverSource   `toml:"source,omitempty"`
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
	if err := m.validateSources(); err != nil {
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
	drivers, err := client.Search(context.Background(), "")
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
		pkg.Source = spec.Source

		pkgs = append(pkgs, pkg)
	}

	return pkgs, nil
}
