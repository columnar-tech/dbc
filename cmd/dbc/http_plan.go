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
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/resolution"
)

// httpPlan is what the plan of a driver with an HTTP source leaves to do
// before its lock entry is complete.
type httpPlan struct {
	// pending are the artifacts of the lock entry whose hash is not known
	// yet, in the order of the entry.
	pending []httpPending
	// checksumArchives lists, for the checksum URL of each artifact of the
	// lock entry, recorded or pending, the archive URLs it serves.
	checksumArchives map[string][]string
}

// httpPending is an artifact of an HTTP source whose hash is read from its
// checksum file or, without one, measured from its download.
type httpPending struct {
	target  resolution.Target
	archive *url.URL
	// checksum is the URL of the checksum file, or nil to measure the hash.
	checksum *url.URL
}

// measured reports whether the hash of the artifact is measured from its
// download rather than read from a checksum file.
func (p httpPending) measured() bool { return p.checksum == nil }

// httpTargets returns the targets that a driver with an HTTP source is
// locked for: the declared platforms, or the current one.
func (p *syncPlanner) httpTargets() ([]resolution.Target, error) {
	if p.platforms != nil {
		return p.platforms, nil
	}
	host, err := resolution.TargetFromPlatformTuple(p.target)
	if err != nil {
		return nil, err
	}
	return []resolution.Target{host}, nil
}

// planHTTP plans a driver with an HTTP source. It never fetches anything:
// a lock entry of the same source and version is replayed after its signer
// and the URL of every artifact are checked against the driver list, and
// the targets it lacks are left pending (see fetchHTTPChecksums and
// measureDeclared). Another version of the same source is planned anew; a
// lock entry from another source only with allowSourceChange, keeping its
// signer (see checkSourceContinuity).
func (p *syncPlanner) planHTTP(name string, spec driverSpec, old *lockedDriver) (plannedDriver, error) {
	src, err := spec.Source.http()
	if err != nil {
		return plannedDriver{}, fmt.Errorf("driver %s: %w", name, err)
	}
	v := spec.exact
	if v == nil {
		return plannedDriver{}, fmt.Errorf("driver %s: a driver with an HTTP source needs an exact version", name)
	}
	policy, err := src.policy(p.projectSigningPolicy)
	if err != nil {
		return plannedDriver{}, fmt.Errorf("driver %s: %w", name, err)
	}
	source := src.lockedSource()

	var artifacts []lockedArtifact
	replay := old != nil && old.Source.sameSource(source) && old.Version == v.String()
	var signer packageSigner
	if replay {
		// Check the pinned key and every recorded URL before anything is
		// fetched; a recorded URL that the templates no longer produce is
		// never used.
		if signer, err = p.trustedSigner(name, old.Signer, policy); err != nil {
			return plannedDriver{}, err
		}
		for _, a := range old.Artifacts {
			u, err := src.url.expand(v, a.Target)
			if err != nil {
				return plannedDriver{}, fmt.Errorf("driver %s: %w", name, err)
			}
			if a.Location.Kind != resolution.ArtifactLocationURL || a.Location.Value != u.String() {
				return plannedDriver{}, fmt.Errorf("driver %s: the lock file has %s for %s, but the url of the driver list gives %s; run `dbc lock` to lock it again",
					name, a.Location.Value, targetTuple(a.Target), u)
			}
		}
		artifacts = slices.Clone(old.Artifacts)
	} else {
		if old != nil && !old.Source.sameSource(source) && !p.allowSourceChange {
			if old.Source.Type == lockSourceHTTP {
				return plannedDriver{}, fmt.Errorf("driver %s: the url of its HTTP source differs from the one it was locked with; run `dbc lock` to lock it again", name)
			}
			return plannedDriver{}, fmt.Errorf("driver %s: the driver list takes it from an HTTP source, but the lock file has it from %s; run `dbc lock` to lock it again", name, old.Source)
		}
		if signer, err = p.signerFor(name, policy); err != nil {
			return plannedDriver{}, err
		}
	}

	targets, err := p.httpTargets()
	if err != nil {
		return plannedDriver{}, err
	}
	var plan httpPlan
	for _, t := range targets {
		if slices.ContainsFunc(artifacts, func(a lockedArtifact) bool { return a.Target == t }) {
			continue
		}
		u, err := src.url.expand(v, t)
		if err != nil {
			return plannedDriver{}, fmt.Errorf("driver %s: %w", name, err)
		}
		pending := httpPending{target: t, archive: u}
		if src.checksum != nil {
			if pending.checksum, err = src.checksum.expand(v, t); err != nil {
				return plannedDriver{}, fmt.Errorf("driver %s: checksum_url: %w", name, err)
			}
		}
		plan.pending = append(plan.pending, pending)
		artifacts = append(artifacts, lockedArtifact{
			Target:   t,
			Format:   "tar.gz",
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: u.String()},
		})
	}
	if src.checksum != nil {
		plan.checksumArchives = map[string][]string{}
		for _, a := range artifacts {
			c, err := src.checksum.expand(v, a.Target)
			if err != nil {
				return plannedDriver{}, fmt.Errorf("driver %s: checksum_url: %w", name, err)
			}
			plan.checksumArchives[c.String()] = append(plan.checksumArchives[c.String()], a.Location.Value)
		}
	}

	entry := lockedDriver{Name: name, Version: v.String(), Signer: signer.locked(), Source: source, Artifacts: artifacts}
	if old != nil && !replay {
		if err := checkSourceContinuity(name, *old, entry); err != nil {
			return plannedDriver{}, err
		}
	}
	pd := plannedDriver{Name: name, Entry: entry, Signer: signer, http: &plan,
		Package: dbc.PkgInfo{Driver: dbc.Driver{Title: name, Path: name}, Version: v}}
	host, err := resolution.TargetFromPlatformTuple(p.target)
	if err != nil {
		return plannedDriver{}, err
	}
	// A recorded artifact of a platform that is no longer declared is kept,
	// but not installed.
	if i := slices.IndexFunc(artifacts, func(a lockedArtifact) bool { return a.Target == host }); i >= 0 && slices.Contains(targets, host) {
		u, err := url.Parse(artifacts[i].Location.Value)
		if err != nil {
			return plannedDriver{}, fmt.Errorf("driver %s: %w", name, err)
		}
		pd.Package.PlatformTuple = p.target
		pd.Package.Path = u
		pd.Package.ArchiveSHA256 = strings.TrimPrefix(artifacts[i].Hash, lockHashPrefix)
		pd.ArchiveSize = artifacts[i].Size
	}
	return pd, nil
}

