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

//go:build !js || dbcnode

package dbc

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keyFileProject writes a dbc.toml in dir whose registry names a key read from
// file, as written in public_key_file.
func keyFileProject(t *testing.T, dir, file string) string {
	t.Helper()
	path := filepath.Join(dir, "dbc.toml")
	content := "[signing_keys.acme]\ntype = \"openpgp\"\npublic_key_file = " + strconv.Quote(file) + "\n" +
		"[[registries]]\nurl = \"https://acme.example.com\"\nsigning_key = \"acme\"\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func resolveFirstKey(t *testing.T, cfg *ProjectConfig) (*SigningKey, error) {
	t.Helper()
	c, err := NewClient(WithProjectConfig(cfg))
	require.NoError(t, err)
	return c.Registries()[0].ResolveSigningKey()
}

func TestPublicKeyFileRelativeToTheConfig(t *testing.T) {
	key := generateTestKey(t, "acme")
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "keys"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keys", "acme.asc"), []byte(publicArmored(t, key)), 0o600))
	path := keyFileProject(t, dir, filepath.Join("keys", "acme.asc"))

	// The working directory does not matter.
	t.Chdir(t.TempDir())
	cfg, err := LoadProjectConfig(path)
	require.NoError(t, err)
	got, err := resolveFirstKey(t, cfg)
	require.NoError(t, err)
	assert.Equal(t, key.GetFingerprint(), got.Fingerprint())
}

func TestPublicKeyFileBinary(t *testing.T) {
	key := generateTestKey(t, "acme")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "acme.gpg"), publicBinary(t, key), 0o600))
	cfg, err := LoadProjectConfig(keyFileProject(t, dir, "acme.gpg"))
	require.NoError(t, err)
	got, err := resolveFirstKey(t, cfg)
	require.NoError(t, err)
	assert.Equal(t, key.GetFingerprint(), got.Fingerprint())
}

// The user config can name a key file anywhere, with an absolute path.
func TestPublicKeyFileAbsoluteInTheUserConfig(t *testing.T) {
	key := generateTestKey(t, "acme")
	keyPath := filepath.Join(t.TempDir(), "acme.asc")
	require.NoError(t, os.WriteFile(keyPath, []byte(publicArmored(t, key)), 0o600))
	dir := t.TempDir()
	content := "[signing_keys.acme]\ntype = \"openpgp\"\npublic_key_file = " + strconv.Quote(keyPath) + "\n" +
		"[[registries]]\nurl = \"https://acme.example.com\"\nsigning_key = \"acme\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600))
	global, err := LoadGlobalConfig(dir)
	require.NoError(t, err)
	c, err := NewClient(WithGlobalConfig(global))
	require.NoError(t, err)
	got, err := c.Registries()[0].ResolveSigningKey()
	require.NoError(t, err)
	assert.Equal(t, key.GetFingerprint(), got.Fingerprint())
}

// A project's key file must be inside the directory of dbc.toml, so that a
// cloned repository cannot make dbc read a file elsewhere, such as a network
// share. The path is rejected before any file is accessed.
func TestProjectPublicKeyFileMustBeInsideTheProject(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "acme.asc")
	require.NoError(t, os.WriteFile(outside, []byte(publicArmored(t, generateTestKey(t, "acme"))), 0o600))
	names := []string{outside, filepath.Join("..", "acme.asc"), "keys/../../acme.asc"}
	if runtime.GOOS == "windows" {
		names = append(names, `\\host\share\acme.asc`, `//host/share/acme.asc`, `\\?\C:\acme.asc`, `C:acme.asc`, `\acme.asc`, `NUL`)
	}
	for _, name := range names {
		path := keyFileProject(t, t.TempDir(), name)
		_, err := LoadProjectConfig(path)
		assert.ErrorContains(t, err, "must be a path inside the directory of dbc.toml", name)
	}
}

