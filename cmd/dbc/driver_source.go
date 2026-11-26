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
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/registryurl"
)

// isSelectionError reports whether err is an error of
// dbc.SigningOperation.ResolvePackage that choosing the registry explicitly avoids: a missing index of a
// registry that might list the driver with another signer, or registries
// with different signers that list the driver.
func isSelectionError(err error) bool {
	var conflict *dbc.SignerConflictError
	return errors.Is(err, dbc.ErrIncompleteIndex) || errors.As(err, &conflict)
}

// Hints that tell how to choose the registry of a driver explicitly.
const (
	sourceHint   = "to resolve the driver from one registry only, set its source in dbc.toml, for example source = { type = \"registry\", url = \"https://dbc-cdn.columnar.tech\" }"
	registryHint = "to take the driver from one registry only, pass --registry with the name or URL of that registry"
)

// sourceTypeRegistry is the only driver source type: a registry that is
// part of the registry configuration.
const sourceTypeRegistry = "registry"

// driverSource selects where a driver of a driver list comes from. A
// registry source names one entry of [[registries]] in the same file, or
// gives the URL of a configured registry. The driver is then resolved from
// that registry only.
type driverSource struct {
	Type string  `toml:"type"`
	Name *string `toml:"name,omitempty"`
	URL  *string `toml:"url,omitempty"`
}

// registrySourceFlag parses the value of a --registry option: a value with
// a scheme is a URL, anything else a registry name.
func registrySourceFlag(v string) *driverSource {
	if v == "" {
		return nil
	}
	if strings.Contains(v, "://") {
		return &driverSource{Type: sourceTypeRegistry, URL: &v}
	}
	return &driverSource{Type: sourceTypeRegistry, Name: &v}
}

// String describes the source for messages. A URL source is shown by its
// registry identity, so credentials never appear.
func (s driverSource) String() string {
	if s.Name != nil {
		return fmt.Sprintf("registry %q", *s.Name)
	}
	if s.URL != nil {
		if u, err := url.Parse(*s.URL); err == nil {
			if id, err := canonicalRegistryIdentity(u); err == nil {
				return "registry " + id
			}
		}
	}
	return "registry"
}

// equal reports whether two sources are spelled the same way.
func (s *driverSource) equal(o *driverSource) bool {
	if s == nil || o == nil {
		return s == o
	}
	eq := func(a, b *string) bool {
		if a == nil || b == nil {
			return a == b
		}
		return *a == *b
	}
	return s.Type == o.Type && eq(s.Name, o.Name) && eq(s.URL, o.URL)
}

// validateDriverSources checks the source of every driver of list against
// the registries the same file declares.
func validateDriverSources(list DriversList) error {
	for _, name := range slices.Sorted(maps.Keys(list.Drivers)) {
		src := list.Drivers[name].Source
		if src == nil {
			continue
		}
		if _, err := src.target(list.Registries); err != nil {
			return fmt.Errorf("driver %s: %w", name, err)
		}
	}
	return nil
}

// sourceTarget is the registry a source selects.
type sourceTarget struct {
	source driverSource
	// identity is the registry identity, as recorded in the lock file.
	identity string
	// declared is the URL of the [[registries]] entry a name refers to, or
	// nil for a URL source.
	declared *url.URL
}

// target validates the source and returns the registry it selects. A name
// refers to entries, the registries declared in the same file.
func (s driverSource) target(entries []dbc.RegistryEntry) (sourceTarget, error) {
	switch s.Type {
	case sourceTypeRegistry:
	case "":
		return sourceTarget{}, fmt.Errorf("source type is required; use type = %q", sourceTypeRegistry)
	default:
		return sourceTarget{}, fmt.Errorf("source type %q is not supported by this version of dbc, which supports type = %q; a newer version of dbc may be required", s.Type, sourceTypeRegistry)
	}
	var (
		u        *url.URL
		declared *url.URL
		err      error
	)
	switch {
	case s.Name != nil && s.URL != nil:
		return sourceTarget{}, errors.New("source must have either name or url, not both")
	case s.Name != nil:
		i := slices.IndexFunc(entries, func(e dbc.RegistryEntry) bool { return e.Name == *s.Name })
		if *s.Name == "" || i < 0 {
			return sourceTarget{}, fmt.Errorf("source refers to registry %q, which [[registries]] in the same file does not declare", *s.Name)
		}
		if u, err = registryurl.Parse(entries[i].URL); err != nil {
			return sourceTarget{}, fmt.Errorf("registry %q: invalid URL: %w", *s.Name, err)
		}
		declared = u
	case s.URL != nil:
		if u, err = parseSourceURL(*s.URL); err != nil {
			return sourceTarget{}, err
		}
	default:
		return sourceTarget{}, errors.New("source must have name or url")
	}
	identity, err := canonicalRegistryIdentity(u)
	if err != nil {
		return sourceTarget{}, err
	}
	return sourceTarget{source: s, identity: identity, declared: declared}, nil
}

// parseSourceURL parses the url of a source. It locates a registry by its
// identity, so it cannot carry anything the identity leaves out.
func parseSourceURL(raw string) (*url.URL, error) {
	u, err := registryurl.Parse(raw)
	if err != nil {
		// Keep a credential in the URL out of the message.
		return nil, errors.New("source url must be an absolute http or https URL with a host")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return nil, errors.New("source url must not contain user information, a query or a fragment; " +
			"declare such a registry in [[registries]] with a name and refer to that name")
	}
	return u, nil
}

// bind finds the configured registry the target selects. It must be the
// only configured registry with that identity, since the lock file records
// only the identity, and for a name it must be the declared entry.
func (t sourceTarget) bind(registries []dbc.Registry) (dbc.Registry, error) {
	var matches []dbc.Registry
	for _, r := range registries {
		if r.BaseURL == nil {
			continue
		}
		if id, err := canonicalRegistryIdentity(r.BaseURL); err == nil && id == t.identity {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 0:
		return dbc.Registry{}, fmt.Errorf("%s (%s) is not among the configured registries", t.source, t.identity)
	case 1:
	default:
		return dbc.Registry{}, fmt.Errorf("%s matches %d configured registries with the identity %s; "+
			"they differ only in user information or query, which the lock file cannot tell apart, so keep only one of them",
			t.source, len(matches), t.identity)
	}
	r := matches[0]
	if t.declared != nil && normalizeRegistryURL(r.BaseURL.String()) != normalizeRegistryURL(t.declared.String()) {
		return dbc.Registry{}, fmt.Errorf("%s is replaced by another registry configuration with the identity %s", t.source, t.identity)
	}
	return r, nil
}

// sameSourceTarget reports whether src selects the same registry with the
// registries declared in before and in after.
func sameSourceTarget(src driverSource, before, after []dbc.RegistryEntry) bool {
	a, errA := src.target(before)
	b, errB := src.target(after)
	if errA != nil || errB != nil {
		return false
	}
	if a.identity != b.identity || (a.declared == nil) != (b.declared == nil) {
		return false
	}
	return a.declared == nil || normalizeRegistryURL(a.declared.String()) == normalizeRegistryURL(b.declared.String())
}
