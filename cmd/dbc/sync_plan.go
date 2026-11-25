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
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/registryurl"
	"github.com/columnar-tech/dbc/internal/resolution"
)

// plannedDriver is the outcome of planning one driver of the driver list.
type plannedDriver struct {
	Name string
	// Entry is the lock entry to write. The artifact of the current target has
	// an empty hash when the registry published none; the hash is then measured
	// from the download.
	Entry lockedDriver
	// Package is the archive of the current target. Its ArchiveSHA256 is empty
	// when the digest is only known after the download. For dbc lock, its Path
	// is nil when the locked version has no build for the current target (see
	// hasHostPackage); only Driver and Version are set then.
	Package dbc.PkgInfo
	// ArchiveSize, when present in a lock entry, is checked against the archive
	// bytes before the lock is saved or any driver is installed.
	ArchiveSize *int64
	// LibrarySHA256 is the installed library digest recorded by a v1 lock file
	// that still applies, or empty.
	LibrarySHA256 string
	// Signer is the key the lock entry pins. Reuse checks, installs and the
	// hash-measuring download of dbc lock all verify with it. It is never nil.
	Signer packageSigner
	// Unmeasured are the packages of declared platforms other than the
	// current one whose hash the registry does not publish. Their archives
	// are downloaded to measure it (see measureDeclared).
	Unmeasured []dbc.PkgInfo
}

// hasHostPackage reports whether the planned driver has an archive for the
// current target. Only a planner with hostOptional plans drivers without one.
func (pd *plannedDriver) hasHostPackage() bool { return pd.Package.Path != nil }

// syncPlanner decides which version, registry and archive digest every driver
// of a driver list needs, replaying a lock file where it still applies.
type syncPlanner struct {
	target string
	// registries lists the configured registries without fetching any index.
	registries func() ([]dbc.Registry, error)
	// index fetches the merged index of all registries. Its error may be a
	// partial failure that is returned together with drivers.
	index func() ([]dbc.Driver, error)
	// registryIndex fetches the index of a single registry.
	registryIndex func(dbc.Registry) ([]dbc.Driver, error)
	// allowSourceChange lets a driver whose source in the driver list
	// selects another registry than its lock entry be resolved again. Only
	// dbc lock sets it; dbc sync stops instead.
	allowSourceChange bool
	// hostOptional lets a selected version that has no build for the current
	// target be planned without a host package; its lock entry then records
	// only the targets whose archive hash the registry publishes. Only dbc
	// lock sets it, since it installs nothing; dbc sync fails instead.
	hostOptional bool
	// platforms are the canonical targets that the driver list declares, or
	// nil. Set by plan.
	platforms []resolution.Target

	// sourceIndexes holds the index of each registry that a driver source
	// selected, by registry identity.
	sourceIndexes map[string][]dbc.Driver

	indexLoaded bool
	drivers     []dbc.Driver
	registryErr error
	indexErr    error
	// signing keeps the keys of this plan: the signer checks of resolution,
	// the replay of lock entries and the verification of every package use
	// the same certificate of a registry.
	signing *dbc.RegistrySigningOperation
}

// loadIndex fetches the merged index once. Like the other commands it only
// fails when no registry delivered anything.
func (p *syncPlanner) loadIndex() ([]dbc.Driver, error) {
	if !p.indexLoaded {
		p.indexLoaded = true
		drivers, err := p.index()
		err = redactRegistryError(err, p.registries)
		p.drivers, p.registryErr = drivers, err
		if len(drivers) == 0 && err != nil {
			p.indexErr = fmt.Errorf("error getting driver list: %w", err)
		}
	}
	return p.drivers, p.indexErr
}

// lockSatisfies reports whether a locked version may still be used for spec.
func lockSatisfies(spec driverSpec, v *semver.Version) bool {
	if spec.Version != nil {
		return spec.Version.Check(v)
	}
	return v.Prerelease() == "" || spec.Prerelease == "allow"
}

