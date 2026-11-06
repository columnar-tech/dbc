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
		spec := list.Drivers[name]
		if spec.Version != nil && spec.Prerelease == "allow" {
			spec.Version.IncludePrerelease = true
		}

		if d, ok := locked[name]; ok {
			v, err := semver.NewVersion(d.Version)
			if err != nil {
				return nil, nil, fmt.Errorf("driver %s: invalid locked version: %w", name, err)
			}
			if lockSatisfies(spec, v) {
				pd, err := p.fromLock(name, d, v)
				if err != nil {
					return nil, nil, err
				}
				planned = append(planned, pd)
				continue
			}
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

		pd, err := p.resolve(name, spec, pin)
		if err != nil {
			return nil, nil, err
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
		return plannedDriver{Name: name, Entry: d, ArchiveSize: a.Size, Package: dbc.PkgInfo{
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
	pkg, err := drv.GetPackage(v, p.target, true)
	if err != nil {
		return plannedDriver{}, fmt.Errorf("could not complete the lock entry of driver %s: %w", name, redactRegistryError(err, p.registries, bound.BaseURL))
	}
	art, err := newLockedArtifact(bound.BaseURL, pkg.Path, p.target, publishedHash(pkg))
	if err != nil {
		return plannedDriver{}, fmt.Errorf("driver %s: %w", name, redactRegistryError(err, p.registries, bound.BaseURL))
	}
	d.Artifacts = append(slices.Clone(d.Artifacts), art)
	return plannedDriver{Name: name, Entry: d, Package: pkg}, nil
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

// resolve picks the package for a driver from the merged registries, keeping
// pin (a version from a v1 lock file) when it is set.
func (p *syncPlanner) resolve(name string, spec driverSpec, pin *semver.Version) (plannedDriver, error) {
	drivers, err := p.loadIndex()
	if err != nil {
		return plannedDriver{}, err
	}
	drv, err := findDriver(name, drivers)
	if err != nil {
		return plannedDriver{}, wrapWithRegistryContext(err, p.registryErr)
	}
	var sourceURL *url.URL
	if drv.Registry != nil {
		sourceURL = drv.Registry.BaseURL
	}

	var pkg dbc.PkgInfo
	switch {
	case pin != nil:
		pkg, err = drv.GetPackage(pin, p.target, spec.Prerelease == "allow")
	case spec.Version != nil:
		pkg, err = drv.GetWithConstraint(spec.Version, p.target)
	default:
		pkg, err = drv.GetPackage(nil, p.target, spec.Prerelease == "allow")
	}
	if err != nil {
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
	artifacts, err := p.lockArtifacts(drv, pkg)
	if err != nil {
		return plannedDriver{}, fmt.Errorf("driver %s: %w", name, redactRegistryError(err, p.registries, drv.Registry.BaseURL))
	}
	return plannedDriver{Name: name, Package: pkg, Entry: lockedDriver{
		Name:      name,
		Version:   pkg.Version.String(),
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

// urlPrefixLen returns the length of an "http://" or "https://" prefix of s
// (ignoring case), or 0.
func urlPrefixLen(s string) int {
	for _, prefix := range []string{"https://", "http://"} {
		if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
			return len(prefix)
		}
	}
	return 0
}

// diagnosticURLEnd returns where the URL that starts at s[start:] (after its
// scheme) ends. A quote just before the URL makes the matching quote the end.
// Once a query or fragment starts, everything is part of the URL: up to the
// closing quote, or otherwise up to the next whitespace, so that quotes and
// brackets inside a secret cannot end it early. Punctuation that closes the
// sentence is left outside.
func diagnosticURLEnd(s string, start int, quote byte) int {
	const trailing = ".,:;)]}'\""
	j := start
	for ; j < len(s); j++ {
		c := s[j]
		if c == '?' || c == '#' {
			queryStart := j
			if quote != 0 {
				for ; j < len(s) && !(s[j] == quote && s[j-1] != '\\'); j++ {
				}
				return j
			}
			for ; j < len(s) && s[j] != ' ' && s[j] != '\t' && s[j] != '\n' && s[j] != '\r'; j++ {
			}
			for j > queryStart && strings.IndexByte(trailing, s[j-1]) >= 0 {
				j--
			}
			return j
		}
		if quote != 0 {
			if c == quote {
				return j
			}
		} else if strings.IndexByte(" \t\n\r\"'()<>", c) >= 0 {
			break
		}
	}
	if quote == 0 {
		for j > start && strings.IndexByte(".,:;", s[j-1]) >= 0 {
			j--
		}
	}
	return j
}

// stripURLSecrets keeps only scheme, host and path of a URL string. It works
// on the text rather than on parsed values, so no encoding of the userinfo or
// the query can slip through.
func stripURLSecrets(raw string) string {
	schemeEnd := strings.Index(raw, "://") + len("://")
	scheme, rest := raw[:schemeEnd], raw[schemeEnd:]
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	authorityEnd := strings.Index(rest, "/")
	if authorityEnd < 0 {
		authorityEnd = len(rest)
	}
	if at := strings.LastIndex(rest[:authorityEnd], "@"); at >= 0 {
		rest = rest[at+1:]
	}
	return scheme + rest
}

// registrySecrets lists the values in the configured registry URLs that must
// never be shown: userinfo and query values, in raw and escaped spellings.
// minRedactableSecretLen is the shortest secret replaced in place; a
// diagnostic containing a shorter one is hidden entirely.
const minRedactableSecretLen = 4

func registrySecrets(registries []dbc.Registry) []string {
	seen := map[string]bool{}
	var secrets []string
	add := func(v string) {
		for _, form := range []string{v, url.QueryEscape(v), url.PathEscape(v)} {
			if form != "" && !seen[form] {
				seen[form] = true
				secrets = append(secrets, form)
			}
		}
	}
	for _, r := range registries {
		if r.BaseURL == nil {
			continue
		}
		if u := r.BaseURL.User; u != nil {
			add(u.Username())
			if password, ok := u.Password(); ok {
				add(password)
			}
		}
		for _, values := range r.BaseURL.Query() {
			for _, v := range values {
				add(v)
			}
		}
		for pair := range strings.SplitSeq(r.BaseURL.RawQuery, "&") {
			if _, v, ok := strings.Cut(pair, "="); ok {
				add(v)
			}
		}
	}
	// Longer secrets first, so one that contains another is replaced whole.
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	return secrets
}

// redactURLSecrets removes the credentials from the text of err in two
// layers. Every URL loses its userinfo, query and fragment, whatever their
// encoding. Then every value in secrets is replaced by "REDACTED", for text
// that is not part of a URL. The result still unwraps to err.
func redactURLSecrets(err error, secrets []string) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	var b strings.Builder
	for i := 0; i < len(text); {
		n := urlPrefixLen(text[i:])
		if n == 0 {
			b.WriteByte(text[i])
			i++
			continue
		}
		var quote byte
		if i > 0 && (text[i-1] == '"' || text[i-1] == '\'') {
			quote = text[i-1]
		}
		end := diagnosticURLEnd(text, i+n, quote)
		b.WriteString(stripURLSecrets(text[i:end]))
		i = end
	}
	msg := b.String()
	for _, secret := range secrets {
		if len(secret) < minRedactableSecretLen {
			// Replacing a short value inside arbitrary text could mangle
			// the message without reliably hiding the secret, so hide the
			// whole diagnostic instead.
			if strings.Contains(msg, secret) {
				return redactedError{msg: "registry request failed; details are hidden because they may contain credentials", err: err}
			}
			continue
		}
		msg = strings.ReplaceAll(msg, secret, "REDACTED")
	}
	if msg == text {
		return err
	}
	return redactedError{msg: msg, err: err}
}
