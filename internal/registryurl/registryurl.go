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
	for _, seg := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
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