// plan resolves every driver in name order. The returned warnings describe
// lock file data that was ignored.
func (p *syncPlanner) plan(list DriversList, lock parsedLockFile) ([]plannedDriver, []string, error) {
	locked := map[string]lockedDriver{}
	for _, d := range lock.V2.Drivers {
		locked[d.Name] = d
	}
	platforms, err := list.targetPlatforms()
	if err != nil {
		return nil, nil, err
	}
	p.platforms = platforms
	if platforms != nil && !p.hostOptional && !p.hostDeclared() {
		return nil, nil, undeclaredHostError(p.target, platforms)
	}
	// A new or changed declaration resolves every driver again, keeping its
	// locked source and version where they have a build for every declared
	// platform. Without a declaration, lock entries are replayed as before.
	platformsChanged := platforms != nil && !slices.Equal(platforms, lock.V2.Platforms)

	var (
		planned  []plannedDriver
		warnings []string
	)
	for _, name := range slices.Sorted(maps.Keys(list.Drivers)) {
		spec := list.Drivers[name].withPrerelease()
		var target *sourceTarget
		if spec.Source != nil {
			t, err := spec.Source.target(list.Registries)
			if err != nil {
				return nil, nil, fmt.Errorf("driver %s: %w", name, err)
			}
			target = &t
		}

		var (
			oldSigner lockedSigner
			oldSource string
			hadLock   bool
			// replaced is the lock entry that relock could not keep because
			// its version has no build for the uncovered platforms.
			replaced  *lockedDriver
			uncovered []resolution.Target
		)
		if d, ok := locked[name]; ok {
			v, err := semver.NewVersion(d.Version)
			if err != nil {
				return nil, nil, fmt.Errorf("driver %s: invalid locked version: %w", name, err)
			}
			sourceChanged := target != nil && target.identity != d.Source.URL
			if sourceChanged && !p.allowSourceChange {
				return nil, nil, fmt.Errorf("driver %s: the driver list takes it from %s, but the lock file has it from %s; "+
					"run `dbc lock` to resolve it again from %s", name, target.identity, d.Source.URL, target.identity)
			}
			if !sourceChanged && lockSatisfies(spec, v) {
				var (
					pd   plannedDriver
					kept = true
					err  error
				)
				if platformsChanged {
					pd, uncovered, err = p.relock(name, d, v)
					kept = uncovered == nil
				} else {
					pd, err = p.fromLock(name, d, v)
				}
				if err != nil {
					return nil, nil, err
				}
				if kept {
					planned = append(planned, pd)
					continue
				}
				replaced = &d
			}
			// The driver is resolved again; its signer must not change silently.
			oldSigner, oldSource, hadLock = d.Signer, d.Source.URL, true
		}

		var pin *semver.Version
		var proof string
		if info, ok := lock.V1.lockinfo[name]; ok {
			if info.Version != nil && lockSatisfies(spec, info.Version) {
				pin = info.Version
			}
			var warning string
			proof, warning = p.v1Proof(name, info, pin != nil)
			if warning != "" {
				warnings = append(warnings, warning)
			}
		}

		pd, err := p.resolve(name, spec, pin, target)
		if err != nil {
			return nil, nil, err
		}
		if hadLock && pd.Entry.Signer != oldSigner {
			return nil, nil, signerChangedError(name, oldSigner, pd.Entry.Signer)
		}
		// Every unsigned registry is a signer of its own, so a driver pinned
		// to one cannot move to another, not even with a changed source.
		if hadLock && oldSigner.Type == lockSignerNone && pd.Entry.Source.URL != oldSource {
			return nil, nil, fmt.Errorf("driver %s: the lock file has it from the unsigned registry %s, but it would now come from the unsigned registry %s, which is another signer; %s",
				name, oldSource, pd.Entry.Source.URL, signerChangeHint)
		}
		if replaced != nil && (pd.Entry.Version != replaced.Version || pd.Entry.Source.URL != replaced.Source.URL) {
			warnings = append(warnings, fmt.Sprintf("warning: driver %s: version %s from %s in the lock file has no build for %s in platforms, so version %s from %s was selected instead",
				name, replaced.Version, replaced.Source.URL, platformList(uncovered), pd.Entry.Version, pd.Entry.Source.URL))
		}
		if pin != nil && pd.Package.Version.String() != pin.String() {
			// The pin was dropped for a version with a build for every
			// declared platform; a proof is about the old version.
			warning := fmt.Sprintf("warning: driver %s: version %s in the lock file does not cover all declared platforms, so version %s was selected instead", name, pin, pd.Package.Version)
			if proof != "" {
				warning += ", ignoring the checksum in the lock file"
				proof = ""
			}
			warnings = append(warnings, warning)
		}
		if proof != "" && !pd.hasHostPackage() {
			// The proof can only be checked against the archive of this target.
			reason := fmt.Sprintf("version %s has no build for it", pd.Package.Version)
			if p.platforms != nil && !p.hostDeclared() {
				reason = "this platform is not in platforms of the driver list"
			}
			return nil, nil, fmt.Errorf("driver %s: the version 1 lock file records a driver library checksum for %s, but %s, so the checksum cannot be checked; "+
				"run `dbc lock` on a machine of another platform, or delete the driver's entry from the lock file to lock it without the check", name, p.target, reason)
		}
		pd.LibrarySHA256 = proof
		planned = append(planned, pd)
	}
	switch {
	case !p.hostOptional:
	case p.platforms != nil && !p.hostDeclared():
		// dbc sync refuses an undeclared platform before looking at any
		// lock entry, so one warning covers every driver.
		warnings = append(warnings, fmt.Sprintf("warning: this platform (%s) is not in platforms of the driver list (%s), so dbc sync cannot install its drivers here",
			p.target, platformList(p.platforms)))
	default:
		// Match the canonical host target as fromLock does. An alias can
		// supply a replayable artifact even when the index lookup finds no
		// package for the raw host tuple. With the host declared, every
		// entry records it.
		host, err := resolution.TargetFromPlatformTuple(p.target)
		if err != nil {
			return nil, nil, err
		}
		for _, pd := range planned {
			if !slices.ContainsFunc(pd.Entry.Artifacts, func(a lockedArtifact) bool { return a.Target == host }) {
				warnings = append(warnings, fmt.Sprintf("warning: driver %s %s has no build for this platform (%s); dbc sync will fail here",
					pd.Name, pd.Package.Version, p.target))
			}
		}
	}
	return planned, warnings, nil
}

