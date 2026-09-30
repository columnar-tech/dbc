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

// Package sourceidentity defines the canonical identities of declared driver
// sources. It has no dependencies outside the standard library so source
// adapters can share identity rules without importing one another.
package sourceidentity

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Kind is the type of a declared driver source.
type Kind string

const (
	Registry Kind = "registry"
	Packslip Kind = "packslip"
	Path     Kind = "path"
)

// Key is a comparable source identity. Reference contains the canonical form
// of the source-specific reference and is suitable for direct equality checks
// and map keys.
type Key struct {
	Kind      Kind
	Reference string
}

// Parse validates and canonicalizes a source reference into its identity key.
// It never substitutes malformed input with a best-effort raw-string key.
func Parse(kind Kind, reference string) (Key, error) {
	switch kind {
	case Registry:
		return parseRegistry(reference)
	case Packslip:
		return parsePackslip(reference)
	case Path:
		if reference == "" {
			return Key{}, errors.New("path source has no path")
		}
		return Key{Kind: Path, Reference: reference}, nil
	default:
		return Key{}, fmt.Errorf("unsupported driver source type %q", kind)
	}
}

func parseRegistry(reference string) (Key, error) {
	if reference == "" {
		return Key{}, errors.New("registry source has no URL")
	}

	parsed, err := url.Parse(reference)
	if err != nil {
		return Key{}, fmt.Errorf("invalid registry URL %q: %w", reference, err)
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return Key{}, fmt.Errorf("invalid registry URL %q: missing host", reference)
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return Key{}, fmt.Errorf("invalid registry URL %q: scheme must be http or https", reference)
	}

	canonical := *parsed
	canonical.Scheme = strings.ToLower(canonical.Scheme)
	canonical.Host = strings.ToLower(canonical.Host)

	// Trim only literal trailing slash bytes in the escaped path. In particular,
	// an encoded slash such as %2F remains part of the path segment and must not
	// become equivalent to a path separator.
	escapedPath := strings.TrimRight(canonical.EscapedPath(), "/")
	path, err := url.PathUnescape(escapedPath)
	if err != nil {
		return Key{}, fmt.Errorf("invalid registry URL %q: %w", reference, err)
	}
	canonical.Path = path
	if (&url.URL{Path: path}).EscapedPath() == escapedPath {
		canonical.RawPath = ""
	} else {
		canonical.RawPath = escapedPath
	}

	// Fragments are client-side only and do not affect the HTTP request. Keep
	// RawQuery and ForceQuery untouched: both are part of the configured URL.
	canonical.Fragment = ""
	canonical.RawFragment = ""
	return Key{Kind: Registry, Reference: canonical.String()}, nil
}

func parsePackslip(reference string) (Key, error) {
	if reference != strings.TrimSpace(reference) || strings.Contains(reference, "://") || strings.HasSuffix(reference, "/") {
		return Key{}, fmt.Errorf("packslip project must be a GitHub host path without a URL scheme: %q", reference)
	}
	parts := strings.Split(reference, "/")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "github.com") {
		return Key{}, fmt.Errorf("packslip project must use github.com/owner/repo[/tool...]: %q", reference)
	}
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return Key{}, fmt.Errorf("packslip project contains an invalid path segment: %q", reference)
		}
		for _, ch := range part {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("._-", ch)) {
				return Key{}, fmt.Errorf("packslip project contains an invalid path segment: %q", reference)
			}
		}
		if i < 3 {
			parts[i] = strings.ToLower(part)
		}
	}
	return Key{Kind: Packslip, Reference: strings.Join(parts, "/")}, nil
}
