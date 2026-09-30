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

package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

type ownedLibraryHashFunc func(io.Reader) ([]byte, error)

type ownedLibraryIntegrityState uint8

const (
	ownedLibraryIntegrityExternal ownedLibraryIntegrityState = iota
	ownedLibraryIntegrityVerified
	ownedLibraryIntegrityMissing
)

// verifyPackageOwnedLibraryIntegrityAtRoot compares the installed bytes with
// the receipt's local integrity evidence. This does not prove correspondence
// to any downloaded or locked archive.
func verifyPackageOwnedLibraryIntegrityAtRoot(generation *os.Root, receipt packageInstallReceipt, hashLibrary ownedLibraryHashFunc) (ownedLibraryIntegrityState, error) {
	switch receipt.LibraryKind {
	case packageLibraryExternal:
		return ownedLibraryIntegrityExternal, nil
	case packageLibraryFile:
	default:
		return 0, fmt.Errorf("invalid package library kind %q", receipt.LibraryKind)
	}
	if err := validateOwnedPackageFilename(receipt.OwnedLibraryFilename); err != nil {
		return 0, fmt.Errorf("invalid owned library filename: %w", err)
	}
	if len(receipt.OwnedLibrarySHA256) != sha256.Size*2 {
		return 0, errors.New("owned library SHA-256 digest has invalid length")
	}
	if _, err := hex.DecodeString(receipt.OwnedLibrarySHA256); err != nil {
		return 0, fmt.Errorf("owned library SHA-256 digest is invalid: %w", err)
	}

	filename := receipt.OwnedLibraryFilename
	info, err := generation.Lstat(filename)
	if errors.Is(err, fs.ErrNotExist) {
		// Partial cleanup may already have removed the library while retaining
		// the receipt. Allow a later pass to remove the remaining payload.
		return ownedLibraryIntegrityMissing, nil
	}
	if err != nil {
		return 0, fmt.Errorf("inspect owned library %s: %w", filename, err)
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("owned library %s is not a regular file", filename)
	}
	file, err := generation.Open(filename)
	if err != nil {
		return 0, fmt.Errorf("open owned library %s: %w", filename, err)
	}
	openedInfo, statErr := file.Stat()
	if statErr != nil {
		closeErr := file.Close()
		return 0, fmt.Errorf("stat opened owned library %s: %w", filename, errors.Join(statErr, closeErr))
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		closeErr := file.Close()
		return 0, errors.Join(fmt.Errorf("opened owned library %s changed identity or is not a regular file", filename), closeErr)
	}
	var actualDigest []byte
	var copyErr error
	if hashLibrary != nil {
		actualDigest, copyErr = hashLibrary(file)
	} else {
		hash := sha256.New()
		_, copyErr = io.Copy(hash, file)
		actualDigest = hash.Sum(nil)
	}
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return 0, fmt.Errorf("hash owned library %s: %w", filename, errors.Join(copyErr, closeErr))
	}
	if len(actualDigest) != sha256.Size {
		return 0, fmt.Errorf("hash owned library %s: got %d digest bytes, want %d", filename, len(actualDigest), sha256.Size)
	}
	actual := hex.EncodeToString(actualDigest)
	if actual != receipt.OwnedLibrarySHA256 {
		return 0, fmt.Errorf("owned library %s SHA-256 does not match installation receipt", filename)
	}
	return ownedLibraryIntegrityVerified, nil
}