// v1Proof returns the library digest of a v1 lock entry when it still applies
// and otherwise a warning that names why it is dropped.
func (p *syncPlanner) v1Proof(name string, info lockInfo, pinned bool) (proof, warning string) {
	if info.Checksum == "" {
		return "", ""
	}
	var reason string
	switch {
	case !pinned:
		reason = "the locked version no longer satisfies the version constraint"
	case info.Platform != p.target:
		reason = fmt.Sprintf("it was not recorded for this platform (%s)", p.target)
	default:
		return info.Checksum, ""
	}
	recorded := info.Platform
	if recorded == "" {
		recorded = "unknown"
	}
	return "", fmt.Sprintf("warning: ignoring the checksum in the lock file for driver %s (recorded platform: %s): %s", name, recorded, reason)
}

// lockReplay is what the lock entry of a driver gives to install it on this
// platform as recorded, without any index. Both the normal plan (fromLock,
// relock) and dbc sync --locked (checkLockedReplay) start from it; they
// differ only in what they do with its problems and a missing host artifact.
type lockReplay struct {
	entry   lockedDriver
	version *semver.Version
	bound   boundRegistry
	signer  packageSigner
	// host is the artifact of the entry for this platform, or nil.
	host *lockedArtifact
	// pkg is the package of host, or, without host, the driver and version
	// alone. It is only valid when err returns nil.
	pkg dbc.PkgInfo
	// trustErr is why the configuration does not accept the entry: its
	// registry is not configured, or the registry selects another signer
	// than the pinned one. The signer can only be checked once the registry
	// is bound, since its configuration selects the key.
	trustErr error
	// artifactErr is why the location of host cannot be resolved. It needs
	// the registry, so it is only checked once the registry is bound.
	artifactErr error
}

// err returns the first problem of the replay, or nil.
func (r lockReplay) err() error {
	if r.trustErr != nil {
		return r.trustErr
	}
	return r.artifactErr
}

// planned returns the planned driver of a replay without problems.
func (r lockReplay) planned() plannedDriver {
	pd := plannedDriver{Name: r.entry.Name, Entry: r.entry, Signer: r.signer, Package: r.pkg}
	if r.host != nil {
		pd.ArchiveSize = r.host.Size
	}
	return pd
}

