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
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/resolution"
)

// targetPlatforms returns the canonical targets of the platforms that the
// driver list declares, sorted, or nil when it declares none.
func (l DriversList) targetPlatforms() ([]resolution.Target, error) {
	if l.Platforms == nil {
		return nil, nil
	}
	if len(l.Platforms) == 0 {
		return nil, errors.New("platforms must not be empty; remove it to resolve drivers without regard to platforms")
	}
	targets := make([]resolution.Target, 0, len(l.Platforms))
	for _, tuple := range l.Platforms {
		t, err := resolution.TargetFromPlatformTuple(tuple)
		if err != nil {
			return nil, fmt.Errorf("platforms: %w", err)
		}
		if slices.Contains(targets, t) {
			return nil, fmt.Errorf("platforms: %q is listed more than once", tuple)
		}
		targets = append(targets, t)
	}
	sortTargets(targets)
	return targets, nil
}

// sortTargets sorts targets in the order the lock file writes them.
func sortTargets(targets []resolution.Target) {
	slices.SortFunc(targets, compareTargets)
}

func compareTargets(x, y resolution.Target) int {
	return strings.Compare(x.OS+"\x00"+x.Arch+"\x00"+x.LibC+"\x00"+x.Variant, y.OS+"\x00"+y.Arch+"\x00"+y.LibC+"\x00"+y.Variant)
}

// platformList names targets for messages, in their tuple spelling.
func platformList(targets []resolution.Target) string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = targetTuple(t)
	}
	return strings.Join(names, ", ")
}

// targetTuple spells t as a platform tuple. The libc of Linux is left out
// when it is the default.
func targetTuple(t resolution.Target) string {
	parts := []string{t.OS, t.Arch}
	if t.LibC != "" && !(t.OS == "linux" && t.LibC == "gnu") {
		parts = append(parts, t.LibC)
	}
	if t.Variant != "" {
		parts = append(parts, t.Variant)
	}
	return strings.Join(parts, "_")
}

// platformPackages returns the packages of exactly version v (build metadata
// included) whose platform tuple denotes target t.
func platformPackages(d dbc.Driver, v *semver.Version, t resolution.Target) []dbc.PackageInfo {
	var out []dbc.PackageInfo
	for _, info := range d.AllVersions() {
		if info.Version == nil || info.Version.String() != v.String() {
			continue
		}
		for _, pkg := range info.Packages {
			if target, err := resolution.TargetFromPlatformTuple(pkg.Platform); err == nil && target == t {
				out = append(out, pkg)
			}
		}
	}
	return out
}

// platformPackage returns the one package of version v for target t. A
// missing package and several packages for the same target (aliases or
// duplicates) are errors.
func platformPackage(d dbc.Driver, v *semver.Version, t resolution.Target) (dbc.PackageInfo, error) {
	switch pkgs := platformPackages(d, v, t); len(pkgs) {
	case 0:
		return dbc.PackageInfo{}, fmt.Errorf("version %s of driver %s has no build for %s", v, d.Path, targetTuple(t))
	case 1:
		return pkgs[0], nil
	default:
		return dbc.PackageInfo{}, fmt.Errorf("version %s of driver %s lists more than one package for %s", v, d.Path, targetTuple(t))
	}
}

// coversPlatforms reports whether version v has a build for every target.
func coversPlatforms(d dbc.Driver, v *semver.Version, targets []resolution.Target) bool {
	for _, t := range targets {
		if len(platformPackages(d, v, t)) == 0 {
			return false
		}
	}
	return true
}

// newestCoveringVersion returns the newest version of d that is eligible for
// spec and has a build for every target. latest is the newest eligible version
// regardless of platforms. A version that has no build for some target is
// not a miss: the next registry would not be asked, so it is a plain error.
func newestCoveringVersion(d dbc.Driver, spec driverSpec, latest *semver.Version, targets []resolution.Target) (*semver.Version, error) {
	if coversPlatforms(d, latest, targets) {
		return latest, nil
	}
	var best *semver.Version
	for _, info := range d.AllVersions() {
		v := info.Version
		if v == nil || !eligibleVersion(spec, v) || (best != nil && !v.GreaterThan(best)) {
			continue
		}
		if coversPlatforms(d, v, targets) {
			best = v
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no version of driver %s that the driver list allows has a build for every platform in platforms (%s)", d.Path, platformList(targets))
	}
	return best, nil
}

// eligibleVersion reports whether v may be selected for spec without a pin,
// like Driver.GetLatestVersionAnyPlatform does.
func eligibleVersion(spec driverSpec, v *semver.Version) bool {
	if spec.Version != nil {
		return spec.Version.Check(v)
	}
	return spec.Prerelease == "allow" || v.Prerelease() == ""
}

// declaredArtifacts records an artifact for every declared target, like
// the artifact of the current target: with the hash the registry publishes,
// or with an empty hash that is measured from the downloaded archive. The
// artifact of the current target comes from current. existing holds artifacts
// of a lock entry for the same source and version, which are kept as they
// are. unmeasured returns the packages of other targets that have to be
// downloaded to measure their hash (see measureDeclared). As without a
// declaration, every other target with a published hash is recorded too.
func (p *syncPlanner) declaredArtifacts(drv dbc.Driver, current dbc.PkgInfo, existing map[resolution.Target]lockedArtifact) (artifacts []lockedArtifact, unmeasured []dbc.PkgInfo, err error) {
	host, err := resolution.TargetFromPlatformTuple(p.target)
	if err != nil {
		return nil, nil, err
	}
	artifacts = make([]lockedArtifact, 0, len(p.platforms))
	for _, t := range p.platforms {
		if a, ok := existing[t]; ok {
			artifacts = append(artifacts, a)
			continue
		}
		entry, err := platformPackage(drv, current.Version, t)
		if err != nil {
			return nil, nil, err
		}
		// The same check for every target, the current one included.
		if unrecordableRelativePackageURL(entry.URL) {
			return nil, nil, fmt.Errorf("the package for %s has an unrecordable relative URL", targetTuple(t))
		}
		isHost := current.Path != nil && t == host
		pkg := current
		if !isHost {
			pkg, err = withExactVersion(drv, current.Version).GetPackage(current.Version, entry.Platform, true)
			if err != nil {
				return nil, nil, err
			}
		}
		art, err := newLockedArtifact(drv.Registry.BaseURL, pkg.Path, pkg.PlatformTuple, publishedHash(pkg))
		if err != nil {
			return nil, nil, err
		}
		if !isHost && art.Hash == "" {
			unmeasured = append(unmeasured, pkg)
		}
		artifacts = append(artifacts, art)
	}
	published, err := publishedArtifacts(drv, current.Version, artifacts)
	if err != nil {
		return nil, nil, err
	}
	return append(artifacts, published...), unmeasured, nil
}

// selectDeclaredPackage selects the version of d for spec that has a build
// for every target, and checks the package metadata of each of them. The
// returned package has no Path: there is no host package.
func selectDeclaredPackage(d *dbc.Driver, spec driverSpec, targets []resolution.Target) (dbc.PkgInfo, error) {
	version, err := selectProjectVersion(d, spec, nil, targets)
	if err != nil {
		return dbc.PkgInfo{}, err
	}
	for _, t := range targets {
		entry, err := platformPackage(*d, version, t)
		if err != nil {
			return dbc.PkgInfo{}, err
		}
		if _, err := withExactVersion(*d, version).GetPackage(version, entry.Platform, true); err != nil {
			return dbc.PkgInfo{}, err
		}
	}
	return dbc.PkgInfo{Version: version}, nil
}
