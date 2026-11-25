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
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/resolution"
)

// lockMismatches returns every way in which a version 2 lock file does not
// match the driver list, one message per difference, ordered by driver name.
// It reads nothing but the two files: no registry configuration and no
// index. An empty result means that dbc lock would keep every entry as it
// is. The error is about invalid input, such as invalid platforms in the
// driver list; lock must be valid, as readLockFile returns it.
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
			return nil, fmt.Errorf("driver %s: invalid locked version: %w", name, err)
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
// dbc sync --locked. It never fetches an index and never changes an entry.
// Every problem that lockMismatches and checkLockedReplay find is reported
// at once, and nothing is planned unless there is none.
func planLocked(p *syncPlanner, list DriversList, lock lockFileV2) ([]plannedDriver, error) {
	mismatches, err := lockMismatches(list, lock)
	if err != nil {
		return nil, err
	}
	replays, problems, err := p.checkLockedReplay(list, lock)
	if err != nil {
		return nil, err
	}
	problems.mismatches = append(mismatches, problems.mismatches...)
	if len(problems.mismatches) > 0 || len(problems.trust) > 0 {
		return nil, &problems
	}
	planned := make([]plannedDriver, len(replays))
	for i, r := range replays {
		planned[i] = plannedDriver{Name: r.entry.Name, Entry: r.entry, ArchiveSize: r.size, Signer: r.signer, Package: r.pkg}
	}
	return planned, nil
}

// lockedReplay is what checkLockedReplay found to install a driver from its
// lock entry.
type lockedReplay struct {
	entry  lockedDriver
	pkg    dbc.PkgInfo
	size   *int64
	signer *dbc.SigningKey
}

// checkLockedReplay checks that the lock entry of every driver of the driver
// list can be installed on this platform as it is: the platform is declared,
// the entry has an artifact for it, its registry is configured and its signer
// is trusted. Each check runs whenever what it needs is there, so one
// problem never hides another; only the artifact URL needs the registry. It
// returns the replays only when it found no problem. Entries of drivers that
// the driver list does not have are left to lockMismatches.
//
// lock must be valid, as readLockFile returns it; an invalid locked version
// is an error, not a problem.
func (p *syncPlanner) checkLockedReplay(list DriversList, lock lockFileV2) ([]lockedReplay, lockedSyncError, error) {
	var problems lockedSyncError
	platforms, err := list.targetPlatforms()
	if err != nil {
		return nil, problems, err
	}
	p.platforms = platforms
	host, err := resolution.TargetFromPlatformTuple(p.target)
	if err != nil {
		return nil, problems, err
	}
	// An undeclared platform is one problem of the project; no driver is
	// reported for lacking an artifact for it.
	hostDeclared := platforms == nil || p.hostDeclared()
	if !hostDeclared {
		problems.mismatches = append(problems.mismatches, undeclaredHostError(p.target, platforms).Error())
	}

	var replays []lockedReplay
	drivers := slices.Clone(lock.Drivers)
	slices.SortFunc(drivers, func(a, b lockedDriver) int { return strings.Compare(a.Name, b.Name) })
	for _, d := range drivers {
		if _, ok := list.Drivers[d.Name]; !ok {
			continue
		}
		v, err := semver.NewVersion(d.Version)
		if err != nil {
			return nil, problems, fmt.Errorf("driver %s: invalid locked version: %w", d.Name, err)
		}
		before := len(problems.mismatches) + len(problems.trust)

		signer, err := trustedSigner(d.Name, d.Signer)
		if err != nil {
			problems.trust = append(problems.trust, err.Error())
		}
		bound, err := p.bind(d.Name, d.Source.URL)
		bindOK := err == nil
		if !bindOK {
			problems.trust = append(problems.trust, err.Error())
		}
		if !hostDeclared {
			continue
		}
		i := slices.IndexFunc(d.Artifacts, func(a lockedArtifact) bool { return a.Target == host })
		if i < 0 {
			problems.mismatches = append(problems.mismatches, fmt.Sprintf("driver %s %s has no artifact for this platform (%s) in the lock file",
				d.Name, d.Version, p.target))
			continue
		}
		if !bindOK {
			continue
		}
		pkg, err := p.lockedPackage(d.Name, bound, d.Artifacts[i], v)
		if err != nil {
			problems.mismatches = append(problems.mismatches, err.Error())
		}
		if len(problems.mismatches)+len(problems.trust) == before {
			replays = append(replays, lockedReplay{entry: d, pkg: pkg, size: d.Artifacts[i].Size, signer: signer})
		}
	}
	if len(problems.mismatches) > 0 || len(problems.trust) > 0 {
		return nil, problems, nil
	}
	return replays, problems, nil
}
