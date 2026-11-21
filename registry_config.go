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

package dbc

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/columnar-tech/dbc/internal/registryurl"
	"github.com/pelletier/go-toml/v2"
)

// RegistryEntry is a single registry declared in a global config.toml or a
// project's dbc.toml.
type RegistryEntry struct {
	URL  string `toml:"url"`
	Name string `toml:"name,omitempty"`
	// SigningKey selects the key that the registry's driver packages are
	// verified with. When it is omitted (nil), the built-in Columnar key is
	// used. A string names a [signing_keys] definition in the same file; the
	// name is never looked up in another file. false declares that the
	// registry's packages are not signed. Any other value is an error.
	SigningKey any `toml:"signing_key,omitempty"`
}

// SigningKeyTypeOpenPGP is the only supported SigningKeyDefinition.Type.
const SigningKeyTypeOpenPGP = "openpgp"

// reservedSigningKeyName is the name of the built-in Columnar key. It cannot
// be defined or referenced; omitting signing_key selects that key.
const reservedSigningKeyName = "columnar"

// SigningKeyDefinition is a [signing_keys.<name>] table. Exactly one of
// PublicKey (an ASCII-armored OpenPGP public key) and PublicKeyFile (the path
// of a file holding one, armored or binary) must be set. A relative
// PublicKeyFile is resolved against the directory of the file that defines
// it.
type SigningKeyDefinition struct {
	Type          string  `toml:"type"`
	PublicKey     *string `toml:"public_key,multiline,omitempty"`
	PublicKeyFile *string `toml:"public_key_file,omitempty"`
}

// GlobalConfig is the schema of a user's global dbc config.toml.
type GlobalConfig struct {
	Registries      []RegistryEntry `toml:"registries"`
	ReplaceDefaults bool            `toml:"replace_defaults,omitempty"`
	// SigningKeys are the keys that Registries can name in signing_key.
	SigningKeys map[string]SigningKeyDefinition `toml:"signing_keys,omitempty"`
	// Network holds settings that change where requests are sent. They are
	// only read from the user's config, never from a project's dbc.toml.
	Network NetworkConfig `toml:"network,omitempty"`
	// Path is the file the config was read from. LoadGlobalConfig sets it.
	Path string `toml:"-"`
}

// ProjectConfig is the registry configuration of a project's dbc.toml.
type ProjectConfig struct {
	Registries []RegistryEntry
	// ReplaceDefaults is a tri-state: nil inherits the global config, &true
	// replaces both global and built-in default registries, and &false keeps
	// the defaults even when the global config replaces them.
	ReplaceDefaults *bool
	// SigningKeys are the keys that Registries can name in signing_key.
	SigningKeys map[string]SigningKeyDefinition
	// Path is the file the config was read from.
	Path string
}

// LoadGlobalConfig reads config.toml from configDir. It returns (nil, nil) if
// the file does not exist; every entry is validated.
func LoadGlobalConfig(configDir string) (*GlobalConfig, error) {
	configPath := filepath.Join(configDir, "config.toml")
	f, err := os.Open(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var cfg GlobalConfig
	if err := toml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", configPath, err)
	}
	cfg.Path = absPath(configPath)

	// Note: a global replace_defaults=true with no [[registries]] is NOT rejected
	// here, because a project's dbc.toml may supply the entries at NewClient
	// time. The "zero resulting registries" case is enforced after merging in
	// NewClient so both library and CLI callers share the same semantics.
	if _, err := registriesFromEntries(cfg.Registries, cfg.SigningKeys, cfg.Path); err != nil {
		return nil, fmt.Errorf("%s: %w", configPath, err)
	}

	if _, err := newURLRouter(cfg.Network.URLReplacements); err != nil {
		return nil, fmt.Errorf("%s: %w", configPath, err)
	}

	return &cfg, nil
}