// replayLock checks the lock entry d of version v against the configuration
// and finds its artifact for this platform. It fetches nothing: the pinned
// key is checked before any index is fetched, archive is downloaded or lock
// file is written, and never falls back to another key or registry. Each
// check runs whenever what it needs is there, and its problem is recorded in
// the replay; the error is only about this platform's target.
func (p *syncPlanner) replayLock(name string, d lockedDriver, v *semver.Version) (lockReplay, error) {
	host, err := resolution.TargetFromPlatformTuple(p.target)
	if err != nil {
		return lockReplay{}, err
	}
	r := lockReplay{entry: d, version: v, pkg: dbc.PkgInfo{Driver: dbc.Driver{Title: name, Path: name}, Version: v}}
	if i := slices.IndexFunc(d.Artifacts, func(a lockedArtifact) bool { return a.Target == host }); i >= 0 {
		r.host = &d.Artifacts[i]
	}
	r.bound, r.trustErr = p.bind(name, d.Source.URL)
	if r.trustErr != nil {
		return r, nil
	}
	r.signer, r.trustErr = p.trustedSigner(name, d.Signer, r.bound)
	if r.host != nil {
		r.pkg, r.artifactErr = p.lockedPackage(name, r.bound, *r.host, v)
	}
	return r, nil
}

// fromLock plans a driver whose locked version is still wanted. Without an
// artifact for this platform, it completes the entry from the index of its
// registry.
func (p *syncPlanner) fromLock(name string, d lockedDriver, v *semver.Version) (plannedDriver, error) {
	r, err := p.replayLock(name, d, v)
	if err != nil {
		return plannedDriver{}, err
	}
	if err := r.err(); err != nil {
		return plannedDriver{}, err
	}
	// The lock has no artifact for this target. A platform that the driver
	// list does not declare is not added: only dbc lock gets here.
	if r.host != nil || (p.platforms != nil && !p.hostDeclared()) {
		return r.planned(), nil
	}
	bound, signer := r.bound, r.signer
	// Add it from the bound registry.
	if bound.HasQuery {
		return plannedDriver{}, queryCompletionError(name, p.target, d.Source.URL)
	}
	drivers, err := p.registryIndex(bound.Registry)
	if err != nil {
		return plannedDriver{}, fmt.Errorf("could not fetch the index of registry %s to complete the lock entry of driver %s: %w",
			d.Source.URL, name, redactRegistryError(err, p.registries, bound.BaseURL))
	}
	drv, err := findDriver(name, drivers)
	if err != nil {
		return plannedDriver{}, err
	}
	if p.hostOptional && hostBuildMissing(drv, v, p.target) {
		// Nothing to add: the entry is kept as it is.
		return r.planned(), nil
	}
	pkg, err := withExactVersion(drv, v).GetPackage(v, p.target, true)
	if err != nil {
		return plannedDriver{}, fmt.Errorf("could not complete the lock entry of driver %s: %w", name, redactRegistryError(err, p.registries, bound.BaseURL))
	}
	art, err := newLockedArtifact(bound.BaseURL, pkg.Path, p.target, publishedHash(pkg))
	if err != nil {
		return plannedDriver{}, fmt.Errorf("driver %s: %w", name, redactRegistryError(err, p.registries, bound.BaseURL))
	}
	d.Artifacts = append(slices.Clone(d.Artifacts), art)
	return plannedDriver{Name: name, Entry: d, Package: pkg, Signer: signer}, nil
}

// undeclaredHostError reports that the current platform is not among the
// declared platforms, so dbc sync cannot install the drivers here.
func undeclaredHostError(host string, platforms []resolution.Target) error {
	return fmt.Errorf("this platform (%s) is not in platforms of the driver list (%s), so its drivers cannot be installed here; add it to platforms",
		host, platformList(platforms))
}

// lockedPackage returns the package of the current target that the locked
// artifact a of version v records, bound to its registry.
func (p *syncPlanner) lockedPackage(name string, bound boundRegistry, a lockedArtifact, v *semver.Version) (dbc.PkgInfo, error) {
	u, err := bound.artifactURL(a)
	if err != nil {
		return dbc.PkgInfo{}, fmt.Errorf("driver %s: %w", name, err)
	}
	return dbc.PkgInfo{
		Driver:        dbc.Driver{Title: name, Path: name},
		Version:       v,
		PlatformTuple: p.target,
		Path:          u,
		ArchiveSHA256: strings.TrimPrefix(a.Hash, lockHashPrefix),
	}, nil
}

