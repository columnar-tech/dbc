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
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sync"

	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"github.com/columnar-tech/dbc/config"
)

// SigningKey is an OpenPGP public key that driver packages are verified with.
// Its fingerprint and its verifier are derived from the same parsed key.
// A recorded fingerprint, such as the signer pinned in a lock file, names the
// key that packages must verify with; it is not evidence that a verification
// was performed or succeeded.
type SigningKey struct {
	fingerprint string
	verifier    crypto.PGPVerify
	// digest is the SHA-256 of the certificate's packets, which tells two
	// versions of the same primary key apart (new subkeys or revocations).
	digest [sha256.Size]byte
}

// maxSigningKeySize bounds the size of a public key given inline or read from
// a file. Real certificates are a few kilobytes.
const maxSigningKeySize = 1 << 20

const (
	publicKeyArmorType  = "PGP PUBLIC KEY BLOCK"
	privateKeyArmorType = "PGP PRIVATE KEY BLOCK"
)

var errPrivateKey = errors.New("signing key must be a public key, not a private key")

// NewSigningKey parses one OpenPGP certificate (a public key), given in ASCII
// armor or in binary form. The input must hold exactly one primary public key
// together with its user IDs, subkeys and signatures, and nothing else:
// private key material, a second primary key, more than one armored block,
// unknown packets and trailing data are all rejected rather than skipped.
func NewSigningKey(data []byte) (*SigningKey, error) {
	if len(data) > maxSigningKeySize {
		return nil, fmt.Errorf("signing key is larger than %d bytes", maxSigningKeySize)
	}
	payload := data
	if isArmored(data) {
		var err error
		if payload, err = dearmorPublicKey(data); err != nil {
			return nil, err
		}
	}
	if err := checkCertificatePackets(payload); err != nil {
		return nil, err
	}

	key, err := crypto.NewKey(payload)
	if err != nil {
		return nil, fmt.Errorf("could not parse signing key: %w", err)
	}
	if key.IsPrivate() {
		return nil, errPrivateKey
	}
	verifier, err := crypto.PGP().Verify().VerificationKey(key).New()
	if err != nil {
		return nil, fmt.Errorf("could not use signing key: %w", err)
	}
	return &SigningKey{fingerprint: key.GetFingerprint(), verifier: verifier, digest: sha256.Sum256(payload)}, nil
}

// NewArmoredSigningKey is NewSigningKey for input that must be ASCII armored,
// such as a key written inline in a configuration file.
func NewArmoredSigningKey(armored string) (*SigningKey, error) {
	if !isArmored([]byte(armored)) {
		return nil, errors.New("signing key must be an ASCII-armored OpenPGP public key")
	}
	return NewSigningKey([]byte(armored))
}

// isArmored reports whether data is text, and so must be ASCII armor. The
// first byte of a binary OpenPGP packet always has its high bit set.
func isArmored(data []byte) bool {
	text := bytes.TrimLeft(data, " \t\r\n")
	return len(text) > 0 && text[0]&0x80 == 0
}

// dearmorPublicKey decodes the one armored public key block in data. Only
// whitespace may surround the block. The armor decoder skips text before a
// block, stops reading at the checksum line and ignores what follows the
// first block, so the layout of the lines is checked here: the BEGIN line
// first, the END line last, and a checksum line only right before the END
// line.
func dearmorPublicKey(data []byte) ([]byte, error) {
	text := bytes.TrimSpace(data)
	begin := "-----BEGIN " + publicKeyArmorType + "-----"
	end := "-----END " + publicKeyArmorType + "-----"
	if bytes.HasPrefix(text, []byte("-----BEGIN "+privateKeyArmorType+"-----")) {
		return nil, errPrivateKey
	}
	if !bytes.HasPrefix(text, []byte(begin)) {
		return nil, fmt.Errorf("signing key must be an armored %s", publicKeyArmorType)
	}
	if bytes.Count(text, []byte("-----BEGIN ")) != 1 || bytes.Count(text, []byte("-----END ")) != 1 {
		return nil, errors.New("signing key must contain exactly one armored block")
	}

	lines := bytes.Split(text, []byte("\n"))
	for i := range lines {
		lines[i] = bytes.TrimSpace(lines[i])
	}
	last := len(lines) - 1
	if string(lines[0]) != begin {
		return nil, errors.New("signing key has data after the BEGIN line of the armored block")
	}
	if string(lines[last]) != end {
		return nil, errors.New("signing key has data after the end of the armored block")
	}
	for i := 1; i < last-1; i++ {
		if bytes.HasPrefix(lines[i], []byte("=")) {
			return nil, errors.New("signing key has data after the checksum of the armored block")
		}
	}

	block, err := armor.Decode(bytes.NewReader(text))
	if err != nil {
		return nil, fmt.Errorf("could not decode armored signing key: %w", err)
	}
	if block.Type != publicKeyArmorType {
		return nil, fmt.Errorf("signing key must be an armored %s", publicKeyArmorType)
	}
	payload, err := io.ReadAll(block.Body)
	if err != nil {
		return nil, fmt.Errorf("could not decode armored signing key: %w", err)
	}
	return payload, nil
}