// ErrProjectNetwork is returned when a project's dbc.toml contains a network
// table. Network settings such as URL replacements are only read from the
// user's config, so a project cannot redirect requests.
var ErrProjectNetwork = errors.New("network settings are only read from the user config, not from dbc.toml; remove the [network] table")

// LoadProjectConfig reads only the registry configuration (registries,
// replace_defaults and signing_keys) from a project's dbc.toml, leaving
// driver specifications to the caller. Missing files and malformed entries
// are errors.
//
// Pass the result to WithProjectConfig. Whether the resulting registry list
// is empty is checked by NewClient after merging global/project config.
func LoadProjectConfig(path string) (*ProjectConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	var cfg struct {
		Registries      []RegistryEntry                 `toml:"registries"`
		ReplaceDefaults *bool                           `toml:"replace_defaults"`
		SigningKeys     map[string]SigningKeyDefinition `toml:"signing_keys"`
		Network         map[string]any                  `toml:"network"`
	}
	if err := toml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", path, err)
	}
	if cfg.Network != nil {
		return nil, fmt.Errorf("%s: %w", path, ErrProjectNetwork)
	}
	project := &ProjectConfig{
		Registries:      cfg.Registries,
		ReplaceDefaults: cfg.ReplaceDefaults,
		SigningKeys:     cfg.SigningKeys,
		Path:            absPath(path),
	}
	if _, err := registriesFromEntries(project.Registries, project.SigningKeys, project.Path); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return project, nil
}