// relock keeps all recorded artifacts and adds missing declared targets
// from the locked version and registry. It replays complete entries
// without fetching an index. Uncovered targets trigger re-resolution.
// If completion needs an index, a query string or missing driver is an error.
func (p *syncPlanner) relock(name string, d lockedDriver, v *semver.Version) (pd plannedDriver, uncovered []resolution.Target, err error) {
	var missing []resolution.Target
	for _, t := range p.platforms {
		if !slices.ContainsFunc(d.Artifacts, func(a lockedArtifact) bool { return a.Target == t }) {
			missing = append(missing, t)
		}
	}
	if missing == nil {
		pd, err := p.fromLock(name, d, v)
		return pd, nil, err
	}
	// As in fromLock, check the pinned key before any index is fetched. The
	// artifact for this platform is only used once the others are found.
	r, err := p.replayLock(name, d, v)
	if err != nil {
		return plannedDriver{}, nil, err
	}
	if r.trustErr != nil {
		return plannedDriver{}, nil, r.trustErr
	}
	bound, signer := r.bound, r.signer
	if bound.HasQuery {
		return plannedDriver{}, nil, queryCompletionError(name, platformList(missing), d.Source.URL)
	}
	drivers, err := p.registryIndex(bound.Registry)
	if err != nil {
		return plannedDriver{}, nil, fmt.Errorf("could not fetch the index of registry %s to lock driver %s for the platforms of the driver list: %w",
			d.Source.URL, name, redactRegistryError(err, p.registries, bound.BaseURL))
	}
	drv, err := findDriver(name, drivers)
	if err != nil {
		return plannedDriver{}, nil, err
	}
	if uncovered := uncoveredPlatforms(drv, v, missing); uncovered != nil {
		return plannedDriver{}, uncovered, nil
	}

	current := dbc.PkgInfo{Driver: dbc.Driver{Title: name, Path: name}, Version: v}
	var size *int64
	if p.hostDeclared() {
		if r.host != nil {
			if r.artifactErr != nil {
				return plannedDriver{}, nil, r.artifactErr
			}
			current, size = r.pkg, r.host.Size
		} else {
			host, err := resolution.TargetFromPlatformTuple(p.target)
			if err != nil {
				return plannedDriver{}, nil, err
			}
			entry, err := platformPackage(drv, v, host)
			if err != nil {
				return plannedDriver{}, nil, err
			}
			current, err = withExactVersion(drv, v).GetPackage(v, entry.Platform, true)
			if err != nil {
				return plannedDriver{}, nil, fmt.Errorf("driver %s: %w", name, redactRegistryError(err, p.registries, bound.BaseURL))
			}
		}
	}
	artifacts, unmeasured, err := p.declaredArtifacts(drv, current, d.Artifacts)
	if err != nil {
		return plannedDriver{}, nil, fmt.Errorf("driver %s: %w", name, redactRegistryError(err, p.registries, bound.BaseURL))
	}
	d.Artifacts = artifacts
	return plannedDriver{Name: name, Entry: d, Package: current, ArchiveSize: size, Signer: signer, Unmeasured: unmeasured}, nil, nil
}

// queryCompletionError reports that a lock entry has no artifact for targets
// and cannot be completed, since its registry URL has a query string.
func queryCompletionError(name, targets, source string) error {
	return fmt.Errorf("the lock file has no artifact of driver %s for %s and its registry %s uses a query string, so it cannot be completed; "+
		"delete the driver's entry from the lock file or change its version constraint to lock it again", name, targets, source)
}

// packageSigner is how the packages of a driver are verified: with key, or
// not at all when the registry is declared unsigned.
type packageSigner struct {
	key      *dbc.SigningKey
	unsigned bool
}

// locked returns the lock file spelling of the signer.
func (s packageSigner) locked() lockedSigner {
	if s.unsigned {
		return lockedSigner{Type: lockSignerNone}
	}
	return lockedSigner{Type: lockSignerOpenPGP, Fingerprint: s.key.Fingerprint()}
}

