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

// Package registryurl validates registry URLs. url.Parse alone accepts
// strings with no scheme or host, which are useless as a registry.
package registryurl

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Parse parses s and requires an http or https scheme and a non-empty host.
func Parse(s string) (*url.URL, error) {
	if s == "" {
		return nil, errors.New("empty url")
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("%q: %w", s, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%q: missing host", s)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%q: scheme must be http or https", s)
	}
	return u, nil
}

// Authority returns the scheme and host of u as they identify a registry:
// in lower case, and without the default port of the scheme, so that
// https://example.com and https://EXAMPLE.com:443 are the same registry.
// Every identity of a registry URL uses it, whatever else it keeps or drops.
func Authority(u *url.URL) (scheme, host string) {
	scheme, host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	if port := u.Port(); (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		host = strings.TrimSuffix(host, ":"+port)
	}
	return scheme, host
}

// CheckPath rejects a registry URL whose path does not name one location
// unambiguously: an empty segment (as in a//b), a segment that is "." or ".."
// once decoded, or an escaped "/" inside a segment. Requests resolve such a
// path to another one (url.URL.JoinPath cleans it), so two spellings of one
// registry would otherwise have different identities. A trailing slash is
// allowed.
func CheckPath(u *url.URL) error {
	p := strings.TrimSuffix(u.EscapedPath(), "/")
	if p == "" {
		return nil
	}
	for seg := range strings.SplitSeq(strings.TrimPrefix(p, "/"), "/") {
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			return fmt.Errorf("path %q: %w", u.EscapedPath(), err)
		}
		if decoded == "" || decoded == "." || decoded == ".." || strings.Contains(decoded, "/") {
			return fmt.Errorf("path %q must not contain empty, \".\" or \"..\" segments or an escaped \"/\"", u.EscapedPath())
		}
	}
	return nil
}

// EscapedPath returns the escaped path of u in the form that identifies a
// registry, following RFC 3986 section 6.2.2: an escaped unreserved
// character (a letter, a digit or one of "-._~") is decoded, and the hex
// digits of every other escape are in upper case. Reserved characters keep
// the spelling they have, escaped or not, since that can change the meaning
// of a path. Nothing is decoded twice. Requests keep using the URL as
// configured; only the comparison of registries uses this form.
func EscapedPath(u *url.URL) string {
	p := u.EscapedPath()
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] != '%' || i+2 >= len(p) || !isHex(p[i+1]) || !isHex(p[i+2]) {
			b.WriteByte(p[i])
			continue
		}
		c := unhex(p[i+1])<<4 | unhex(p[i+2])
		if isUnreserved(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteString(strings.ToUpper(p[i+1 : i+3]))
		}
		i += 2
	}
	return b.String()
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c <= '9':
		return c - '0'
	case c >= 'a':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

func isUnreserved(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~'
}

// Endpoint returns a copy of u in the form that identifies the endpoint of a
// registry: its scheme and host as Authority gives them, its path as
// EscapedPath gives it without a trailing slash, and no fragment. It keeps
// the user information and query, which can select another registry.
func Endpoint(u *url.URL) *url.URL {
	cp := *u
	cp.Scheme, cp.Host = Authority(u)
	cp.RawPath = strings.TrimRight(EscapedPath(u), "/")
	// The canonical path only decodes valid escapes, so it unescapes.
	cp.Path, _ = url.PathUnescape(cp.RawPath)
	cp.Fragment, cp.RawFragment = "", ""
	return &cp
}
