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
	"slices"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
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

	keyA, err := NewArmoredSigningKey(publicArmored(t, a))
	require.NoError(t, err)
	keyB, err := NewArmoredSigningKey(publicArmored(t, b))
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

	key, err := NewArmoredSigningKey(string(armored))
	require.NoError(t, err)
	assert.Equal(t, "9b96d38c4b87a17f1f4b1fa7e0ff4c99f17a9538", key.Fingerprint())
	assert.NoError(t, key.Verify(bytes.NewReader(payload), bytes.NewReader(sig)))
}

func TestNewSigningKeyRejectsPrivateKey(t *testing.T) {
	private, err := generateTestKey(t, "private").Armor()
	require.NoError(t, err)
	_, err = NewArmoredSigningKey(private)
	assert.ErrorContains(t, err, "private")

	_, err = NewArmoredSigningKey("not a key")
	assert.Error(t, err)
}

func publicBinary(t *testing.T, key *crypto.Key) []byte {
	t.Helper()
	pub, err := key.GetPublicKey()
	require.NoError(t, err)
	return pub
}

// splitPackets returns the raw bytes of each OpenPGP packet in data.
func splitPackets(t *testing.T, data []byte) [][]byte {
	t.Helper()
	r := bytes.NewReader(data)
	var out [][]byte
	for r.Len() > 0 {
		start := len(data) - r.Len()
		_, err := packet.Read(r)
		require.NoError(t, err)
		out = append(out, data[start:len(data)-r.Len()])
	}
	return out
}

func armorPublic(t *testing.T, payload []byte) string {
	t.Helper()
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, "PGP PUBLIC KEY BLOCK", nil)
	require.NoError(t, err)
	_, err = w.Write(payload)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.String()
}

func TestNewSigningKeyAcceptsArmoredAndBinary(t *testing.T) {
	key := generateTestKey(t, "both")
	fromArmor, err := NewSigningKey([]byte("\n  " + publicArmored(t, key) + "\n\n"))
	require.NoError(t, err)
	fromBinary, err := NewSigningKey(publicBinary(t, key))
	require.NoError(t, err)
	assert.Equal(t, key.GetFingerprint(), fromArmor.Fingerprint())
	assert.Equal(t, key.GetFingerprint(), fromBinary.Fingerprint())

	data := []byte("driver library bytes")
	assert.NoError(t, fromBinary.Verify(bytes.NewReader(data), bytes.NewReader(detachedSign(t, key, data))))
}

func TestNewSigningKeyRejectsAmbiguousInput(t *testing.T) {
	a, b := generateTestKey(t, "a"), generateTestKey(t, "b")
	binA, binB := publicBinary(t, a), publicBinary(t, b)
	armoredA := publicArmored(t, a)

	privateBinary, err := a.Serialize()
	require.NoError(t, err)
	// A certificate whose subkey packet carries secret key material: the
	// public packets of the primary key followed by the private subkey and
	// its binding signature.
	pubPackets, privPackets := splitPackets(t, binA), splitPackets(t, privateBinary)
	require.Len(t, pubPackets, len(privPackets))
	var secretSubkey []byte
	for _, p := range pubPackets[:len(pubPackets)-2] {
		secretSubkey = append(secretSubkey, p...)
	}
	for _, p := range privPackets[len(privPackets)-2:] {
		secretSubkey = append(secretSubkey, p...)
	}
	marker := []byte{0xca, 0x03, 'P', 'G', 'P'}

	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{"two certificates", append(slices.Clone(binA), binB...), "more than one"},
		{"two armored blocks", []byte(armoredA + publicArmored(t, b)), "exactly one armored block"},
		{"text before the armor", []byte("comment\n" + armoredA), "armored PGP PUBLIC KEY BLOCK"},
		{"text after the armor", []byte(armoredA + "trailing\n"), "after the end"},
		{"private key", privateBinary, "private"},
		{"private armored key", []byte(mustArmor(t, a)), "private"},
		{"private subkey", secretSubkey, "private"},
		{"unknown packet", append(slices.Clone(binA), marker...), "unexpected OpenPGP packet"},
		{"armored unknown packet", []byte(armorPublic(t, append(slices.Clone(binA), marker...))), "unexpected OpenPGP packet"},
		{"truncated", binA[:len(binA)-1], "invalid OpenPGP public key"},
		{"trailing byte", append(slices.Clone(binA), 0x00), "invalid OpenPGP public key"},
		{"subkey only", slices.Concat(pubPackets[len(pubPackets)-2:]...), "start with a primary public key"},
		{"data after the checksum", []byte(strings.Replace(armorPublic(t, binA), "-----END", "=AAAA\nextra\n-----END", 1)), "after the checksum"},
		{"truncated padding packet", append(slices.Clone(binA), 0xd5, 0x05, 0x00, 0x00), "unexpected EOF"},
		{"partial body length", append(slices.Clone(binA), 0xd5, 0xe1, 0x00), "partial body length"},
		{"length beyond int32", append(slices.Clone(binA), 0xd5, 0xff, 0xff, 0xff, 0xff, 0xff), "unexpected EOF"},
		{"legacy length beyond int32", append(slices.Clone(binA), 0x8a, 0xff, 0xff, 0xff, 0xff), "unexpected EOF"},
		{"empty", nil, "does not contain a public key"},
		{"too large", bytes.Repeat([]byte{' '}, maxSigningKeySize+1), "larger than"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewSigningKey(tt.input)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func mustArmor(t *testing.T, key *crypto.Key) string {
	t.Helper()
	armored, err := key.Armor()
	require.NoError(t, err)
	return armored
}

func TestArmoredSigningKeyRejectsBinary(t *testing.T) {
	_, err := NewArmoredSigningKey(string(publicBinary(t, generateTestKey(t, "binary"))))
	assert.ErrorContains(t, err, "ASCII-armored")
}
