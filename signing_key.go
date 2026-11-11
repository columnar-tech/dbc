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
	"io"
	"io/fs"
	"os"
	"sync"

	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"github.com/columnar-tech/dbc/config"
)

// SigningKey is an OpenPGP public key that driver packages are verified with.
// Its fingerprint and its verifier are derived from the same parsed key.
type SigningKey struct {
	fingerprint string
	verifier    crypto.PGPVerify
}

// newSigningKey parses one armored OpenPGP public key. Private key material is
// rejected.
func newSigningKey(armored string) (*SigningKey, error) {
	key, err := crypto.NewKeyFromArmored(armored)
	if err != nil {
		return nil, fmt.Errorf("could not parse signing key: %w", err)
	}
	if key.IsPrivate() {
		return nil, errors.New("signing key must be a public key, not a private key")
	}
	verifier, err := crypto.PGP().Verify().VerificationKey(key).New()
	if err != nil {
		return nil, fmt.Errorf("could not use signing key: %w", err)
	}
	return &SigningKey{fingerprint: key.GetFingerprint(), verifier: verifier}, nil
}

var columnarSigningKey = sync.OnceValues(func() (*SigningKey, error) {
	return newSigningKey(armoredPubKey)
})

// ColumnarSigningKey returns the Columnar public key that is embedded in the
// binary.
func ColumnarSigningKey() (*SigningKey, error) {
	return columnarSigningKey()
}

// Fingerprint returns the lowercase hex fingerprint of the primary key
// (certificate). It is not the fingerprint of a signing subkey, so a renewed
// subkey of the same primary key keeps the fingerprint.
func (k *SigningKey) Fingerprint() string { return k.fingerprint }

// Verify returns nil if sig is a valid detached signature of lib made by this
// key or one of its subkeys.
func (k *SigningKey) Verify(lib, sig io.Reader) error {
	reader, err := k.verifier.VerifyingReader(lib, sig, crypto.Auto)
	if err != nil {
		return err
	}

	result, err := reader.DiscardAllAndVerifySignature()
	if err != nil {
		return err
	}

	return result.SignatureError()
}

// VerifyPackage checks the driver library of a package extracted to
// stagingDir against its detached signature (m.Files.Signature, by default
// "<driver>.sig") using this key. It has the shape of a config.PackageVerifier.
// Both files are opened through an os.Root, so a manifest path that leaves
// stagingDir (via "..", an absolute path, or a symlink) is an error rather
// than verifying a file elsewhere.
func (k *SigningKey) VerifyPackage(stagingDir string, m config.Manifest) error {
	if m.Files.Driver == "" {
		return fmt.Errorf("package manifest does not specify Files.driver")
	}

	root, err := os.OpenRoot(stagingDir)
	if err != nil {
		return fmt.Errorf("could not open staging directory: %w", err)
	}
	defer root.Close()

	lib, err := root.Open(m.Files.Driver)
	if err != nil {
		return fmt.Errorf("could not open driver file: %w", err)
	}
	defer lib.Close()

	sigFile := m.Files.Signature
	if sigFile == "" {
		sigFile = m.Files.Driver + ".sig"
	}

	sig, err := root.Open(sigFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("signature file '%s' for driver is missing", sigFile)
		}
		return fmt.Errorf("failed to open signature file: %w", err)
	}
	defer sig.Close()

	if err := k.Verify(lib, sig); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}
