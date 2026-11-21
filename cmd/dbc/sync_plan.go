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
	// when the digest is only known after the download.
	Package dbc.PkgInfo
	// ArchiveSize, when present in a lock entry, is checked against the archive
	// bytes before the lock is saved or any driver is installed.
	ArchiveSize *int64
	// LibrarySHA256 is the installed library digest recorded by a v1 lock file
	// that still applies, or empty.
	LibrarySHA256 string
	// Signer is the key the lock entry pins. Reuse checks, installs and the
	// hash-measuring download of dbc lock all verify with it. It is never nil.
	Signer *dbc.SigningKey
}

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

	// sourceIndexes holds the index of each registry that a driver source
	// selected, by registry identity.
	sourceIndexes map[string][]dbc.Driver

	indexLoaded bool
	drivers     []dbc.Driver
	registryErr error
	indexErr    error
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
			hadLock   bool
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
				pd, err := p.fromLock(name, d, v)
				if err != nil {
					return nil, nil, err
				}
				planned = append(planned, pd)
				continue
			}
			// The driver is resolved again; its signer must not change silently.
			oldSigner, hadLock = d.Signer, true
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
			return nil, nil, signerChangedError(name, oldSigner)
		}
		pd.LibrarySHA256 = proof
		planned = append(planned, pd)
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

// fromLock plans a driver whose locked version is still wanted.
func (p *syncPlanner) fromLock(name string, d lockedDriver, v *semver.Version) (plannedDriver, error) {
	target, err := resolution.TargetFromPlatformTuple(p.target)
	if err != nil {
		return plannedDriver{}, err
	}
	// Look up the pinned key before anything else: a pin that is not trusted
	// fails here, before any index is fetched, archive is downloaded or lock
	// file is written, and never falls back to another key or registry.
	signer, err := trustedSigner(name, d.Signer)
	if err != nil {
		return plannedDriver{}, err
	}
	bound, err := p.bind(name, d.Source.URL)
	if err != nil {
		return plannedDriver{}, err
	}
	for _, a := range d.Artifacts {
		if a.Target != target {
			continue
		}
		u, err := bound.artifactURL(a)
		if err != nil {
			return plannedDriver{}, fmt.Errorf("driver %s: %w", name, err)
		}
		return plannedDriver{Name: name, Entry: d, ArchiveSize: a.Size, Signer: signer, Package: dbc.PkgInfo{
			Driver:        dbc.Driver{Title: name, Path: name},
			Version:       v,
			PlatformTuple: p.target,
			Path:          u,
			ArchiveSHA256: strings.TrimPrefix(a.Hash, lockHashPrefix),
		}}, nil
	}

	// The lock has no artifact for this target: add it from the bound registry.
	if bound.HasQuery {
		return plannedDriver{}, fmt.Errorf("the lock file has no artifact of driver %s for %s and its registry %s uses a query string, so it cannot be completed; "+
			"delete the driver's entry from the lock file or change its version constraint to lock it again", name, p.target, d.Source.URL)
	}
	drivers, err := p.registryIndex(dbc.Registry{BaseURL: bound.BaseURL})
	if err != nil {
		return plannedDriver{}, fmt.Errorf("could not fetch the index of registry %s to complete the lock entry of driver %s: %w",
			d.Source.URL, name, redactRegistryError(err, p.registries, bound.BaseURL))
	}
	drv, err := findDriver(name, drivers)
	if err != nil {
		return plannedDriver{}, err
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

// signerFor returns the key that packages of a driver from the registry with
// the given identity must verify with. Only the built-in Columnar key exists
// for now; this is the one place that would choose a key per registry.
//
// TODO: choose the key from the registry configuration: the key named by the
// registry's signing_key, the Columnar key when it is omitted, and no key when
// it is false, which the lock file records as an explicit unsigned signer
// type. Until then every registry requires the Columnar key, and --no-verify
// only skips the verification of one run without changing the pin.
func (p *syncPlanner) signerFor(name, registryIdentity string) (*dbc.SigningKey, error) {
	key, err := dbc.ColumnarSigningKey()
	if err != nil {
		return nil, fmt.Errorf("driver %s: could not load the signing key: %w", name, err)
	}
	return key, nil
}

// trustedSigner returns the trusted key that a lock entry pins. Only the
// built-in Columnar key is trusted for now.
func trustedSigner(name string, sig lockedSigner) (*dbc.SigningKey, error) {
	if sig.Type == lockSignerOpenPGP {
		key, err := dbc.ColumnarSigningKey()
		if err != nil {
			return nil, fmt.Errorf("driver %s: could not load the signing key: %w", name, err)
		}
		if key.Fingerprint() == sig.Fingerprint {
			return key, nil
		}
	}
	return nil, fmt.Errorf("driver %s: the lock file pins the signing key %s, which is not trusted by this version of dbc; %s",
		name, sig.Fingerprint, signerChangeHint)
}

// signerChangeHint tells how to accept a change of the pinned signing key
// without editing the lock file by hand.
//
// TODO: suggest `dbc lock --upgrade-driver <name>` once it exists, so that
// the other drivers keep their locked versions.
const signerChangeHint = "if the change of key is intended, remove the lock file and run `dbc lock` to create it again, " +
	"which may also select newer versions of the other drivers"

// signerChangedError reports that resolving a driver again would replace the
// signing key that its lock entry pins.
func signerChangedError(name string, locked lockedSigner) error {
	return fmt.Errorf("driver %s: the lock file pins the signing key %s, but its packages would now be verified with a different key; %s",
		name, locked.Fingerprint, signerChangeHint)
}

// lockedSignerOf returns the lock file spelling of the policy for key.
func lockedSignerOf(key *dbc.SigningKey) lockedSigner {
	return lockedSigner{Type: lockSignerOpenPGP, Fingerprint: key.Fingerprint()}
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
	version := pin
	if version == nil {
		var err error
		version, err = driver.GetLatestVersionAnyPlatform(spec.Version, spec.Prerelease == "allow")
		if err != nil {
			return dbc.PkgInfo{}, err
		}
	}
	return withExactVersion(*driver, version).GetPackage(version, platform, true)
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
	// A driver with a source is resolved from one complete index. Any other
	// driver needs every registry's index, so a failed one is passed on.
	var indexErr error
	if target == nil {
		indexErr = p.registryErr
	}
	pkg, err := dbc.ResolvePackage(drivers, indexErr, name, func(candidate *dbc.Driver) (dbc.PkgInfo, error) {
		return selectProjectPackage(candidate, spec, pin, p.target)
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
	// one configured registry.
	if _, err := p.bind(name, identity); err != nil {
		return plannedDriver{}, err
	}
	signer, err := p.signerFor(name, identity)
	if err != nil {
		return plannedDriver{}, err
	}
	artifacts, err := p.lockArtifacts(drv, pkg)
	if err != nil {
		return plannedDriver{}, fmt.Errorf("driver %s: %w", name, redactRegistryError(err, p.registries, sourceURL))
	}
	return plannedDriver{Name: name, Package: pkg, Signer: signer, Entry: lockedDriver{
		Name:      name,
		Version:   pkg.Version.String(),
		Signer:    lockedSignerOf(signer),
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
