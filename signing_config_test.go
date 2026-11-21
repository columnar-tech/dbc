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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tomlString quotes s as a TOML multi-line literal string.
func tomlString(s string) string { return "'''\n" + s + "'''" }

func writeProject(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dbc.toml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoadProjectConfigSigningKeys(t *testing.T) {
	key := generateTestKey(t, "acme")
	armored := publicArmored(t, key)
	acme := "[signing_keys.acme]\ntype = \"openpgp\"\npublic_key = " + tomlString(armored) + "\n"

	path := writeProject(t, acme+`
[[registries]]
url = "https://acme.example.com"
signing_key = "acme"

[[registries]]
url = "https://unsigned.example.com"
signing_key = false

[[registries]]
url = "https://columnar.example.com"
`)
	cfg, err := LoadProjectConfig(path)
	require.NoError(t, err)
	require.Len(t, cfg.Registries, 3)
	assert.Equal(t, "acme", cfg.Registries[0].SigningKey)
	assert.Equal(t, false, cfg.Registries[1].SigningKey)
	assert.Nil(t, cfg.Registries[2].SigningKey)

	c, err := NewClient(WithProjectConfig(cfg), WithRegistries(nil))
	require.NoError(t, err)
	assert.Empty(t, c.Registries(), "WithRegistries still wins over the project config")

	c, err = NewClient(WithProjectConfig(cfg))
	require.NoError(t, err)
	regs := c.Registries()
	require.GreaterOrEqual(t, len(regs), 3)

	got, err := regs[0].ResolveSigningKey()
	require.NoError(t, err)
	assert.Equal(t, key.GetFingerprint(), got.Fingerprint())
	assert.Equal(t, `signing key "acme"`, regs[0].Signing.String())

	got, err = regs[1].ResolveSigningKey()
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.True(t, regs[1].Signing.IsUnsigned())

	got, err = regs[2].ResolveSigningKey()
	require.NoError(t, err)
	assert.Equal(t, columnarFingerprint, got.Fingerprint())
}

func TestLoadProjectConfigSigningKeyErrors(t *testing.T) {
	armored := publicArmored(t, generateTestKey(t, "acme"))
	private, err := generateTestKey(t, "private").Armor()
	require.NoError(t, err)
	def := func(name, body string) string { return fmt.Sprintf("[signing_keys.%s]\n%s\n", name, body) }
	openpgp := "type = \"openpgp\"\npublic_key = " + tomlString(armored)
	registry := func(value string) string {
		return "[[registries]]\nurl = \"https://acme.example.com\"\nsigning_key = " + value + "\n"
	}

	tests := []struct {
		name string
		toml string
		want string
	}{
		{"undefined name", registry(`"acme"`), `signing_key "acme" is not defined`},
		{"true", registry("true"), "signing_key = true is not valid"},
		{"empty name", registry(`""`), "must not be empty"},
		{"number", registry("1"), "not int64"},
		{"table", registry("{ name = \"acme\" }"), "not map[string]interface {}"},
		{"reference to columnar", registry(`"columnar"`), `omit signing_key to use the built-in Columnar key`},
		{"columnar defined", def("columnar", openpgp), "reserved"},
		{"missing type", def("acme", "public_key = "+tomlString(armored)), "type is required"},
		{"unknown type", def("acme", "type = \"minisign\"\npublic_key = \"x\""), `unsupported type "minisign"`},
		{"no key", def("acme", `type = "openpgp"`), "exactly one of public_key and public_key_file"},
		{"both inputs", def("acme", openpgp+"\npublic_key_file = \"acme.asc\""), "exactly one of public_key and public_key_file"},
		{"empty key", def("acme", "type = \"openpgp\"\npublic_key = \"\""), "public_key must not be empty"},
		{"not a key", def("acme", "type = \"openpgp\"\npublic_key = \"nope\""), "armored PGP PUBLIC KEY BLOCK"},
		{"private key", def("acme", "type = \"openpgp\"\npublic_key = "+tomlString(private)), "private"},
		// A definition that no registry names is still checked.
		{"unused invalid key", def("unused", "type = \"openpgp\"\npublic_key = \"nope\""), `signing key "unused"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeProject(t, tt.toml)
			_, err := LoadProjectConfig(path)
			require.ErrorContains(t, err, tt.want)
			assert.ErrorContains(t, err, path)
		})
	}
}

// A signing_key name is looked up only in the file that declares the
// registry: a project cannot name a key of the user config, and the other
// way around.
func TestSigningKeyNamesAreScopedToTheirFile(t *testing.T) {
	armored := publicArmored(t, generateTestKey(t, "acme"))
	global := &GlobalConfig{
		SigningKeys: map[string]SigningKeyDefinition{"acme": {Type: SigningKeyTypeOpenPGP, PublicKey: &armored}},
	}
	project := &ProjectConfig{Registries: []RegistryEntry{{URL: "https://acme.example.com", SigningKey: "acme"}}}
	_, err := NewClient(WithGlobalConfig(global), WithProjectConfig(project))
	assert.ErrorContains(t, err, `signing_key "acme" is not defined`)

	global = &GlobalConfig{Registries: []RegistryEntry{{URL: "https://acme.example.com", SigningKey: "acme"}}}
	project = &ProjectConfig{
		SigningKeys: map[string]SigningKeyDefinition{"acme": {Type: SigningKeyTypeOpenPGP, PublicKey: &armored}},
	}
	_, err = NewClient(WithGlobalConfig(global), WithProjectConfig(project))
	assert.ErrorContains(t, err, `signing_key "acme" is not defined`)
}

// A project can declare a registry that the user config or dbc's defaults
// also declare only with the same signing policy; it cannot change how that
// registry is verified.
func TestProjectCannotChangeTheSigningPolicyOfATrustedRegistry(t *testing.T) {
	armored := publicArmored(t, generateTestKey(t, "acme"))
	other := publicArmored(t, generateTestKey(t, "other"))
	keyFile := filepath.Join(t.TempDir(), "acme.asc")
	require.NoError(t, os.WriteFile(keyFile, []byte(armored), 0o600))
	inline := func(armored string) map[string]SigningKeyDefinition {
		return map[string]SigningKeyDefinition{"acme": {Type: SigningKeyTypeOpenPGP, PublicKey: &armored}}
	}
	fromFile := map[string]SigningKeyDefinition{"acme": {Type: SigningKeyTypeOpenPGP, PublicKeyFile: &keyFile}}
	relFile := "acme.asc"
	userAcme := func(keys map[string]SigningKeyDefinition) *GlobalConfig {
		return &GlobalConfig{
			Registries:  []RegistryEntry{{URL: "https://acme.example.com", SigningKey: "acme"}},
			SigningKeys: keys,
		}
	}
	replace := true

	tests := []struct {
		name    string
		global  *GlobalConfig
		project *ProjectConfig
		wantErr bool
	}{
		{
			name:    "project omits the key of a user registry",
			global:  userAcme(inline(armored)),
			project: &ProjectConfig{Registries: []RegistryEntry{{URL: "https://acme.example.com"}}},
			wantErr: true,
		},
		{
			name:    "project declares a user registry unsigned",
			global:  userAcme(inline(armored)),
			project: &ProjectConfig{Registries: []RegistryEntry{{URL: "https://ACME.example.com/", SigningKey: false}}},
			wantErr: true,
		},
		{
			name:   "project declares another key for a user registry",
			global: userAcme(inline(armored)),
			project: &ProjectConfig{
				Registries:  []RegistryEntry{{URL: "https://acme.example.com", SigningKey: "acme"}},
				SigningKeys: inline(other),
			},
			wantErr: true,
		},
		{
			name:   "project declares the same key for a user registry",
			global: userAcme(inline(armored)),
			project: &ProjectConfig{
				Registries:  []RegistryEntry{{URL: "https://acme.example.com", SigningKey: "mine"}},
				SigningKeys: map[string]SigningKeyDefinition{"mine": inline(armored)["acme"]},
			},
		},
		{
			name:   "key files are not compared",
			global: userAcme(fromFile),
			project: &ProjectConfig{
				Registries:  []RegistryEntry{{URL: "https://acme.example.com", SigningKey: "acme"}},
				SigningKeys: map[string]SigningKeyDefinition{"acme": {Type: SigningKeyTypeOpenPGP, PublicKeyFile: &relFile}},
				Path:        filepath.Join(filepath.Dir(keyFile), "dbc.toml"),
			},
			wantErr: true,
		},
		{
			name:    "project declares the default registry unsigned",
			project: &ProjectConfig{Registries: []RegistryEntry{{URL: "https://dbc-cdn.columnar.tech", SigningKey: false}}},
			wantErr: true,
		},
		{
			name: "project replace_defaults does not lift the check",
			project: &ProjectConfig{
				Registries:      []RegistryEntry{{URL: "https://dbc-cdn.columnar.tech", SigningKey: false}},
				ReplaceDefaults: &replace,
			},
			wantErr: true,
		},
		{
			name:    "the default port is the same registry",
			project: &ProjectConfig{Registries: []RegistryEntry{{URL: "https://dbc-cdn.columnar.tech:443/", SigningKey: false}}},
			wantErr: true,
		},
		{
			name:    "project declares the default registry with the Columnar key",
			project: &ProjectConfig{Registries: []RegistryEntry{{URL: "https://dbc-cdn.columnar.tech"}}},
		},
		{
			name: "project declares the default registry with an inline Columnar key",
			project: &ProjectConfig{
				Registries:  []RegistryEntry{{URL: "https://dbc-cdn.columnar.tech", SigningKey: "columnar-release"}},
				SigningKeys: map[string]SigningKeyDefinition{"columnar-release": {Type: SigningKeyTypeOpenPGP, PublicKey: &armoredPubKey}},
			},
		},
		{
			name: "user config replaces the defaults",
			global: &GlobalConfig{
				Registries:      []RegistryEntry{{URL: "https://mirror.example.com"}},
				ReplaceDefaults: true,
			},
			project: &ProjectConfig{Registries: []RegistryEntry{{URL: "https://dbc-cdn.columnar.tech", SigningKey: false}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "key files are not compared" && !keyFilesSupported {
				t.Skip("the browser build has no key files")
			}
			opts := []Option{WithProjectConfig(tt.project)}
			if tt.global != nil {
				opts = append(opts, WithGlobalConfig(tt.global))
			}
			_, err := NewClient(opts...)
			if tt.wantErr {
				assert.ErrorContains(t, err, "a project cannot change how that registry is verified")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoadGlobalConfigSigningKeys(t *testing.T) {
	dir := t.TempDir()
	armored := publicArmored(t, generateTestKey(t, "acme"))
	content := "[signing_keys.acme]\ntype = \"openpgp\"\npublic_key = " + tomlString(armored) + "\n" +
		"[[registries]]\nurl = \"https://acme.example.com\"\nsigning_key = \"acme\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600))
	cfg, err := LoadGlobalConfig(dir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "config.toml"), cfg.Path)
	assert.Contains(t, cfg.SigningKeys, "acme")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"),
		[]byte("[[registries]]\nurl = \"https://acme.example.com\"\nsigning_key = \"missing\"\n"), 0o600))
	_, err = LoadGlobalConfig(dir)
	assert.ErrorContains(t, err, `signing_key "missing" is not defined`)
}

func TestExplicitRegistrySigningPolicy(t *testing.T) {
	key, err := NewSigningKey([]byte(publicArmored(t, generateTestKey(t, "acme"))))
	require.NoError(t, err)
	u := mustParseURL("https://acme.example.com")

	c, err := NewClient(WithRegistries([]Registry{{BaseURL: u, Signing: SignWith(key)}}))
	require.NoError(t, err)
	got, err := c.Registries()[0].ResolveSigningKey()
	require.NoError(t, err)
	assert.Same(t, key, got)

	for _, policy := range []RegistrySigningPolicy{SignWith(nil), SignWith(&SigningKey{})} {
		_, err = NewClient(WithRegistries([]Registry{{BaseURL: u, Signing: policy}}))
		assert.ErrorContains(t, err, "has no key")
		_, err = Registry{BaseURL: u, Signing: policy}.ResolveSigningKey()
		assert.ErrorContains(t, err, "has no key")
	}
}

func TestSigningPolicyEqual(t *testing.T) {
	a, err := NewSigningKey([]byte(publicArmored(t, generateTestKey(t, "a"))))
	require.NoError(t, err)
	b, err := NewSigningKey([]byte(publicArmored(t, generateTestKey(t, "b"))))
	require.NoError(t, err)
	assert.True(t, RegistrySigningPolicy{}.Equal(RegistrySigningPolicy{}))
	assert.True(t, Unsigned().Equal(Unsigned()))
	assert.True(t, SignWith(a).Equal(SignWith(a)))
	assert.False(t, SignWith(a).Equal(SignWith(b)))
	assert.False(t, SignWith(a).Equal(RegistrySigningPolicy{}))
	assert.False(t, Unsigned().Equal(RegistrySigningPolicy{}))

	// The same primary key with one more packet, as after adding a subkey or
	// a revocation, is a different certificate.
	key := generateTestKey(t, "c")
	c1, err := NewSigningKey(publicBinary(t, key))
	require.NoError(t, err)
	padding := []byte{0xd5, 0x02, 0x00, 0x00}
	c2, err := NewSigningKey(append(publicBinary(t, key), padding...))
	require.NoError(t, err)
	require.Equal(t, c1.Fingerprint(), c2.Fingerprint())
	assert.False(t, SignWith(c1).Equal(SignWith(c2)))
}

func TestProjectConfigValidate(t *testing.T) {
	assert.NoError(t, (&ProjectConfig{}).Validate())
	err := (&ProjectConfig{Registries: []RegistryEntry{{URL: "https://a.example.com", SigningKey: true}}}).Validate()
	assert.ErrorContains(t, err, "signing_key = true")
}

// signing_key = false must survive a decode and encode round trip, which the
// CLI does when it writes dbc.toml back.
func TestRegistryEntrySigningKeyRoundTrip(t *testing.T) {
	in := "[[registries]]\nurl = 'https://a.example.com'\nsigning_key = false\n\n" +
		"[[registries]]\nurl = 'https://b.example.com'\nsigning_key = 'acme'\n\n" +
		"[[registries]]\nurl = 'https://c.example.com'\n"
	var cfg struct {
		Registries []RegistryEntry `toml:"registries"`
	}
	require.NoError(t, toml.Unmarshal([]byte(in), &cfg))
	out, err := toml.Marshal(cfg)
	require.NoError(t, err)
	assert.Contains(t, string(out), "signing_key = false")
	assert.Contains(t, string(out), "signing_key = 'acme'")
	assert.Equal(t, 2, strings.Count(string(out), "signing_key"))
}