// checkCertificatePackets checks that payload is exactly one certificate:
// a primary public key followed only by the packets that belong to it. The
// key ring readers of the OpenPGP libraries skip packets they do not
// understand, so the whole input is split into packets here, and each
// packet must be parsed completely from its own bytes.
func checkCertificatePackets(payload []byte) error {
	primary := false
	for rest := payload; len(rest) > 0; {
		size, err := packetSize(rest)
		if err != nil {
			return fmt.Errorf("invalid OpenPGP public key: %w", err)
		}
		r := bytes.NewReader(rest[:size])
		rest = rest[size:]
		p, err := packet.Read(r)
		if err == nil && r.Len() != 0 {
			err = errors.New("packet has unparsed data")
		}
		if err != nil {
			return fmt.Errorf("invalid OpenPGP public key: %w", err)
		}
		switch p := p.(type) {
		case *packet.PrivateKey:
			return errPrivateKey
		case *packet.PublicKey:
			if !p.IsSubkey {
				if primary {
					return errors.New("signing key must contain exactly one public key, but it contains more than one")
				}
				primary = true
				continue
			}
		case *packet.UserId, *packet.UserAttribute, *packet.Signature, packet.Padding:
		default:
			return fmt.Errorf("signing key contains an unexpected OpenPGP packet (%T)", p)
		}
		if !primary {
			return errors.New("signing key must start with a primary public key")
		}
	}
	if !primary {
		return errors.New("signing key does not contain a public key")
	}
	return nil
}

// packetSize returns the length of the OpenPGP packet at the start of b,
// header included (RFC 9580, section 4.2). Partial body lengths and the
// indeterminate length of the legacy format are only used by data packets,
// so they are rejected here.
func packetSize(b []byte) (int, error) {
	if b[0]&0x80 == 0 {
		return 0, errors.New("packet header does not have its high bit set")
	}
	// The body length is kept in a uint64 until it is known to fit in b, so
	// that a four-octet length cannot overflow an int on 32-bit platforms.
	var header int
	var body uint64
	if b[0]&0x40 != 0 {
		// OpenPGP format.
		if len(b) < 2 {
			return 0, io.ErrUnexpectedEOF
		}
		switch first := uint64(b[1]); {
		case first < 192:
			header, body = 2, first
		case first < 224:
			if len(b) < 3 {
				return 0, io.ErrUnexpectedEOF
			}
			header, body = 3, (first-192)<<8+uint64(b[2])+192
		case first == 255:
			if len(b) < 6 {
				return 0, io.ErrUnexpectedEOF
			}
			header, body = 6, uint64(binary.BigEndian.Uint32(b[2:6]))
		default:
			return 0, errors.New("packet uses a partial body length")
		}
	} else {
		// Legacy format.
		switch b[0] & 3 {
		case 0:
			header = 2
		case 1:
			header = 3
		case 2:
			header = 5
		default:
			return 0, errors.New("packet has an indeterminate length")
		}
		if len(b) < header {
			return 0, io.ErrUnexpectedEOF
		}
		for _, c := range b[1:header] {
			body = body<<8 | uint64(c)
		}
	}
	if body > uint64(len(b)-header) {
		return 0, io.ErrUnexpectedEOF
	}
	return header + int(body), nil
}

var columnarSigningKey = sync.OnceValues(func() (*SigningKey, error) {
	return NewArmoredSigningKey(armoredPubKey)
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
