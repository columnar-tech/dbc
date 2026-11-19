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
)

type signingPolicyKind uint8

const (
	signingColumnar signingPolicyKind = iota
	signingWithKey
	signingUnsigned
	signingFromFile
)

// RegistrySigningPolicy says how the driver packages of a registry are
// verified with a detached OpenPGP signature. The zero value verifies them
// with the built-in Columnar key. Use SignWith for another key and Unsigned
// for a registry whose packages are not signed.
type RegistrySigningPolicy struct {
	kind signingPolicyKind
	key  *SigningKey
	file *signingKeyFile
	// name is the [signing_keys] definition the policy came from, if any.
	name string
}

// SignWith returns the policy that verifies packages with key.
func SignWith(key *SigningKey) RegistrySigningPolicy {
	return RegistrySigningPolicy{kind: signingWithKey, key: key}
}

// Unsigned returns the policy for a registry whose packages are not verified
// with any key. The archive hash that a registry publishes is still checked.
func Unsigned() RegistrySigningPolicy {
	return RegistrySigningPolicy{kind: signingUnsigned}
}

// IsUnsigned reports whether the policy is Unsigned.
func (p RegistrySigningPolicy) IsUnsigned() bool { return p.kind == signingUnsigned }

// validate rejects a policy that cannot produce a key, such as SignWith(nil).
func (p RegistrySigningPolicy) validate() error {
	if p.kind == signingWithKey && (p.key == nil || p.key.verifier == nil) {
		return errors.New("signing policy has no key; create the key with NewSigningKey")
	}
	return nil
}

// Equal reports whether p and q select the same signing configuration. Two
// keys given directly are the same only when their certificates are
// identical, so a new subkey or revocation of the same primary key makes
// them differ. Two keys read from files are the same when the paths are; the
// files are not read. The name of the definition a key came from does not
// matter.
func (p RegistrySigningPolicy) Equal(q RegistrySigningPolicy) bool {
	if p.kind != q.kind {
		return false
	}
	switch p.kind {
	case signingWithKey:
		if p.key == nil || q.key == nil {
			return p.key == q.key
		}
		return p.key.digest == q.key.digest
	case signingFromFile:
		// The content of the files is not read here; a file that a
		// configuration names is the same setting even when it changes.
		return p.file.path == q.file.path
	}
	return true
}

// String describes the policy for messages.
func (p RegistrySigningPolicy) String() string {
	switch p.kind {
	case signingUnsigned:
		return "unsigned"
	case signingWithKey, signingFromFile:
		if p.name != "" {
			return fmt.Sprintf("signing key %q", p.name)
		}
		return "a custom signing key"
	case signingColumnar:
		return "the Columnar signing key"
	default:
		return "an unknown signing policy"
	}
}

// ResolveSigningKey returns the key that the driver packages of the registry
// must be verified with. It returns (nil, nil) only for an Unsigned registry;
// any failure to produce a key, such as a public_key_file that cannot be
// read, is an error, so a caller never falls back to another key or to no
// verification. A public_key_file is read on every call; a caller that must
// verify several packages with the same certificate keeps the returned key.
func (r Registry) ResolveSigningKey() (*SigningKey, error) {
	p := r.Signing
	if err := p.validate(); err != nil {
		return nil, err
	}
	switch p.kind {
	case signingColumnar:
		return ColumnarSigningKey()
	case signingUnsigned:
		return nil, nil
	case signingWithKey:
		return p.key, nil
	case signingFromFile:
		key, err := p.file.load()
		if err != nil && p.name != "" {
			err = fmt.Errorf("signing key %q: %w", p.name, err)
		}
		return key, err
	default:
		return nil, fmt.Errorf("unknown signing policy %d", p.kind)
	}
}
