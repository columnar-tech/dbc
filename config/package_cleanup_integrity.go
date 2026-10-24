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

// verifyPackageOwnedLibraryIntegrityAtRoot compares the installed bytes with
// the receipt's local integrity evidence. This does not prove correspondence
// to any downloaded or locked archive. The receipt must already have passed
// validPackageInstallReceiptForGeneration, which checks the filename and
// digest format. A library that is already absent is not an error.
func verifyPackageOwnedLibraryIntegrityAtRoot(generation *os.Root, receipt packageInstallReceipt, hashLibrary ownedLibraryHashFunc) error {
	filename := receipt.OwnedLibraryFilename
	info, err := generation.Lstat(filename)
	if errors.Is(err, fs.ErrNotExist) {
		// Partial cleanup may already have removed the library while retaining
		// the receipt. Allow a later pass to remove the remaining payload.
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect owned library %s: %w", filename, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("owned library %s is not a regular file", filename)
	}
	file, err := generation.Open(filename)
	if err != nil {
		return fmt.Errorf("open owned library %s: %w", filename, err)
	}
	openedInfo, statErr := file.Stat()
	if statErr != nil {
		closeErr := file.Close()
		return fmt.Errorf("stat opened owned library %s: %w", filename, errors.Join(statErr, closeErr))
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		closeErr := file.Close()
		return errors.Join(fmt.Errorf("opened owned library %s changed identity or is not a regular file", filename), closeErr)
	}
	if hashLibrary == nil {
		hashLibrary = hashSHA256
	}
	actualDigest, copyErr := hashLibrary(file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return fmt.Errorf("hash owned library %s: %w", filename, errors.Join(copyErr, closeErr))
	}
	if len(actualDigest) != sha256.Size {
		return fmt.Errorf("hash owned library %s: got %d digest bytes, want %d", filename, len(actualDigest), sha256.Size)
	}
	actual := hex.EncodeToString(actualDigest)
	if actual != receipt.OwnedLibrarySHA256 {
		return fmt.Errorf("owned library %s SHA-256 does not match installation receipt", filename)
	}
	return nil
}
