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
	"os"
	"path/filepath"
	"testing"
)

func TestFilesystemPathIdentityUsesExistingFileIdentity(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "library.so")
	second := filepath.Join(root, "hardlink.so")
	other := filepath.Join(root, "other.so")
	if err := os.WriteFile(first, []byte("library"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Skipf("hardlinks are unavailable: %v", err)
	}
	if err := os.WriteFile(other, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if equal, certain := sameResolvedFilesystemPathWithCertainty(first, second); !equal || !certain {
		t.Fatalf("hardlink equality = (%t, %t), want (true, true)", equal, certain)
	}
	if equal, certain := sameResolvedFilesystemPathWithCertainty(first, other); equal || !certain {
		t.Fatalf("distinct-file equality = (%t, %t), want (false, true)", equal, certain)
	}
	if equal, certain := sameResolvedFilesystemPathWithCertainty(first, filepath.Join(root, "missing.so")); equal || !certain {
		t.Fatalf("existing/missing equality = (%t, %t), want (false, true)", equal, certain)
	}
	if equal, certain := sameResolvedFilesystemPathWithCertainty(filepath.Join(root, "missing.so"), filepath.Join(root, "missing.so")); !equal || !certain {
		t.Fatalf("same missing-path equality = (%t, %t), want (true, true)", equal, certain)
	}
	if equal, certain := sameResolvedFilesystemPathWithCertainty(filepath.Join(root, "missing-a.so"), filepath.Join(root, "missing-b.so")); equal || certain {
		t.Fatalf("different missing-path equality = (%t, %t), want (false, false)", equal, certain)
	}

	generation := filepath.Join(root, "generation")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	packageLibrary := filepath.Join(generation, "driver.so")
	if err := os.WriteFile(packageLibrary, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	externalHardlink := filepath.Join(root, "external.so")
	if err := os.Link(packageLibrary, externalHardlink); err != nil {
		t.Skipf("hardlinks are unavailable: %v", err)
	}
	if !sameFilesystemPathOrUncertain(packageLibrary, externalHardlink) {
		t.Fatal("external hardlink did not retain exact shared-file reference")
	}
	if pathWithin(generation, externalHardlink) || generationReferenced(generation, []string{externalHardlink}) {
		t.Fatal("external hardlink was treated as a descendant of its source generation")
	}
}

func TestFilesystemPathContainmentUsesDirectoryIdentity(t *testing.T) {
	root := t.TempDir()
	generation := filepath.Join(root, "generation")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generation, "driver.so"), []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	missingDescendant := filepath.Join(generation, "missing", "ancestor", "borrowed.so")
	if !pathWithin(generation, missingDescendant) {
		t.Fatal("missing descendant did not resolve to the existing generation directory")
	}
	if !generationReferenced(generation, []string{missingDescendant}) {
		t.Fatal("missing descendant reference did not protect generation")
	}
	otherDirectory := filepath.Join(root, "other")
	if err := os.Mkdir(otherDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if pathWithin(generation, filepath.Join(otherDirectory, "missing.so")) {
		t.Fatal("a missing file under a distinct directory was treated as contained")
	}
	if pathWithin(generation, filepath.Join(root, "outside.so")) {
		t.Fatal("a distinct missing file was treated as contained")
	}

	alias := filepath.Join(root, "generation-alias")
	if err := os.Symlink(generation, alias); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	if !pathWithin(generation, filepath.Join(alias, "missing", "borrowed.so")) {
		t.Fatal("symlinked missing descendant did not resolve to generation")
	}

	broken := filepath.Join(root, "broken")
	if err := os.Symlink(filepath.Join(root, "absent"), broken); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	if !pathWithin(generation, filepath.Join(broken, "borrowed.so")) {
		t.Fatal("broken symlink reference was not treated as uncertain")
	}
	if equal, certain := sameResolvedFilesystemPathWithCertainty(filepath.Join(broken, "library.so"), filepath.Join(broken, "library.so")); equal || certain {
		t.Fatalf("broken symlink equality = (%t, %t), want uncertain", equal, certain)
	}

	nonDirectory := filepath.Join(root, "file")
	if err := os.WriteFile(nonDirectory, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	invalidDescendant := filepath.Join(nonDirectory, "child")
	if !pathWithin(generation, invalidDescendant) {
		t.Fatal("ENOTDIR reference was not treated as uncertain")
	}
	if equal, certain := sameResolvedFilesystemPathWithCertainty(invalidDescendant, invalidDescendant); equal || certain {
		t.Fatalf("ENOTDIR equality = (%t, %t), want uncertain", equal, certain)
	}
}

func TestReservedTransactionPrefixIsCaseFoldedOnEveryPlatform(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".dbc-package-stage-abc", ".DBC-PACKAGE-STAGE-abc", ".DbC-PaCkAgE-g-driver-token"} {
		if !hasReservedTransactionAncestor(filepath.Join(root, name, "driver.so")) {
			t.Errorf("transaction-like path %q was not reserved", name)
		}
	}
	if hasReservedTransactionAncestor(filepath.Join(root, ".dbc-package-", "driver.so")) {
		t.Fatal("bare transaction prefix without a suffix was reserved")
	}
}

func TestFilesystemPathIdentityTreatsNonNotExistErrorsAsUncertain(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "directory", "child")
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	notDirectory := filepath.Join(path, "nested")
	if _, err := os.Stat(notDirectory); err == nil {
		t.Fatal("unexpected path unexpectedly exists")
	}
	if equal, certain := sameResolvedFilesystemPathWithCertainty(notDirectory, notDirectory); equal || certain {
		t.Fatalf("non-directory ancestry equality = (%t, %t), want uncertain", equal, certain)
	}
}
