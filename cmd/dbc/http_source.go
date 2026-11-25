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
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/resolution"
)

// sourceTypeHTTP is a driver source that names the URL of the package of
// each platform with a template, for drivers that are not in a registry.
const sourceTypeHTTP = "http"

// httpSourceKeys are the keys an HTTP source may have. Any other key is an
// error, so that a key that a newer dbc understands, such as a per-target
// override, is never silently ignored.
var httpSourceKeys = []string{"type", "url", "checksum_url", "signing_key"}

// httpSource is a validated HTTP source of a driver list.
type httpSource struct {
	url urlTemplate
	// checksum, when set, is the template of the URL of a SHA-256 checksum
	// file that gives the archive hash of each target, so that dbc lock does
	// not download the archives.
	checksum *urlTemplate
	// signingKey is nil for the Columnar key, false for an unsigned source
	// or the name of a [signing_keys] definition of the same dbc.toml.
	signingKey any
}

// http validates s as an HTTP source.
func (s driverSource) http() (httpSource, error) {
	if s.Type != sourceTypeHTTP {
		return httpSource{}, fmt.Errorf("source type %q is not %q", s.Type, sourceTypeHTTP)
	}
	if s.Name != nil {
		return httpSource{}, errors.New("an HTTP source has no name; give its url")
	}
	if s.URL == nil {
		return httpSource{}, errors.New("an HTTP source must have url")
	}
	u, err := parseURLTemplate("url", *s.URL)
	if err != nil {
		return httpSource{}, err
	}
	src := httpSource{url: u}
	if s.ChecksumURL != nil {
		c, err := parseURLTemplate("checksum_url", *s.ChecksumURL)
		if err != nil {
			return httpSource{}, err
		}
		if u.scheme == "https" && c.scheme != "https" {
			return httpSource{}, errors.New("checksum_url must use https when url does, so that the hash of an https download does not come over plain http")
		}
		src.checksum = &c
	}
	switch v := s.SigningKey.(type) {
	case nil:
	case bool:
		if v {
			return httpSource{}, errors.New("signing_key = true is not valid; name a [signing_keys] definition, omit signing_key for the Columnar key, or set it to false for unsigned packages")
		}
		src.signingKey = false
	case string:
		switch v {
		case "":
			return httpSource{}, errors.New("signing_key must not be empty")
		case "columnar":
			return httpSource{}, errors.New(`signing_key = "columnar" is not valid; omit signing_key to use the built-in Columnar key`)
		}
		src.signingKey = v
	default:
		return httpSource{}, fmt.Errorf("signing_key must be the name of a [signing_keys] definition or false, not %T", s.SigningKey)
	}
	return src, nil
}

// policy returns the signing policy that the source selects. A key name is
// looked up in the project's [signing_keys] only.
func (s httpSource) policy(lookup func(string) (dbc.SigningPolicy, error)) (dbc.SigningPolicy, error) {
	switch v := s.signingKey.(type) {
	case nil:
		return dbc.SigningPolicy{}, nil
	case string:
		if lookup == nil {
			return dbc.SigningPolicy{}, fmt.Errorf("signing_key %q is not defined in [signing_keys] of the driver list", v)
		}
		return lookup(v)
	default:
		return dbc.Unsigned(), nil
	}
}

// identity returns the lock file identity of the source: the digest of its
// declared archive URL templates. It changes when the templates do, so that
// a lock entry is never replayed from other URLs. The checksum URL is not
// part of it: it only says where to read the hash of an archive once. The
// encoding has a slot for per-target overrides, empty for now, so that a
// source without overrides keeps its identity when they are added.
func (s httpSource) identity() string {
	h := sha256.New()
	field := func(v string) { fmt.Fprintf(h, "%d:%s;", len(v), v) }
	field("dbc-http-source-v1")
	field(s.url.raw)
	field("0") // the number of per-target archive URL overrides
	return lockHashPrefix + hex.EncodeToString(h.Sum(nil))
}

// lockedSource returns the source of a lock entry of the source.
func (s httpSource) lockedSource() lockedSource {
	return lockedSource{Type: lockSourceHTTP, TemplateSHA256: s.identity()}
}

// urlTemplate is the URL of a file of a target and version, where {version},
// {os} and {arch} in its path stand for the full version and the target's
// operating system and architecture as dbc spells them (linux, amd64).
type urlTemplate struct {
	raw    string
	scheme string
}

var templateVarRE = regexp.MustCompile(`\{([^{}]*)\}`)