// signerFor returns how the packages of a driver from registry must be
// verified, as the registry's configuration says: the key it names, the
// Columnar key when it names none, or no key when it is declared unsigned.
// A key that cannot be loaded, such as a missing public_key_file, is an
// error rather than a fallback to another key.
//
// The key of each registry is read once per plan (see signingOperation), so
// that wherever one sync or lock checks a signer or a signature it uses the
// same certificate, even when the key is read from a file that changes
// meanwhile. This does not make dbc lock verify more: it still records a
// published archive hash without downloading the archive.
func (p *syncPlanner) signerFor(name string, bound boundRegistry) (packageSigner, error) {
	key, err := p.signingOperation().ResolveSigningKey(&bound.Registry)
	if err != nil {
		return packageSigner{}, fmt.Errorf("driver %s: could not load the signing key of its registry: %w", name, err)
	}
	return packageSigner{key: key, unsigned: key == nil}, nil
}

// signingOperation returns the keys of this plan.
func (p *syncPlanner) signingOperation() *dbc.RegistrySigningOperation {
	if p.signing == nil {
		p.signing = &dbc.RegistrySigningOperation{}
	}
	return p.signing
}

// trustedSigner returns how the packages of a driver locked with sig must be
// verified. The configuration of the bound registry decides which key is
// trusted; the lock only says which one was chosen. A pin that the
// configuration no longer selects is an error, also with --no-verify, and
// never falls back to another key or registry.
func (p *syncPlanner) trustedSigner(name string, sig lockedSigner, bound boundRegistry) (packageSigner, error) {
	signer, err := p.signerFor(name, bound)
	if err != nil {
		return packageSigner{}, err
	}
	if signer.locked() != sig {
		return packageSigner{}, fmt.Errorf("driver %s: the lock file pins %s, but the configuration of its registry selects %s; %s",
			name, sig, signer.locked(), signerChangeHint)
	}
	return signer, nil
}

// signerChangeHint tells how to accept a change of the pinned signing key
// without editing the lock file by hand.
//
// TODO: suggest `dbc lock --upgrade-driver <name>` once it exists, so that
// the other drivers keep their locked versions.
const signerChangeHint = "if the change of key is intended, remove the lock file and run `dbc lock` to create it again, " +
	"which may also select newer versions of the other drivers"

// signerChangedError reports that resolving a driver again would replace the
// signer that its lock entry pins.
func signerChangedError(name string, locked, now lockedSigner) error {
	return fmt.Errorf("driver %s: the lock file pins %s, but its packages would now be verified with %s; %s",
		name, locked, now, signerChangeHint)
}

// bind finds the one configured registry with the given identity.
func (p *syncPlanner) bind(name, identity string) (boundRegistry, error) {
	registries, err := p.registries()
	if err != nil {
		return boundRegistry{}, redactURLSecrets(err, registrySecrets(registries))
	}
	bound, err := bindLockedRegistry(identity, registries)
	if err != nil {
		return boundRegistry{}, fmt.Errorf("driver %s: %w", name, err)
	}
	return bound, nil
}

// withExactVersion returns d with only the versions spelled as v, so that
// GetPackage, which ignores build metadata, picks the package of exactly v:
// installs check the package against that spelling. Versions without a
// value are kept for GetPackage to report.
func withExactVersion(d dbc.Driver, v *semver.Version) dbc.Driver {
	exact := d.PkgInfo[:0:0]
	for _, info := range d.PkgInfo {
		if info.Version == nil || info.Version.String() == v.String() {
			exact = append(exact, info)
		}
	}
	d.PkgInfo = exact
	return d
}

// selectProjectPackage looks up the host artifact for the pinned or newest eligible version.
func selectProjectPackage(driver *dbc.Driver, spec driverSpec, pin *semver.Version, platform string) (dbc.PkgInfo, error) {
	version, err := selectProjectVersion(driver, spec, pin, nil)
	if err != nil {
		return dbc.PkgInfo{}, err
	}
	return withExactVersion(*driver, version).GetPackage(version, platform, true)
}