// plaintextWarnings warns, once per driver, when a new hash of an HTTP
// source is pinned from what was read over plain HTTP.
func plaintextWarnings(planned []plannedDriver) []string {
	var warnings []string
	for _, pd := range planned {
		if pd.http == nil {
			continue
		}
		if slices.ContainsFunc(pd.http.pending, func(p httpPending) bool {
			from := p.archive
			if p.checksum != nil {
				from = p.checksum
			}
			return strings.EqualFold(from.Scheme, "http")
		}) {
			warnings = append(warnings, fmt.Sprintf("warning: driver %s: the archive hash recorded in the lock file comes from a plain http URL of the driver list, which does not protect it in transit", pd.Name))
		}
	}
	return warnings
}

// fetchHTTPChecksums reads the hash of every pending artifact of an HTTP
// source that has a checksum URL, each checksum file once, and records it in
// the lock entry. The other pending artifacts are left to be measured: the
// one of the current target when it is downloaded, the others through
// Unmeasured. A checksum file that cannot be read or lacks an archive is an
// error and never falls back to measuring.
func fetchHTTPChecksums(ctx context.Context, base baseModel, planned []plannedDriver) error {
	// A checksum file of a hash alone can only serve one archive, and a
	// file name in it one archive URL, of whichever driver and recorded or
	// not; count the archives of each checksum URL over all.
	archives := map[string]map[string]bool{}
	for _, pd := range planned {
		if pd.http == nil {
			continue
		}
		for key, urls := range pd.http.checksumArchives {
			if archives[key] == nil {
				archives[key] = map[string]bool{}
			}
			for _, u := range urls {
				archives[key][u] = true
			}
		}
	}
	for key, urls := range archives {
		names := map[string]string{}
		for u := range urls {
			parsed, err := url.Parse(u)
			if err != nil {
				return err
			}
			name, err := archiveFileName(parsed)
			if err != nil {
				continue // reported when the archive's hash is looked up
			}
			if other, ok := names[name]; ok {
				return fmt.Errorf("the checksum file %s would give one hash for %s and %s, which have the same file name %s", key, other, u, name)
			}
			names[name] = u
		}
	}
	files := map[string][]byte{}
	for i := range planned {
		pd := &planned[i]
		if pd.http == nil {
			continue
		}
		host := pd.hasHostPackage()
		var hostTarget resolution.Target
		if host {
			var err error
			if hostTarget, err = resolution.TargetFromPlatformTuple(pd.Package.PlatformTuple); err != nil {
				return err
			}
		}
		for _, pending := range pd.http.pending {
			j := slices.IndexFunc(pd.Entry.Artifacts, func(a lockedArtifact) bool { return a.Target == pending.target })
			isHost := host && pending.target == hostTarget
			if pending.measured() {
				if !isHost {
					pd.Unmeasured = append(pd.Unmeasured, dbc.PkgInfo{
						Driver:        pd.Package.Driver,
						Version:       pd.Package.Version,
						PlatformTuple: targetTuple(pending.target),
						Path:          pending.archive,
					})
				}
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			key := pending.checksum.String()
			body, ok := files[key]
			if !ok {
				var err error
				if body, err = base.fetchHTTP(ctx, pending.checksum); err != nil {
					return fmt.Errorf("driver %s: could not read the checksum file %s: %w", pd.Name, pending.checksum, err)
				}
				files[key] = body
			}
			hash, err := checksumFor(body, pending.archive, len(archives[key]) == 1)
			if err != nil {
				return fmt.Errorf("driver %s: checksum file %s: %w", pd.Name, pending.checksum, err)
			}
			pd.Entry.Artifacts[j].Hash = lockHashPrefix + hash
			if isHost {
				pd.Package.ArchiveSHA256 = hash
			}
		}
	}
	return nil
}
