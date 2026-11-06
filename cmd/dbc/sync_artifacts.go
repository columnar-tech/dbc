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
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/resolution"
	"net/url"
)

// lockArtifacts records the current package and any secondary packages that
// can be represented unambiguously in the lock file. Problems in secondary
// packages only omit those packages; failure to record the current package
// prevents the driver from being locked.
func (p *syncPlanner) lockArtifacts(drv dbc.Driver, current dbc.PkgInfo) ([]lockedArtifact, error) {
	currentArtifact, err := newLockedArtifact(drv.Registry.BaseURL, current.Path, current.PlatformTuple, publishedHash(current))
	if err != nil {
		return nil, err
	}
	artifacts := []lockedArtifact{currentArtifact}
	currentTarget := resolution.CanonicalTarget(currentArtifact.Target)
	currentIdentity, err := canonicalRegistryIdentity(drv.Registry.BaseURL)
	if err != nil {
		return nil, err
	}
	currentLocation := lockedLocation(lockedSource{Type: lockSourceRegistry, URL: currentIdentity}, currentArtifact)

	// Group raw tuples before resolving packages. GetPackage selects the first
	// matching raw tuple, so resolving before grouping would make aliases
	// dependent on registry source order.
	groups := map[resolution.Target][]dbc.PackageInfo{}
	checkedCurrent := false
	for _, version := range drv.AllVersions() {
		if version.Version == nil || !version.Version.Equal(current.Version) {
			continue
		}
		for _, pkg := range version.Packages {
			if !checkedCurrent && pkg.Platform == current.PlatformTuple {
				checkedCurrent = true
				if unrecordableRelativePackageURL(pkg.URL) {
					return nil, errors.New("current package has an unrecordable relative URL")
				}
			}
			target, err := resolution.TargetFromPlatformTuple(pkg.Platform)
			if err != nil {
				continue
			}
			target = resolution.CanonicalTarget(target)
			if target == currentTarget {
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
		pkg, err := drv.GetPackage(current.Version, packages[0].Platform, true)
		if err != nil || publishedHash(pkg) == "" {
			continue
		}
		artifact, err := newLockedArtifact(drv.Registry.BaseURL, pkg.Path, packages[0].Platform, publishedHash(pkg))
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{
			artifact: artifact,
			location: lockedLocation(lockedSource{Type: lockSourceRegistry, URL: currentIdentity}, artifact),
		})
	}

	// The strict lock writer requires one hash per artifact location. A
	// secondary target that aliases the current location is omitted, as are all
	// secondary targets that conflict with one another at a shared location.
	currentHash := currentArtifact.Hash
	byLocation := make(map[string]map[string]bool)
	for _, c := range candidates {
		if byLocation[c.location] == nil {
			byLocation[c.location] = make(map[string]bool)
		}
		byLocation[c.location][c.artifact.Hash] = true
	}
	for _, c := range candidates {
		if c.location == currentLocation && (currentHash == "" || c.artifact.Hash != currentHash) {
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
