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
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanupVerifiesOwnedLibraryIntegrityBeforeRemoval(t *testing.T) {
	for _, mode := range []string{"valid", "mismatch", "missing", "nonregular", "read error"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			cfg := Config{Level: ConfigEnv, Location: root}
			installInitialPackage(t, cfg)
			info, err := GetDriver(cfg, "driver")
			if err != nil {
				t.Fatal(err)
			}
			generation := filepath.Dir(info.Driver.Shared.Get(PlatformTuple()))
			library := info.Driver.Shared.Get(PlatformTuple())
			var injectedErr error
			switch mode {
			case "mismatch":
				if err := os.WriteFile(library, []byte("bad"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(library); err != nil {
					t.Fatal(err)
				}
			case "nonregular":
				if err := os.Remove(library); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(library, 0o700); err != nil {
					t.Fatal(err)
				}
			case "read error":
				injectedErr = errors.New("injected library read failure")
			}
			operations := packageCleanupOperations{}
			if injectedErr != nil {
				operations.hashOwnedLibrary = func(io.Reader) ([]byte, error) { return nil, injectedErr }
			}
			err = cleanupInstalledPackageWithOperations(cfg, root, info, operations)
			if mode == "valid" || mode == "missing" {
				if err != nil {
					t.Fatalf("cleanup failed for %s owned library: %v", mode, err)
				}
				if _, err := os.Stat(generation); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("generation remains after %s library cleanup: %v", mode, err)
				}
				return
			}
			if err == nil {
				t.Fatal("cleanup succeeded despite unverified owned library bytes")
			}
			if mode == "read error" && !errors.Is(err, injectedErr) {
				t.Fatalf("cleanup error = %v, want injected read failure", err)
			}
			if !strings.Contains(err.Error(), generation) {
				t.Fatalf("cleanup error lacks retained generation path: %v", err)
			}
			if _, err := os.Stat(filepath.Join(generation, packageInstallReceiptFilename)); err != nil {
				t.Fatalf("unverified generation receipt was not retained: %v", err)
			}
		})
	}
}

func TestCleanupRetriesAfterOwnedLibraryWasRemoved(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	info, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	generation := filepath.Dir(info.Driver.Shared.Get(PlatformTuple()))
	library := info.Driver.Shared.Get(PlatformTuple())
	cleanupErr := errors.New("injected locked library")
	err = cleanupInstalledPackageWithOperations(cfg, root, info, packageCleanupOperations{
		beforeRemove: func(path string) error {
			if path == library {
				return cleanupErr
			}
			return nil
		},
	})
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("first cleanup error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(generation, packageInstallReceiptFilename)); err != nil {
		t.Fatalf("receipt was not retained after partial cleanup: %v", err)
	}
	if err := os.Remove(library); err != nil {
		t.Fatal(err)
	}
	if err := cleanupInstalledPackageWithOperations(cfg, root, info, packageCleanupOperations{}); err != nil {
		t.Fatalf("retry with missing owned library failed: %v", err)
	}
	if _, err := os.Stat(generation); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("receipt-backed generation remains after cleanup retry: %v", err)
	}
}

func TestStaleCleanupRetainsOwnedLibraryHashMismatch(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	previous, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	previousGeneration := filepath.Dir(previous.Driver.Shared.Get(PlatformTuple()))
	archive := testPackageArchive(t, "updated")
	operations := testPackageInstallOperations()
	operations.cleanupBeforeRemove = func(path string) error {
		if filepath.Dir(path) == previousGeneration && filepath.Base(path) == filepath.Base(previous.Driver.Shared.Get(PlatformTuple())) {
			return errors.New("retain previous generation for integrity test")
		}
		return nil
	}
	if _, err := installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, operations); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	if err := os.WriteFile(previous.Driver.Shared.Get(PlatformTuple()), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	current, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	referenced, uncertain := referencedPaths(root, current.Driver.Shared)
	if uncertain {
		t.Fatal("current registration references were uncertain")
	}
	cleanupStalePackageGenerationsWithReferences(cfg, root, current, []string{filepath.Dir(current.Driver.Shared.Get(PlatformTuple()))}, referenced, false, packageCleanupOperations{})
	if _, err := os.Stat(filepath.Join(previousGeneration, packageInstallReceiptFilename)); err != nil {
		t.Fatalf("stale generation with hash mismatch was removed: %v", err)
	}
}

func TestOwnedLibraryIntegrityIsCheckedOnceBeforeRemoval(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	info, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	checks := 0
	if err := cleanupInstalledPackageWithOperations(cfg, root, info, packageCleanupOperations{
		hashOwnedLibrary: func(reader io.Reader) ([]byte, error) {
			checks++
			data, err := io.ReadAll(reader)
			if err != nil {
				return nil, err
			}
			digest := sha256.Sum256(data)
			return digest[:], nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatalf("owned library was checked %d times, want once", checks)
	}
}

func TestExternalPackageCleanupDoesNotHashSharedLibrary(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	external := filepath.Join(t.TempDir(), "shared.so")
	if err := os.WriteFile(external, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf("name = \"External\"\nversion = \"1.0.0\"\n[Driver]\nshared = %q\n", external)
	archive := writeCustomPackageArchive(t, manifest, packageFile("NOTICE", "metadata"))
	if _, err := InstallPackage(context.Background(), cfg, "external", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	info, err := GetDriver(cfg, "external")
	if err != nil {
		t.Fatal(err)
	}
	checks := 0
	if err := cleanupInstalledPackageWithOperations(cfg, root, info, packageCleanupOperations{
		hashOwnedLibrary: func(io.Reader) ([]byte, error) { checks++; return nil, nil },
	}); err != nil {
		t.Fatal(err)
	}
	if checks != 0 {
		t.Fatalf("external shared library was hashed %d times", checks)
	}
	if _, err := os.Stat(external); err != nil {
		t.Fatalf("external shared library was changed: %v", err)
	}
}
