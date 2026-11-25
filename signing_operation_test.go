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
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignerIdentity(t *testing.T) {
	var op SigningOperation
	key := func(name string) *SigningKey {
		t.Helper()
		k, err := NewArmoredSigningKey(publicArmored(t, generateTestKey(t, name)))
		require.NoError(t, err)
		return k
	}
	identity := func(r *Registry) signerIdentity {
		t.Helper()
		id, err := op.signerIdentity(r)
		require.NoError(t, err)
		return id
	}
	columnarCopy, err := NewArmoredSigningKey(armoredPubKey)
	require.NoError(t, err)
	acmeArmored := publicArmored(t, generateTestKey(t, "acme"))
	acme, err := NewArmoredSigningKey(acmeArmored)
	require.NoError(t, err)
	acmeCopy, err := NewArmoredSigningKey(acmeArmored)
	require.NoError(t, err)

	columnar := identity(&Registry{BaseURL: mustParseURL("https://a.test")})
	assert.Equal(t, "the Columnar signing key", columnar.label)
	// A driver without a registry uses the Columnar key, as does an inline
	// copy of it.
	assert.Equal(t, columnar, identity(nil))
	assert.Equal(t, columnar.key, identity(&Registry{BaseURL: mustParseURL("https://b.test"), Signing: SignWith(columnarCopy)}).key)

	// Two keys are the same signer when their certificates are.
	a := identity(&Registry{BaseURL: mustParseURL("https://a.test"), Signing: SignWith(acme)})
	assert.NotEqual(t, columnar.key, a.key)
	assert.Contains(t, a.label, acme.Fingerprint())
	assert.Equal(t, a.key, identity(&Registry{BaseURL: mustParseURL("https://b.test"), Signing: SignWith(acmeCopy)}).key)
	assert.NotEqual(t, a.key, identity(&Registry{BaseURL: mustParseURL("https://a.test"), Signing: SignWith(key("other"))}).key)

	// Every unsigned registry is a signer of its own, also when only its
	// credentials or query differ, but none of them is shown.
	unsigned := func(raw string) signerIdentity {
		return identity(&Registry{BaseURL: mustParseURL(raw), Signing: Unsigned()})
	}
	u := unsigned("https://user:secret@u.test/r?tenant=one")
	assert.Equal(t, "unsigned", u.label)
	assert.Equal(t, u, unsigned("https://user:secret@u.test/r?tenant=one"))
	assert.NotEqual(t, u.key, unsigned("https://user:secret@u.test/r?tenant=two").key)
	assert.NotEqual(t, u.key, unsigned("https://other@u.test/r?tenant=one").key)
	assert.NotEqual(t, u.key, unsigned("https://v.test/r").key)

	_, err = op.signerIdentity(&Registry{Signing: Unsigned()})
	assert.Error(t, err)
}

// TestResolvePackageSigners pins the signer check of ResolvePackage with
// real signing policies.
func TestResolvePackageSigners(t *testing.T) {
	acme, err := NewArmoredSigningKey(publicArmored(t, generateTestKey(t, "acme")))
	require.NoError(t, err)
	driver := func(r *Registry, versions ...string) Driver {
		d := Driver{Path: "shared", Registry: r}
		for _, v := range versions {
			d.PkgInfo = append(d.PkgInfo, pkginfo{Version: semver.MustParse(v), Packages: []pkgEntry{{PlatformTuple: "linux_amd64"}}})
		}
		return d
	}
	latest := func(d *Driver) (PkgInfo, error) { return d.GetLatestPackage("linux_amd64", false) }
	resolve := func(drivers []Driver, indexErr error) (PkgInfo, error) {
		var op SigningOperation
		return op.ResolvePackage(drivers, indexErr, "shared", latest)
	}
	failed := func(r *Registry) error { return &RegistryIndexError{Registry: r, Err: ErrUnauthorized} }

	acmeA := &Registry{BaseURL: mustParseURL("https://a.test"), Signing: SignWith(acme)}
	acmeB := &Registry{BaseURL: mustParseURL("https://b.test"), Signing: SignWith(acme)}
	columnar := &Registry{BaseURL: mustParseURL("https://columnar.test")}
	unsignedA := &Registry{BaseURL: mustParseURL("https://ua.test"), Signing: Unsigned()}
	unsignedB := &Registry{BaseURL: mustParseURL("https://ub.test"), Signing: Unsigned()}

	// Registries with the same certificate fall back to one another, and a
	// failed one does not block the other.
	pkg, err := resolve([]Driver{driver(acmeA), driver(acmeB, "1.0.0")}, nil)
	require.NoError(t, err)
	assert.Same(t, acmeB, pkg.Driver.Registry)
	pkg, err = resolve([]Driver{driver(acmeB, "1.0.0")}, failed(acmeA))
	require.NoError(t, err)
	assert.Same(t, acmeB, pkg.Driver.Registry)

	// Another key conflicts, also with the built-in Columnar key.
	var conflict *SignerConflictError
	_, err = resolve([]Driver{driver(columnar, "1.0.0"), driver(acmeA, "1.0.0")}, nil)
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, "the Columnar signing key", conflict.Signers[0])
	assert.Contains(t, conflict.Signers[1], acme.Fingerprint())
	_, err = resolve([]Driver{driver(columnar, "1.0.0")}, failed(acmeA))
	assert.ErrorIs(t, err, ErrIncompleteIndex)

	// Two unsigned registries are different signers, but a single one can
	// be used alone.
	_, err = resolve([]Driver{driver(unsignedA, "1.0.0"), driver(unsignedB, "1.0.0")}, nil)
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, []string{"unsigned", "unsigned"}, conflict.Signers)
	_, err = resolve([]Driver{driver(unsignedA, "1.0.0")}, failed(unsignedB))
	assert.ErrorIs(t, err, ErrIncompleteIndex)
	pkg, err = resolve([]Driver{driver(unsignedA, "1.0.0")}, nil)
	require.NoError(t, err)
	assert.Same(t, unsignedA, pkg.Driver.Registry)
}

func TestSigningOperationResolveKey(t *testing.T) {
	columnar, err := ColumnarSigningKey()
	require.NoError(t, err)
	var op SigningOperation

	key, err := op.ResolveKey(SigningPolicy{})
	require.NoError(t, err)
	assert.Same(t, columnar, key, "the zero value is the Columnar key")

	key, err = op.ResolveKey(Unsigned())
	require.NoError(t, err)
	assert.Nil(t, key, "only Unsigned resolves to no key")

	_, err = op.ResolveKey(SignWith(nil))
	assert.Error(t, err, "a policy without a key is an error, not unsigned")
}
