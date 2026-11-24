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

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/resolution"
	"net/url"
)

// lockArtifacts records the current package and any secondary packages that
// can be represented unambiguously in the lock file. Problems in secondary
// packages only omit those packages; failure to record the current package
// prevents the driver from being locked. A current package without Path (see
// plannedDriver.hasHostPackage) is not recorded; at least one secondary
// package with a published hash must then be, since no archive of another
// target is downloaded to measure its hash.
func (p *syncPlanner) lockArtifacts(drv dbc.Driver, current dbc.PkgInfo) ([]lockedArtifact, error) {
	var artifacts []lockedArtifact
	if current.Path != nil {
		for _, version := range drv.AllVersions() {
			// Compare the spelling: Equal ignores build metadata, and
			// installs check the package against the locked spelling.
			if version.Version == nil || version.Version.String() != current.Version.String() {
				continue
			}
			if i := slices.IndexFunc(version.Packages, func(pkg dbc.PackageInfo) bool { return pkg.Platform == current.PlatformTuple }); i >= 0 {
				if unrecordableRelativePackageURL(version.Packages[i].URL) {
					return nil, errors.New("current package has an unrecordable relative URL")
				}
				break
			}
		}
		currentArtifact, err := newLockedArtifact(drv.Registry.BaseURL, current.Path, current.PlatformTuple, publishedHash(current))
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, currentArtifact)
	}
	published, err := publishedArtifacts(drv, current.Version, artifacts)
	if err != nil {
		return nil, err
	}
	artifacts = append(artifacts, published...)
	if len(artifacts) == 0 {
		return nil, fmt.Errorf("version %s has no build for %s, and the registry publishes no archive hash for any other platform, which dbc lock records without downloading the archive; "+
			"run `dbc lock` on a machine of a platform the version supports, or publish archive hashes in the registry", current.Version, p.target)
	}
	return artifacts, nil
}

// publishedArtifacts returns the artifacts of version v for every target
// that required does not cover and whose package has a published hash and
// can be represented unambiguously in the lock file. Problematic packages are
// omitted, not errors.
func publishedArtifacts(drv dbc.Driver, v *semver.Version, required []lockedArtifact) ([]lockedArtifact, error) {
	identity, err := canonicalRegistryIdentity(drv.Registry.BaseURL)
	if err != nil {
		return nil, err
	}
	source := lockedSource{Type: lockSourceRegistry, URL: identity}
	requiredTargets := map[resolution.Target]bool{}
	requiredHashes := map[string]string{}
	for _, a := range required {
		requiredTargets[resolution.CanonicalTarget(a.Target)] = true
		requiredHashes[lockedLocation(source, a)] = a.Hash
	}

	// Group raw tuples before resolving packages. GetPackage selects the first
	// matching raw tuple, so resolving before grouping would make aliases
	// dependent on registry source order.
	groups := map[resolution.Target][]dbc.PackageInfo{}
	for _, version := range drv.AllVersions() {
		// Compare the spelling: Equal ignores build metadata, and installs
		// check the package against the locked spelling.
		if version.Version == nil || version.Version.String() != v.String() {
			continue
		}
		for _, pkg := range version.Packages {
			target, err := resolution.TargetFromPlatformTuple(pkg.Platform)
			if err != nil {
				continue
			}
			target = resolution.CanonicalTarget(target)
			if requiredTargets[target] {
				continue
			}
			groups[target] = append(groups[target], pkg)
		}
	}

	type candidate struct {
		artifact lockedArtifact
		location string
	}
	var candidates []candidate
	for _, packages := range groups {
		// Any duplicate raw entry or canonical alias makes the complete target
		// ambiguous, including exact duplicate tuples.
		if len(packages) != 1 || unrecordableRelativePackageURL(packages[0].URL) {
			continue
		}
		pkg, err := withExactVersion(drv, v).GetPackage(v, packages[0].Platform, true)
		if err != nil || publishedHash(pkg) == "" {
			continue
		}
		artifact, err := newLockedArtifact(drv.Registry.BaseURL, pkg.Path, packages[0].Platform, publishedHash(pkg))
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{artifact: artifact, location: lockedLocation(source, artifact)})
	}

	// The strict lock writer requires one hash per artifact location. A
	// target that aliases the location of a required artifact with another
	// (or a not yet measured) hash is omitted, as are all targets that
	// conflict with one another at a shared location.
	byLocation := make(map[string]map[string]bool)
	for _, c := range candidates {
		if byLocation[c.location] == nil {
			byLocation[c.location] = make(map[string]bool)
		}
		byLocation[c.location][c.artifact.Hash] = true
	}
	var artifacts []lockedArtifact
	for _, c := range candidates {
		if hash, ok := requiredHashes[c.location]; ok && (hash == "" || c.artifact.Hash != hash) {
			continue
		}
		if len(byLocation[c.location]) > 1 {
			continue
		}
		artifacts = append(artifacts, c.artifact)
	}
	return artifacts, nil
}

// GetPackage joins relative registry URLs as paths. Do not persist a query or
// fragment from raw metadata as an escaped filename instead of a URL suffix.
func unrecordableRelativePackageURL(raw string) bool {
	u, err := url.Parse(raw)
	return err != nil || (!u.IsAbs() && (u.Host != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != ""))
}
