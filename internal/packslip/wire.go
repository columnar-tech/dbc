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

package packslip

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func decodeStrict(data []byte, destination any) error {
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

// decodeWire keeps the structural protections required for packslip payloads
// while allowing future optional fields to be ignored by this consumer.
// Trust-store files intentionally continue to use decodeStrict.
func decodeWire(data []byte, destination any) error {
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if seen[key] {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			seen[key] = true
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

// peekBundleDigest reads only the digest needed to invoke the cryptographic
// verifier. Its result is untrusted and is never used to establish identity or
// accept metadata; strict parsing happens only after Verify succeeds.
func peekBundleDigest(bundleJSON []byte) (string, string, []byte, error) {
	var raw struct {
		Envelope struct {
			Payload     string `json:"payload"`
			PayloadType string `json:"payloadType"`
		} `json:"dsseEnvelope"`
	}
	if err := json.Unmarshal(bundleJSON, &raw); err != nil {
		return "", "", nil, fmt.Errorf("decode Sigstore bundle envelope: %w", err)
	}
	if raw.Envelope.PayloadType != "application/vnd.in-toto+json" {
		return "", "", nil, fmt.Errorf("Sigstore bundle payload type %q is not in-toto JSON", raw.Envelope.PayloadType)
	}
	payload, err := base64.StdEncoding.DecodeString(raw.Envelope.Payload)
	if err != nil {
		return "", "", nil, fmt.Errorf("decode Sigstore bundle payload: %w", err)
	}
	var candidate struct {
		Subject []subject `json:"subject"`
	}
	if err := json.Unmarshal(payload, &candidate); err != nil {
		return "", "", nil, fmt.Errorf("peek Sigstore statement subject: %w", err)
	}
	for _, entry := range candidate.Subject {
		if digest, ok := entry.Digest["sha256"]; ok && validHexDigest(digest, 64) {
			return "sha256", digest, payload, nil
		}
		if digest, ok := entry.Digest["sha512"]; ok && validHexDigest(digest, 128) {
			return "sha512", digest, payload, nil
		}
	}
	return "", "", nil, errors.New("Sigstore statement has no supported SHA-256 or SHA-512 subject for verification")
}