// A symbolic link in the project cannot lead to a key file outside of it.
func TestProjectPublicKeyFileSymlinkCannotLeaveTheProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links needs privileges on Windows")
	}
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "acme.asc"), []byte(publicArmored(t, generateTestKey(t, "acme"))), 0o600))
	dir := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(outside, "acme.asc"), filepath.Join(dir, "acme.asc")))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "keys")))

	for _, name := range []string{"acme.asc", filepath.Join("keys", "acme.asc")} {
		cfg, err := LoadProjectConfig(keyFileProject(t, dir, name))
		require.NoError(t, err)
		got, err := resolveFirstKey(t, cfg)
		assert.Nil(t, got, name)
		assert.ErrorContains(t, err, "escapes from parent", name)
	}

	// A link that stays inside the project is followed.
	key := generateTestKey(t, "inside")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "real"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "real", "inside.asc"), []byte(publicArmored(t, key)), 0o600))
	require.NoError(t, os.Symlink(filepath.Join("real", "inside.asc"), filepath.Join(dir, "inside.asc")))
	cfg, err := LoadProjectConfig(keyFileProject(t, dir, "inside.asc"))
	require.NoError(t, err)
	got, err := resolveFirstKey(t, cfg)
	require.NoError(t, err)
	assert.Equal(t, key.GetFingerprint(), got.Fingerprint())
}

// A key file is read only when a registry needs its key, so a missing file
// does not stop loading the configuration or building a client; using the
// key is an error, not a fallback to another key.
func TestPublicKeyFileIsReadWhenUsed(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadProjectConfig(keyFileProject(t, dir, "missing.asc"))
	require.NoError(t, err)
	got, err := resolveFirstKey(t, cfg)
	assert.Nil(t, got)
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.ErrorContains(t, err, `signing key "acme"`)
	assert.ErrorContains(t, err, filepath.Join(dir, "missing.asc"))
}

// The file is read on every use, so a long-lived client sees a key file that
// is added, fixed or updated later.
func TestPublicKeyFileIsReadOnEveryUse(t *testing.T) {
	key := generateTestKey(t, "acme")
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "acme.asc")
	require.NoError(t, os.WriteFile(keyPath, []byte(publicArmored(t, key)), 0o600))
	cfg, err := LoadProjectConfig(keyFileProject(t, dir, "acme.asc"))
	require.NoError(t, err)
	c, err := NewClient(WithProjectConfig(cfg))
	require.NoError(t, err)

	first, err := c.Registries()[0].ResolveSigningKey()
	require.NoError(t, err)
	assert.Equal(t, key.GetFingerprint(), first.Fingerprint())

	require.NoError(t, os.Remove(keyPath))
	_, err = c.Registries()[0].ResolveSigningKey()
	assert.ErrorIs(t, err, os.ErrNotExist)

	other := generateTestKey(t, "other")
	require.NoError(t, os.WriteFile(keyPath, []byte(publicArmored(t, other)), 0o600))
	second, err := c.Registries()[0].ResolveSigningKey()
	require.NoError(t, err)
	assert.Equal(t, other.GetFingerprint(), second.Fingerprint())
}

func TestPublicKeyFileErrors(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "keys"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "large.asc"), make([]byte, maxSigningKeySize+1), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "invalid.asc"), []byte("not a key"), 0o600))

	for _, tt := range []struct {
		name, file, want string
	}{
		{"directory", "keys", "not a regular file"},
		{"too large", "large.asc", "larger than"},
		{"not a key", "invalid.asc", "armored PGP PUBLIC KEY BLOCK"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadProjectConfig(keyFileProject(t, dir, tt.file))
			require.NoError(t, err)
			_, err = resolveFirstKey(t, cfg)
			assert.ErrorContains(t, err, tt.want)
			assert.ErrorContains(t, err, filepath.Join(dir, tt.file))
		})
	}

	path := filepath.Join(t.TempDir(), "dbc.toml")
	require.NoError(t, os.WriteFile(path, []byte("[signing_keys.acme]\ntype = \"openpgp\"\npublic_key_file = \"\"\n"), 0o600))
	_, err := LoadProjectConfig(path)
	assert.ErrorContains(t, err, "public_key_file must not be empty")
}

// A relative public_key_file needs the file that defines it; a configuration
// built in memory has none.
func TestRelativePublicKeyFileNeedsAConfigFile(t *testing.T) {
	file := "acme.asc"
	cfg := &ProjectConfig{
		Registries:  []RegistryEntry{{URL: "https://acme.example.com", SigningKey: "acme"}},
		SigningKeys: map[string]SigningKeyDefinition{"acme": {Type: SigningKeyTypeOpenPGP, PublicKeyFile: &file}},
	}
	_, err := NewClient(WithProjectConfig(cfg))
	assert.ErrorContains(t, err, "is relative")
}

