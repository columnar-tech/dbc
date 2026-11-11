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
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const columnarFingerprint = "ab7766e9b341299ebc7f45dd82abd0a33a3c9e48"

func generateTestKey(t *testing.T, name string) *crypto.Key {
	t.Helper()
	key, err := crypto.PGP().KeyGeneration().AddUserId(name, name+"@example.invalid").New().GenerateKey()
	require.NoError(t, err)
	return key
}

func publicArmored(t *testing.T, key *crypto.Key) string {
	t.Helper()
	pub, err := key.ToPublic()
	require.NoError(t, err)
	armored, err := pub.Armor()
	require.NoError(t, err)
	return armored
}

func detachedSign(t *testing.T, key *crypto.Key, data []byte) []byte {
	t.Helper()
	signer, err := crypto.PGP().Sign().SigningKey(key).Detached().New()
	require.NoError(t, err)
	sig, err := signer.Sign(data, crypto.Armor)
	require.NoError(t, err)
	return sig
}

func TestColumnarSigningKeyFingerprint(t *testing.T) {
	key, err := ColumnarSigningKey()
	require.NoError(t, err)
	assert.Equal(t, columnarFingerprint, key.Fingerprint())
}

func TestSigningKeyVerifiesOnlyItsOwnSignatures(t *testing.T) {
	data := []byte("driver library bytes")
	a, b := generateTestKey(t, "a"), generateTestKey(t, "b")
	sig := detachedSign(t, a, data)

	keyA, err := newSigningKey(publicArmored(t, a))
	require.NoError(t, err)
	keyB, err := newSigningKey(publicArmored(t, b))
	require.NoError(t, err)

	assert.Equal(t, a.GetFingerprint(), keyA.Fingerprint())
	assert.NotEqual(t, keyA.Fingerprint(), keyB.Fingerprint())
	assert.NoError(t, keyA.Verify(bytes.NewReader(data), bytes.NewReader(sig)))
	assert.Error(t, keyB.Verify(bytes.NewReader(data), bytes.NewReader(sig)))
	assert.Error(t, keyA.Verify(bytes.NewReader([]byte("other bytes")), bytes.NewReader(sig)))
}

// The fixture signature was made by a signing subkey; the fingerprint of the
// key is still the one of the primary key.
func TestSigningKeySubkeySignature(t *testing.T) {
	dir := filepath.Join("testdata", "signing")
	armored, err := os.ReadFile(filepath.Join(dir, "subkey-signer.pub.asc"))
	require.NoError(t, err)
	payload, err := os.ReadFile(filepath.Join(dir, "payload.txt"))
	require.NoError(t, err)
	sig, err := os.ReadFile(filepath.Join(dir, "payload.txt.sig"))
	require.NoError(t, err)

	key, err := newSigningKey(string(armored))
	require.NoError(t, err)
	assert.Equal(t, "9b96d38c4b87a17f1f4b1fa7e0ff4c99f17a9538", key.Fingerprint())
	assert.NoError(t, key.Verify(bytes.NewReader(payload), bytes.NewReader(sig)))
}

func TestNewSigningKeyRejectsPrivateKey(t *testing.T) {
	private, err := generateTestKey(t, "private").Armor()
	require.NoError(t, err)
	_, err = newSigningKey(private)
	assert.ErrorContains(t, err, "private")

	_, err = newSigningKey("not a key")
	assert.Error(t, err)
}
