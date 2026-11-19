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
)

// SigningPolicy says how the driver packages of a registry are verified. The
// zero value verifies them with the built-in Columnar key. Use SignWith for
// another key and Unsigned for a registry whose packages are not signed.
type SigningPolicy struct {
	kind signingPolicyKind
	key  *SigningKey
	// name is the [signing_keys] definition the policy came from, if any.
	name string
}

// SignWith returns the policy that verifies packages with key.
func SignWith(key *SigningKey) SigningPolicy {
	return SigningPolicy{kind: signingWithKey, key: key}
}

// Unsigned returns the policy for a registry whose packages are not verified
// with any key. The archive hash that a registry publishes is still checked.
func Unsigned() SigningPolicy {
	return SigningPolicy{kind: signingUnsigned}
}

// IsUnsigned reports whether the policy is Unsigned.
func (p SigningPolicy) IsUnsigned() bool { return p.kind == signingUnsigned }

// validate rejects a policy that cannot produce a key, such as SignWith(nil).
func (p SigningPolicy) validate() error {
	if p.kind == signingWithKey && (p.key == nil || p.key.verifier == nil) {
		return errors.New("signing policy has no key; create the key with NewSigningKey")
	}
	return nil
}

// Equal reports whether p and q select the same key in the same way. Two
// keys are the same when their primary key fingerprints are; the name of the
// definition a key came from does not matter.
func (p SigningPolicy) Equal(q SigningPolicy) bool {
	if p.kind != q.kind {
		return false
	}
	if p.kind == signingWithKey {
		if p.key == nil || q.key == nil {
			return p.key == q.key
		}
		return p.key.Fingerprint() == q.key.Fingerprint()
	}
	return true
}

// String describes the policy for messages.
func (p SigningPolicy) String() string {
	switch p.kind {
	case signingUnsigned:
		return "unsigned"
	case signingWithKey:
		if p.name != "" {
			return fmt.Sprintf("signing key %q", p.name)
		}
		return "a custom signing key"
	default:
		return "the Columnar signing key"
	}
}

// ResolveSigningKey returns the key that the driver packages of the registry
// must be verified with. It returns (nil, nil) only for an Unsigned registry;
// any failure to produce a key is an error, so a caller never falls back to
// another key or to no verification.
func (r Registry) ResolveSigningKey() (*SigningKey, error) {
	p := r.Signing
	if err := p.validate(); err != nil {
		return nil, err
	}
	switch p.kind {
	case signingUnsigned:
		return nil, nil
	case signingWithKey:
		return p.key, nil
	default:
		return ColumnarSigningKey()
	}
}