// parseURLTemplate checks the template of field.
func parseURLTemplate(field, raw string) (urlTemplate, error) {
	scheme, rest, ok := strings.Cut(raw, "://")
	scheme = strings.ToLower(scheme)
	if !ok || (scheme != "http" && scheme != "https") {
		return urlTemplate{}, fmt.Errorf("%s must be an absolute http or https URL", field)
	}
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return urlTemplate{}, fmt.Errorf("%s must have a path", field)
	}
	authority, p := rest[:slash], rest[slash:]
	if strings.ContainsAny(authority, "{}") {
		return urlTemplate{}, fmt.Errorf("%s may use {version}, {os} and {arch} in its path only", field)
	}
	if strings.Contains(authority, "@") || strings.ContainsAny(raw, "?#") {
		return urlTemplate{}, fmt.Errorf("%s must not contain user information, a query or a fragment", field)
	}
	var unknown string
	rest = templateVarRE.ReplaceAllStringFunc(p, func(m string) string {
		if name := m[1 : len(m)-1]; name != "version" && name != "os" && name != "arch" && unknown == "" {
			unknown = m
		}
		return ""
	})
	if unknown != "" {
		return urlTemplate{}, fmt.Errorf("%s uses %s; only {version}, {os} and {arch} are supported", field, unknown)
	}
	if strings.ContainsAny(rest, "{}") {
		return urlTemplate{}, fmt.Errorf("%s has an unmatched { or }", field)
	}
	t := urlTemplate{raw: raw, scheme: scheme}
	if _, err := t.expand(semver.MustParse("1.0.0"), resolution.Target{OS: "linux", Arch: "amd64"}); err != nil {
		return urlTemplate{}, fmt.Errorf("%s: %w", field, err)
	}
	return t, nil
}

// expand returns the URL of the file of version v for target t.
func (t urlTemplate) expand(v *semver.Version, target resolution.Target) (*url.URL, error) {
	s := strings.NewReplacer("{version}", v.String(), "{os}", target.OS, "{arch}", target.Arch).Replace(t.raw)
	if err := validateLockedURL(s); err != nil {
		return nil, fmt.Errorf("the URL for %s %s is not valid: %w", targetTuple(target), v, err)
	}
	return url.Parse(s)
}

// exactHTTPVersion returns the version that the version requirement of an
// HTTP source pins: a single "=" followed by a full semantic version, as
// written in dbc.toml, so that build metadata is kept.
func exactHTTPVersion(raw any) (*semver.Version, error) {
	s, ok := raw.(string)
	if !ok {
		return nil, errors.New(`a driver with an HTTP source needs an exact version, such as version = "=1.2.3"`)
	}
	exact, ok := strings.CutPrefix(s, "=")
	if !ok || strings.HasPrefix(exact, "=") {
		return nil, fmt.Errorf(`a driver with an HTTP source needs an exact version, such as version = "=1.2.3", not %q`, s)
	}
	v, err := semver.StrictNewVersion(exact)
	if err != nil {
		return nil, fmt.Errorf(`a driver with an HTTP source needs an exact version, such as version = "=1.2.3", not %q: %w`, s, err)
	}
	return v, nil
}

// maxChecksumFile limits the size of a checksum file that dbc reads, and
// maxChecksumLine the length of each of its lines.
const (
	maxChecksumFile = 1 << 20
	maxChecksumLine = 4096
)

// checksumFor returns the SHA-256 that a checksum file gives for the archive
// at archive. The file is either a single hash, when only this archive uses
// it, or lines of a hash and a file name as sha256sum writes them ("<hash>
// <name>" or "<hash> *<name>"), matched against the last segment of the
// archive URL.
func checksumFor(body []byte, archive *url.URL, single bool) (string, error) {
	if len(body) > maxChecksumFile {
		return "", fmt.Errorf("the checksum file is larger than %d bytes", maxChecksumFile)
	}
	name, err := url.PathUnescape(path.Base(archive.EscapedPath()))
	if err != nil || name == "" || name == "/" || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("the archive URL has no file name to look up in the checksum file")
	}
	var (
		found string
		bare  []string
	)
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, maxChecksumLine), maxChecksumLine)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) < 64 || !isHex(line[:64]) {
			return "", errors.New("the checksum file has a line that does not start with a SHA-256 hash")
		}
		hash, rest := strings.ToLower(line[:64]), line[64:]
		if rest == "" {
			bare = append(bare, hash)
			continue
		}
		file, ok := strings.CutPrefix(rest, " ")
		if !ok || (file != "" && file[0] != ' ' && file[0] != '*') {
			return "", errors.New("the checksum file has a line that is not a hash and a file name as sha256sum writes them")
		}
		if file = file[1:]; file != name {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("the checksum file lists %s more than once", name)
		}
		found = hash
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("could not read the checksum file: %w", err)
	}
	switch {
	case len(bare) > 0 && (found != "" || len(bare) > 1 || !single):
		return "", errors.New("a checksum file with a hash alone must hold one hash and serve one archive")
	case len(bare) == 1:
		return bare[0], nil
	case found == "":
		return "", fmt.Errorf("the checksum file does not list %s", name)
	}
	return found, nil
}

func isHex(s string) bool {
	return !slices.ContainsFunc([]byte(s), func(c byte) bool {
		return !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F')
	})
}