// selectProjectVersion returns pin or the newest eligible version. Without
// platforms, the builds a version has do not matter. With platforms, the
// version must have a build for each of them: a pin without one is dropped
// and an older eligible version may be selected.
func selectProjectVersion(driver *dbc.Driver, spec driverSpec, pin *semver.Version, platforms []resolution.Target) (*semver.Version, error) {
	if pin != nil && (platforms == nil || coversPlatforms(*driver, pin, platforms)) {
		return pin, nil
	}
	latest, err := driver.GetLatestVersionAnyPlatform(spec.Version, spec.Prerelease == "allow")
	if err != nil || platforms == nil {
		return latest, err
	}
	return newestCoveringVersion(*driver, spec, latest, platforms)
}

// hostDeclared reports whether the current target is one of the declared
// platforms.
func (p *syncPlanner) hostDeclared() bool {
	host, err := resolution.TargetFromPlatformTuple(p.target)
	return err == nil && slices.Contains(p.platforms, host)
}

// hostBuildMissing reports whether d lists packages for exactly v, but none
// with the requested platform tuple. This lets dbc lock proceed without a
// host package for a listed version. Missing versions, nil versions, and
// matching entries without packages are left to the package lookup.
func hostBuildMissing(d dbc.Driver, v *semver.Version, platform string) bool {
	listed := false
	for _, info := range d.AllVersions() {
		if info.Version == nil {
			return false
		}
		// Include build metadata to match withExactVersion. Sync checks the
		// package manifest against the full version recorded in the lock.
		if info.Version.String() != v.String() {
			continue
		}
		if len(info.Packages) == 0 {
			return false
		}
		listed = true
		for _, pkg := range info.Packages {
			// Match the raw tuple so this check agrees with GetPackage.
			// Counting an alias as a host build would make GetPackage look
			// for a tuple the index lacks. lockArtifacts can still record
			// an unambiguous alias with a published hash for the host target.
			if pkg.Platform == platform {
				return false
			}
		}
	}
	return listed
}

// sourceIndex fetches the index of the registry that target selects, once
// per registry. Unlike the merged index, any failure is an error.
func (p *syncPlanner) sourceIndex(name string, target sourceTarget) ([]dbc.Driver, error) {
	if drivers, ok := p.sourceIndexes[target.identity]; ok {
		return drivers, nil
	}
	registries, err := p.registries()
	if err != nil {
		return nil, redactURLSecrets(err, registrySecrets(registries))
	}
	reg, err := target.bind(registries)
	if err != nil {
		return nil, fmt.Errorf("driver %s: %w", name, err)
	}
	drivers, err := p.registryIndex(reg)
	if err != nil {
		return nil, fmt.Errorf("could not fetch the index of registry %s, which driver %s is taken from: %w",
			target.identity, name, redactRegistryError(err, p.registries, reg.BaseURL))
	}
	if p.sourceIndexes == nil {
		p.sourceIndexes = map[string][]dbc.Driver{}
	}
	p.sourceIndexes[target.identity] = drivers
	return drivers, nil
}

