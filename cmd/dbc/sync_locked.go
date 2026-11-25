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
	"maps"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/internal/resolution"
)

// lockMismatches returns every way in which a version 2 lock file does not
// match the driver list, one message per difference, ordered by driver name.
// It reads nothing but the two files: no registry configuration and no
// index. An empty result means that dbc lock would keep every entry as it
// is. The error is about the driver list itself, such as invalid platforms.
func lockMismatches(list DriversList, lock lockFileV2) ([]string, error) {
	declared, err := list.targetPlatforms()
	if err != nil {
		return nil, err
	}
	var mismatches []string
	if !sameTargets(declared, lock.Platforms) {
		mismatches = append(mismatches, platformsMismatch(declared, lock.Platforms))
	}

	locked := make(map[string]lockedDriver, len(lock.Drivers))
	for _, d := range lock.Drivers {
		locked[d.Name] = d
	}
	names := slices.Collect(maps.Keys(list.Drivers))
	for name := range locked {
		if _, ok := list.Drivers[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	for _, name := range names {
		spec, listed := list.Drivers[name]
		d, isLocked := locked[name]
		switch {
		case !isLocked:
			mismatches = append(mismatches, fmt.Sprintf("driver %s is in the driver list but not in the lock file", name))
			continue
		case !listed:
			mismatches = append(mismatches, fmt.Sprintf("driver %s is in the lock file but not in the driver list", name))
			continue
		}
		spec = spec.withPrerelease()
		if spec.Source != nil {
			// Unlike validateDriverSources, an invalid source is one more
			// difference to report, not the end of the check.
			target, err := spec.Source.target(list.Registries)
			switch {
			case err != nil:
				mismatches = append(mismatches, fmt.Sprintf("driver %s: %v", name, err))
			case target.identity != d.Source.URL:
				mismatches = append(mismatches, fmt.Sprintf("driver %s: the driver list takes it from %s, but the lock file has it from %s",
					name, target.identity, d.Source.URL))
			}
		}
		v, err := semver.NewVersion(d.Version)
		if err != nil {
			mismatches = append(mismatches, fmt.Sprintf("driver %s: invalid locked version: %v", name, err))
			continue
		}
		if !lockSatisfies(spec, v) {
			mismatches = append(mismatches, versionMismatch(name, spec, v))
		}
	}
	return mismatches, nil
}

// sameTargets reports whether two sets of canonical targets are equal. A
// lock file need not list its platforms in order.
func sameTargets(a, b []resolution.Target) bool {
	if len(a) != len(b) {
		return false
	}
	a, b = slices.Clone(a), slices.Clone(b)
	sortTargets(a)
	sortTargets(b)
	return slices.Equal(a, b)
}

func platformsMismatch(declared, locked []resolution.Target) string {
	describe := func(targets []resolution.Target) string {
		if len(targets) == 0 {
			return "no platforms"
		}
		targets = slices.Clone(targets)
		sortTargets(targets)
		return "platforms " + platformList(targets)
	}
	return fmt.Sprintf("the driver list declares %s, but the lock file was created for %s", describe(declared), describe(locked))
}

func versionMismatch(name string, spec driverSpec, v *semver.Version) string {
	if spec.Version != nil {
		return fmt.Sprintf("driver %s: version %s in the lock file does not satisfy %s", name, v, spec.Version)
	}
	return fmt.Sprintf("driver %s: version %s in the lock file is a pre-release, which the driver list does not allow", name, v)
}

// lockedSyncError reports why dbc sync --locked cannot install from a lock
// file. Mismatches between the lock file and the driver list are fixed by
// dbc lock; trust problems, such as a key that the lock file pins but the
// configuration does not trust, by checking that configuration first.
type lockedSyncError struct {
	mismatches []string
	trust      []string
}

func (e *lockedSyncError) Error() string {
	var b strings.Builder
	if len(e.mismatches) > 0 {
		b.WriteString("the lock file does not match the driver list, so dbc sync --locked cannot install from it:\n")
		writeItems(&b, e.mismatches)
		b.WriteString("run `dbc lock` to update the lock file, or `dbc sync` without --locked")
	}
	if len(e.trust) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("the configuration does not accept the lock file:\n")
		writeItems(&b, e.trust)
		b.WriteString("check the registries and signing keys in the configuration; updating the lock file would change what it pins")
	}
	return b.String()
}

func writeItems(b *strings.Builder, items []string) {
	for _, item := range items {
		fmt.Fprintf(b, "  - %s\n", item)
	}
}

// errLockedNoLockFile is returned by dbc sync --locked without a lock file.
var errLockedNoLockFile = errors.New("dbc sync --locked needs a lock file; run `dbc lock` to create it")

// planLocked plans every driver of the driver list from its lock entry, for
// dbc sync --locked. It never fetches an index and never changes an entry:
// each driver must have an artifact for this platform in the lock file. All
// drivers are checked before anything is downloaded, and every problem is
// reported at once. The lock file must already match the driver list; see
// lockMismatches.
func (p *syncPlanner) planLocked(list DriversList, lock lockFileV2) ([]plannedDriver, error) {
	platforms, err := list.targetPlatforms()
	if err != nil {
		return nil, err
	}
	p.platforms = platforms
	if platforms != nil && !p.hostDeclared() {
		return nil, undeclaredHostError(p.target, platforms)
	}
	host, err := resolution.TargetFromPlatformTuple(p.target)
	if err != nil {
		return nil, err
	}

	var (
		planned []plannedDriver
		failed  lockedSyncError
	)
	drivers := slices.Clone(lock.Drivers)
	slices.SortFunc(drivers, func(a, b lockedDriver) int { return strings.Compare(a.Name, b.Name) })
	for _, d := range drivers {
		i := slices.IndexFunc(d.Artifacts, func(a lockedArtifact) bool { return a.Target == host })
		if i < 0 {
			failed.mismatches = append(failed.mismatches, fmt.Sprintf("driver %s %s has no artifact for this platform (%s) in the lock file",
				d.Name, d.Version, p.target))
			continue
		}
		v, err := semver.NewVersion(d.Version)
		if err != nil {
			failed.mismatches = append(failed.mismatches, fmt.Sprintf("driver %s: invalid locked version: %v", d.Name, err))
			continue
		}
		signer, err := trustedSigner(d.Name, d.Signer)
		if err != nil {
			failed.trust = append(failed.trust, err.Error())
			continue
		}
		bound, err := p.bind(d.Name, d.Source.URL)
		if err != nil {
			failed.trust = append(failed.trust, err.Error())
			continue
		}
		pd, err := p.replayArtifact(d, bound, signer, d.Artifacts[i], v)
		if err != nil {
			failed.mismatches = append(failed.mismatches, err.Error())
			continue
		}
		planned = append(planned, pd)
	}
	if len(failed.mismatches) > 0 || len(failed.trust) > 0 {
		return nil, &failed
	}
	return planned, nil
}

// planLocked checks that lock matches list and plans every driver from it;
// see lockMismatches and syncPlanner.planLocked.
func planLocked(p *syncPlanner, list DriversList, lock lockFileV2) ([]plannedDriver, error) {
	mismatches, err := lockMismatches(list, lock)
	if err != nil {
		return nil, err
	}
	if len(mismatches) > 0 {
		return nil, &lockedSyncError{mismatches: mismatches}
	}
	return p.planLocked(list, lock)
}
