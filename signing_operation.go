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
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

// RegistrySigningOperation resolves the signing keys of registries for one
// operation, such as one install, lock or sync. Within it, the signer check
// of ResolvePackage, the verification of packages and the key that a lock
// file pins all use the same certificate of a registry, even when the key is
// read from a public_key_file that changes meanwhile. A key file is read
// only when a key is needed, and the key read from it is reused for the rest
// of the operation; a failed read is not remembered, but every caller stops
// on it. The next operation reads the file again.
//
// It covers the signing policies of registries only. The zero value is ready
// to use; it must not be copied after first use.
type RegistrySigningOperation struct {
	mu    sync.Mutex
	files map[*signingKeyFile]*SigningKey
}

// ResolveSigningKey returns the key that the driver packages of registry r
// must be verified with, like Registry.ResolveSigningKey, but reads a key
// file only once in this operation. A nil registry, as for a driver without
// one, uses the Columnar key. It returns (nil, nil) only for an Unsigned
// registry.
func (o *RegistrySigningOperation) ResolveSigningKey(r *Registry) (*SigningKey, error) {
	if r == nil {
		return ColumnarSigningKey()
	}
	if r.Signing.kind != signingFromFile || r.Signing.file == nil {
		return r.ResolveSigningKey()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if key, ok := o.files[r.Signing.file]; ok {
		return key, nil
	}
	key, err := r.ResolveSigningKey()
	if err != nil {
		return nil, err
	}
	if o.files == nil {
		o.files = make(map[*signingKeyFile]*SigningKey)
	}
	o.files[r.Signing.file] = key
	return key, nil
}

// ResolvePackage chooses the package of a driver that is not pinned yet.
// drivers is the merged index of the registries in priority order, and
// indexErr the error Client.Search returned with it.
//
// All registries that list the driver, whatever their versions and
// platforms, must verify their packages with the same signer; otherwise it
// returns a SignerConflictError. Two keys are the same signer when their
// certificates are identical, and every unsigned registry is a signer of its
// own. A registry whose index is missing (a RegistryIndexError in indexErr)
// might list the driver too, so its signer, which is known from its
// configuration, must be the same as well: the same as that of the
// registries that list the driver, or, when none does, the same as that of
// the other failed registries. Otherwise, or when a failure cannot be
// attributed to a registry or its signer cannot be determined, it returns
// ErrIncompleteIndex. A failed registry with the same signer is skipped, as
// the registries that list the driver could replace it anyway. Signers are
// only compared, and keys only read for it, when several registries list the
// driver or an index is missing.
//
// Among the registries that list the driver, matching entries are tried in
// order, advancing only on ErrPackageMiss. The returned package identifies
// the selected driver on success or a terminal error. If all matching
// entries miss, it identifies the first matching driver and returns that
// entry's error. If no entry matches, it returns a zero package and a
// driver-not-found error.
//
// To resolve from one registry only, pass that registry's index alone.
func (o *RegistrySigningOperation) ResolvePackage(drivers []Driver, indexErr error, name string, selectPackage func(*Driver) (PkgInfo, error)) (PkgInfo, error) {
	return resolvePackage(drivers, indexErr, name, selectPackage, o.signerIdentity)
}

// signerIdentity tells which signer verifies the packages of a registry.
// key is compared; label describes the signer in messages and never holds
// the registry URL, which may contain credentials.
type signerIdentity struct {
	key   string
	label string
}

// signerIdentity returns the signer of registry r: for a key, the digest of
// the whole certificate, since subkeys and revocations change which
// signatures it accepts; for an unsigned registry, the registry itself, so
// that two unsigned registries never count as the same signer.
func (o *RegistrySigningOperation) signerIdentity(r *Registry) (signerIdentity, error) {
	if r != nil && r.Signing.IsUnsigned() {
		if r.BaseURL == nil {
			return signerIdentity{}, errors.New("unsigned registry has no URL")
		}
		// registryURLKey keeps the user information and query, which can
		// select different registries on the same host and path.
		return signerIdentity{key: "unsigned " + registryURLKey(r.BaseURL), label: "unsigned"}, nil
	}
	key, err := o.ResolveSigningKey(r)
	if err != nil {
		return signerIdentity{}, err
	}
	if key == nil {
		return signerIdentity{}, errors.New("signing policy selected no key")
	}
	digest := hex.EncodeToString(key.digest[:])
	id := signerIdentity{key: "openpgp " + digest}
	if columnar, err := ColumnarSigningKey(); err == nil && columnar.digest == key.digest {
		id.label = "the Columnar signing key"
	} else {
		// r is not nil: a nil registry uses the Columnar key.
		id.label = fmt.Sprintf("%s (OpenPGP key %s, certificate sha256:%s)", r.Signing, key.Fingerprint(), digest[:16])
	}
	return id, nil
}