// resolve picks the package for a driver, keeping pin (a version from a v1
// lock file) when it is set. A driver with a source is resolved from that
// registry only; any other driver from the merged registries.
func (p *syncPlanner) resolve(name string, spec driverSpec, pin *semver.Version, target *sourceTarget) (plannedDriver, error) {
	var (
		drivers []dbc.Driver
		err     error
	)
	if target != nil {
		drivers, err = p.sourceIndex(name, *target)
	} else {
		drivers, err = p.loadIndex()
	}
	if err != nil {
		return plannedDriver{}, err
	}
	// A driver with a source is resolved from one complete index. For any
	// other driver the failed registries are passed on, since one with
	// another signer might list it.
	var indexErr error
	if target == nil {
		indexErr = p.registryErr
	}
	pkg, err := p.signingOperation().ResolvePackage(drivers, indexErr, name, func(candidate *dbc.Driver) (dbc.PkgInfo, error) {
		version, err := selectProjectVersion(candidate, spec, pin, p.platforms)
		if err != nil {
			return dbc.PkgInfo{}, err
		}
		if p.platforms != nil {
			if !p.hostDeclared() {
				// Only dbc lock gets here; the platform is not recorded.
				return dbc.PkgInfo{Version: version}, nil
			}
			host, err := resolution.TargetFromPlatformTuple(p.target)
			if err != nil {
				return dbc.PkgInfo{}, err
			}
			entry, err := platformPackage(*candidate, version, host)
			if err != nil {
				return dbc.PkgInfo{}, err
			}
			return withExactVersion(*candidate, version).GetPackage(version, entry.Platform, true)
		}
		if p.hostOptional && hostBuildMissing(*candidate, version, p.target) {
			// The version is chosen without regard to platforms, so a
			// missing host build neither changes it nor the registry.
			return dbc.PkgInfo{Version: version}, nil
		}
		return withExactVersion(*candidate, version).GetPackage(version, p.target, true)
	})
	drv := pkg.Driver
	var sourceURL *url.URL
	if drv.Registry != nil {
		sourceURL = drv.Registry.BaseURL
	}
	if err != nil {
		switch {
		case target != nil && drv.Path == "":
			return plannedDriver{}, fmt.Errorf("driver %s is not in %s (%s), which the driver list takes it from", name, target.source, target.identity)
		case target != nil:
			return plannedDriver{}, fmt.Errorf("driver %s from %s: %w", name, target.source, redactRegistryError(err, p.registries, sourceURL))
		case isSelectionError(err):
			return plannedDriver{}, fmt.Errorf("%w\n%s", redactRegistryError(err, p.registries), sourceHint)
		case drv.Path == "":
			return plannedDriver{}, wrapWithRegistryContext(err, p.registryErr)
		}
		return plannedDriver{}, redactRegistryError(err, p.registries, sourceURL)
	}

	identity, err := canonicalRegistryIdentity(sourceURL)
	if err != nil {
		return plannedDriver{}, fmt.Errorf("driver %s: %w", name, err)
	}
	// The next sync binds the entry by this identity, so it must name exactly
	// one configured registry, whose configuration also selects the key.
	bound, err := p.bind(name, identity)
	if err != nil {
		return plannedDriver{}, err
	}
	// A new resolution has to fetch the index first to learn which
	// registry the driver comes from; a key that cannot be loaded still
	// fails before any archive is downloaded or lock file written.
	signer, err := p.signerFor(name, bound)
	if err != nil {
		return plannedDriver{}, err
	}
	var (
		artifacts  []lockedArtifact
		unmeasured []dbc.PkgInfo
	)
	if p.platforms != nil {
		artifacts, unmeasured, err = p.declaredArtifacts(drv, pkg, nil)
	} else {
		artifacts, err = p.lockArtifacts(drv, pkg)
	}
	if err != nil {
		return plannedDriver{}, fmt.Errorf("driver %s: %w", name, redactRegistryError(err, p.registries, sourceURL))
	}
	return plannedDriver{Name: name, Package: pkg, Signer: signer, Unmeasured: unmeasured, Entry: lockedDriver{
		Name:      name,
		Version:   pkg.Version.String(),
		Signer:    signer.locked(),
		Source:    lockedSource{Type: lockSourceRegistry, URL: identity},
		Artifacts: artifacts,
	}}, nil
}

// publishedHash returns the lock file spelling of the registry's archive hash,
// or empty when none is published.
func publishedHash(pkg dbc.PkgInfo) string {
	if pkg.ArchiveSHA256 == "" {
		return ""
	}
	return lockHashPrefix + pkg.ArchiveSHA256
}

// redactedError is an error whose text no longer shows URL secrets while it
// still unwraps to the original error.
type redactedError struct {
	msg string
	err error
}

func (e redactedError) Error() string { return e.msg }
func (e redactedError) Unwrap() error { return e.err }

// registrySecrets lists the values in the configured registry URLs that must
// never be shown; see registryurl.Secrets.
func registrySecrets(registries []dbc.Registry) []string {
	urls := make([]*url.URL, 0, len(registries))
	for _, r := range registries {
		urls = append(urls, r.BaseURL)
	}
	return registryurl.Secrets(urls)
}

// redactURLSecrets removes the credentials from the text of err; see
// registryurl.Redact. The result still unwraps to err.
func redactURLSecrets(err error, secrets []string) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	msg := registryurl.Redact(text, secrets)
	if msg == text {
		return err
	}
	return redactedError{msg: msg, err: err}
}