// The base of a relative public_key_file is the directory of the config file
// as named, not the target of a symbolic link to it.
func TestPublicKeyFileBaseIsNotResolvedThroughSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links needs privileges on Windows")
	}
	key := generateTestKey(t, "acme")
	real, link := t.TempDir(), t.TempDir()
	path := keyFileProject(t, real, "acme.asc")
	require.NoError(t, os.Symlink(path, filepath.Join(link, "dbc.toml")))
	require.NoError(t, os.WriteFile(filepath.Join(link, "acme.asc"), []byte(publicArmored(t, key)), 0o600))

	cfg, err := LoadProjectConfig(filepath.Join(link, "dbc.toml"))
	require.NoError(t, err)
	got, err := resolveFirstKey(t, cfg)
	require.NoError(t, err)
	assert.Equal(t, key.GetFingerprint(), got.Fingerprint())
}

func TestPublicKeyFilePolicyEqual(t *testing.T) {
	dir := t.TempDir()
	a, err := newSigningKeyFile(filepath.Join(dir, "a.asc"), "", false)
	require.NoError(t, err)
	a2, err := newSigningKeyFile("a.asc", filepath.Join(dir, "dbc.toml"), false)
	require.NoError(t, err)
	b, err := newSigningKeyFile(filepath.Join(dir, "b.asc"), "", false)
	require.NoError(t, err)
	policy := func(f *signingKeyFile) RegistrySigningPolicy {
		return RegistrySigningPolicy{kind: signingFromFile, file: f}
	}
	assert.True(t, policy(a).Equal(policy(a2)))
	assert.False(t, policy(a).Equal(policy(b)))
}

// On Windows, a path rooted without a drive, or with a drive but not rooted,
// depends on the current drive or directory, so it is rejected rather than
// joined to the directory of the configuration file.
func TestPublicKeyFileRejectsDriveDependentPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows has drive-relative paths")
	}
	definedIn := filepath.Join(t.TempDir(), "dbc.toml")
	for _, name := range []string{`\keys\a.asc`, `/keys/a.asc`, `C:keys\a.asc`} {
		_, err := newSigningKeyFile(name, definedIn, false)
		assert.ErrorContains(t, err, "absolute path with a drive", name)
	}
}

// TestSigningOperationReadsAKeyFileOnce pins that one operation compares
// signers and verifies with the certificate it read first, even when the
// file changes meanwhile, and that the next operation reads the file again.
func TestSigningOperationReadsAKeyFileOnce(t *testing.T) {
	key := generateTestKey(t, "acme")
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "acme.asc")
	require.NoError(t, os.WriteFile(keyPath, []byte(publicArmored(t, key)), 0o600))
	cfg, err := LoadProjectConfig(keyFileProject(t, dir, "acme.asc"))
	require.NoError(t, err)
	c, err := NewClient(WithProjectConfig(cfg))
	require.NoError(t, err)
	fromFile := c.Registries()[0]
	inline, err := NewArmoredSigningKey(publicArmored(t, key))
	require.NoError(t, err)
	other := &Registry{BaseURL: mustParseURL("https://other.test"), Signing: SignWith(inline)}

	drivers := []Driver{{Path: "shared", Registry: &fromFile}, {Path: "shared", Registry: other,
		PkgInfo: []pkginfo{{Version: semver.MustParse("1.0.0"), Packages: []pkgEntry{{PlatformTuple: "linux_amd64"}}}}}}
	latest := func(d *Driver) (PkgInfo, error) { return d.GetLatestPackage("linux_amd64", false) }

	var op RegistrySigningOperation
	_, err = op.ResolvePackage(drivers, nil, "shared", latest)
	require.NoError(t, err, "the file holds the same certificate as the inline key")

	replaced := generateTestKey(t, "replaced")
	require.NoError(t, os.WriteFile(keyPath, []byte(publicArmored(t, replaced)), 0o600))

	// A copy of the registry, as a single-registry fetch makes, still gets
	// the certificate that the signer check compared.
	registryCopy := fromFile
	got, err := op.ResolveSigningKey(&registryCopy)
	require.NoError(t, err)
	assert.Equal(t, key.GetFingerprint(), got.Fingerprint())
	_, err = op.ResolvePackage(drivers, nil, "shared", latest)
	require.NoError(t, err)

	// The next operation reads the file again and sees the new key.
	var next RegistrySigningOperation
	got, err = next.ResolveSigningKey(&fromFile)
	require.NoError(t, err)
	assert.Equal(t, replaced.GetFingerprint(), got.Fingerprint())
	_, err = next.ResolvePackage(drivers, nil, "shared", latest)
	var conflict *SignerConflictError
	require.ErrorAs(t, err, &conflict)
}