// absPath returns path made absolute, so that a relative public_key_file
// keeps its base when the working directory changes. The path is not
// resolved through symbolic links. An empty path, a configuration that does
// not come from a file, stays empty.
func absPath(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// Validate checks the registry entries and signing key definitions of the
// project on their own, without merging them with other configuration or
// reading key files.
func (c *ProjectConfig) Validate() error {
	_, err := registriesFromEntries(c.Registries, c.SigningKeys, c.Path)
	return err
}

// registriesFromEntries validates the registry entries and signing key
// definitions of one configuration file, and returns the registries with
// their signing policies. Every definition is checked, including ones that
// no registry names; a signing_key name is looked up only in keys, the
// definitions of the same file. path is the file the configuration was read
// from, or empty when it was built in memory.
func registriesFromEntries(entries []RegistryEntry, keys map[string]SigningKeyDefinition, path string) ([]Registry, error) {
	policies := make(map[string]RegistrySigningPolicy, len(keys))
	for _, name := range slices.Sorted(maps.Keys(keys)) {
		policy, err := signingPolicyFromDefinition(name, keys[name], path)
		if err != nil {
			return nil, fmt.Errorf("signing key %q: %w", name, err)
		}
		policies[name] = policy
	}

	registries := make([]Registry, 0, len(entries))
	for _, e := range entries {
		u, err := parseRegistryEntryURL(e)
		if err != nil {
			return nil, err
		}
		policy, err := entrySigningPolicy(e.SigningKey, policies)
		if err != nil {
			return nil, fmt.Errorf("registry %s: %w", e.URL, err)
		}
		registries = append(registries, Registry{Name: e.Name, BaseURL: u, Signing: policy})
	}
	return registries, nil
}

// signingPolicyFromDefinition checks one [signing_keys.<name>] definition of
// the configuration file at path and returns the policy that a registry
// naming it gets. An inline public_key is parsed now; a public_key_file is
// only read when a registry needs its key.
func signingPolicyFromDefinition(name string, def SigningKeyDefinition, path string) (RegistrySigningPolicy, error) {
	switch {
	case name == "":
		return RegistrySigningPolicy{}, errors.New("name must not be empty")
	case name == reservedSigningKeyName:
		return RegistrySigningPolicy{}, fmt.Errorf("the name %q is reserved for the built-in Columnar key, which is used when signing_key is omitted", name)
	case def.Type == "":
		return RegistrySigningPolicy{}, fmt.Errorf("type is required; the supported type is %q", SigningKeyTypeOpenPGP)
	case def.Type != SigningKeyTypeOpenPGP:
		return RegistrySigningPolicy{}, fmt.Errorf("unsupported type %q; the supported type is %q", def.Type, SigningKeyTypeOpenPGP)
	case (def.PublicKey == nil) == (def.PublicKeyFile == nil):
		return RegistrySigningPolicy{}, errors.New("exactly one of public_key and public_key_file must be set")
	case def.PublicKey != nil && *def.PublicKey == "":
		return RegistrySigningPolicy{}, errors.New("public_key must not be empty")
	case def.PublicKeyFile != nil:
		file, err := newSigningKeyFile(*def.PublicKeyFile, path)
		if err != nil {
			return RegistrySigningPolicy{}, err
		}
		return RegistrySigningPolicy{kind: signingFromFile, file: file, name: name}, nil
	}
	key, err := NewArmoredSigningKey(*def.PublicKey)
	if err != nil {
		return RegistrySigningPolicy{}, fmt.Errorf("public_key: %w", err)
	}
	return RegistrySigningPolicy{kind: signingWithKey, key: key, name: name}, nil
}

// entrySigningPolicy returns the policy that a registry entry's signing_key
// selects, looking a name up in the policies of the same file.
func entrySigningPolicy(value any, policies map[string]RegistrySigningPolicy) (RegistrySigningPolicy, error) {
	switch v := value.(type) {
	case nil:
		return RegistrySigningPolicy{}, nil
	case bool:
		if v {
			return RegistrySigningPolicy{}, errors.New("signing_key = true is not valid; name a [signing_keys] definition, omit signing_key for the Columnar key, or set it to false for an unsigned registry")
		}
		return Unsigned(), nil
	case string:
		switch {
		case v == "":
			return RegistrySigningPolicy{}, errors.New("signing_key must not be empty")
		case v == reservedSigningKeyName:
			return RegistrySigningPolicy{}, errors.New(`signing_key = "columnar" is not valid; omit signing_key to use the built-in Columnar key`)
		}
		policy, ok := policies[v]
		if !ok {
			return RegistrySigningPolicy{}, fmt.Errorf("signing_key %q is not defined in [signing_keys] of the same file", v)
		}
		return policy, nil
	default:
		return RegistrySigningPolicy{}, fmt.Errorf("signing_key must be the name of a [signing_keys] definition or false, not %T", value)
	}
}

func parseRegistryEntryURL(e RegistryEntry) (*url.URL, error) {
	if e.URL == "" {
		return nil, errors.New("registry entry has empty url")
	}
	u, err := registryurl.Parse(e.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid registry URL %w", err)
	}
	return u, nil
}

// registryURLKey returns a canonical form of a registry URL that collapses
// only truly no-op differences: scheme/host casing, trailing-slash on the
// path, and fragments. Query, userinfo, and path segments are preserved
// because they change the effective registry endpoint (tenant selectors,
// credential-bearing URLs, path-mounted registries) and must be treated as
// distinct registries.
func registryURLKey(u *url.URL) string {
	cp := *u
	cp.Scheme = strings.ToLower(cp.Scheme)
	cp.Host = strings.ToLower(cp.Host)
	cp.Path = strings.TrimRight(cp.Path, "/")
	cp.Fragment = ""
	cp.RawFragment = ""
	return cp.String()
}

// checkProjectRedeclarations rejects a project registry that declares the URL
// of a registry from the user's config, or of a built-in default registry,
// with a different signing policy. A project's dbc.toml can add registries,
// but it cannot change how a registry that the user or dbc already trusts is
// verified. The user's config is compared first; the defaults are compared
// unless the user's config replaces them, since a project's replace_defaults
// does not lift this check. Policies are the same when they verify with the
// same certificate; an inline copy of the Columnar key is the same as the
// built-in key. Policies that read a key file are never the same, because the
// files are not read here; the project's declaration has to be removed
// instead.
func checkProjectRedeclarations(projectRegs, globalRegs []Registry, globalReplaceDefaults bool, defaults []Registry) error {
	trusted := globalRegs
	if !globalReplaceDefaults {
		trusted = append(slices.Clip(globalRegs), defaults...)
	}
	declared := make(map[string]Registry)
	for _, r := range trusted {
		if r.BaseURL == nil {
			continue
		}
		key := registryURLKey(r.BaseURL)
		if _, ok := declared[key]; !ok {
			declared[key] = r
		}
	}
	for _, p := range projectRegs {
		if p.BaseURL == nil {
			continue
		}
		existing, ok := declared[registryURLKey(p.BaseURL)]
		if !ok {
			continue
		}
		same, err := p.Signing.sameKeyWithoutFiles(existing.Signing)
		if err != nil {
			return fmt.Errorf("registry %s: %w", registryURLForMessage(p.BaseURL), err)
		}
		if same {
			continue
		}
		return fmt.Errorf("registry %s in the project is also declared by the user config or by dbc's defaults, with %s; a project cannot change how that registry is verified (it declares %s), so remove it from the project's registries",
			registryURLForMessage(p.BaseURL), existing.Signing, p.Signing)
	}
	return nil
}

// registryURLForMessage returns u without the user information and query,
// which can hold credentials, for use in error messages.
func registryURLForMessage(u *url.URL) string {
	cp := *u
	cp.User = nil
	cp.RawQuery = ""
	cp.Fragment = ""
	cp.RawFragment = ""
	return cp.String()
}

// mergeRegistries combines project, global, and default registries into a
// deduplicated list in priority order: project first, then global, then
// built-in defaults (unless either global or project overrides with
// replace_defaults). A registry is kept whole, with its signing policy, from
// the first tier that declares its URL.
func mergeRegistries(
	projectRegs []Registry,
	projectReplaceDefaults *bool,
	globalRegs []Registry,
	globalReplaceDefaults bool,
	defaults []Registry,
) []Registry {
	replaceDefaults := globalReplaceDefaults
	if projectReplaceDefaults != nil {
		replaceDefaults = *projectReplaceDefaults
	}

	seen := make(map[string]bool)
	var result []Registry

	add := func(registries []Registry) {
		for _, r := range registries {
			if r.BaseURL == nil {
				continue
			}
			key := registryURLKey(r.BaseURL)
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, r)
		}
	}

	add(projectRegs)
	add(globalRegs)
	if !replaceDefaults {
		add(defaults)
	}

	return result
}

// WithGlobalConfig applies the registries declared in a loaded global config
// on top of the client's built-in defaults. Pass nil (or an empty config) to
// leave the default registry set untouched.
//
// WithBaseURL takes precedence over this option — when set, registries from
// the global config are ignored.
func WithGlobalConfig(cfg *GlobalConfig) Option {
	return func(c *clientConfig) {
		c.globalConfig = cfg
	}
}

// WithProjectConfig applies the registry configuration of a project's
// dbc.toml, usually read with LoadProjectConfig, on top of the global and
// default registries. Its ReplaceDefaults takes priority over the same flag
// in the global config; nil inherits the global config's value.
//
// NewClient returns an error if any entry or signing key definition is
// malformed or if the merged registry list ends up empty (e.g.
// ReplaceDefaults=true with no project or global entries).
// ReplaceDefaults=true is valid on its own when a non-empty global config
// supplies the entries — the post-merge check is what matters.
//
// WithBaseURL takes precedence over this option — when set, project
// registries are ignored.
func WithProjectConfig(cfg *ProjectConfig) Option {
	var cp *ProjectConfig
	if cfg != nil {
		cp = &ProjectConfig{
			Registries:      slices.Clone(cfg.Registries),
			ReplaceDefaults: cfg.ReplaceDefaults,
			SigningKeys:     maps.Clone(cfg.SigningKeys),
			Path:            cfg.Path,
		}
	}
	return func(c *clientConfig) {
		c.project = cp
	}
}
