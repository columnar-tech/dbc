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

//go:build !js && !plan9

package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/columnar-tech/dbc/internal/fslock"
)

func TestFilesystemCaseIdentity(t *testing.T) {
	probe := probeFilesystemCaseBehavior(t)
	t.Logf("filesystem case probe: directories case-insensitive=%t, files case-insensitive=%t", probe.directoriesCaseInsensitive, probe.filesCaseInsensitive)
	if probe.directoriesCaseInsensitive != probe.filesCaseInsensitive {
		t.Skip("directory and file case behavior differ on this filesystem; focused identity cases need a consistent probe")
	}

	if probe.directoriesCaseInsensitive {
		t.Run("case-insensitive filesystem", func(t *testing.T) {
			t.Run("case-variant reference protects generation with missing ancestor", func(t *testing.T) {
				root := t.TempDir()
				cfg := Config{Level: ConfigEnv, Location: root}
				installInitialPackage(t, cfg)
				selected, err := GetDriver(cfg, "driver")
				if err != nil {
					t.Fatal(err)
				}
				generation := filepath.Dir(selected.Driver.Shared.Get(PlatformTuple()))
				caseVariant := strings.ToUpper(filepath.Base(generation))
				if same, err := samePathIdentity(generation, filepath.Join(root, caseVariant)); err != nil || !same {
					t.Fatalf("generation case variant identity = %t, err=%v; want same path", same, err)
				}

				borrower := driverMap{}
				borrower.Set(PlatformTuple(), filepath.Join(root, caseVariant, "missing", "ancestor", "borrowed.so"))
				if err := cleanupInstalledPackageWithReferences(cfg, root, selected, []driverMap{borrower}, true, packageCleanupOperations{}); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(generation); err != nil {
					t.Fatalf("case-variant reference failed to protect generation: %v", err)
				}
				if _, err := os.Stat(selected.Driver.Shared.Get(PlatformTuple())); err != nil {
					t.Fatalf("owned library disappeared with its referenced generation: %v", err)
				}
			})

			t.Run("case-variant reference protects existing library", func(t *testing.T) {
				root := t.TempDir()
				cfg := Config{Level: ConfigEnv, Location: root}
				installInitialPackage(t, cfg)
				selected, err := GetDriver(cfg, "driver")
				if err != nil {
					t.Fatal(err)
				}
				library := selected.Driver.Shared.Get(PlatformTuple())
				generation := filepath.Dir(library)
				caseVariantLibrary := filepath.Join(root, strings.ToUpper(filepath.Base(generation)), strings.ToUpper(filepath.Base(library)))
				if same, err := samePathIdentity(library, caseVariantLibrary); err != nil || !same {
					t.Fatalf("library case variant identity = %t, err=%v; want same file", same, err)
				}

				borrower := driverMap{}
				borrower.Set(PlatformTuple(), caseVariantLibrary)
				if err := cleanupInstalledPackageWithReferences(cfg, root, selected, []driverMap{borrower}, true, packageCleanupOperations{}); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(generation); err != nil {
					t.Fatalf("case-variant library reference failed to protect generation: %v", err)
				}
				if _, err := os.Stat(library); err != nil {
					t.Fatalf("referenced library disappeared: %v", err)
				}
			})

			t.Run("namespace lock through case-variant root contends", func(t *testing.T) {
				first, rootAlias := probe.root, probe.rootVariant
				lock, _, err := acquireRegistrationNamespaceLock(context.Background(), Config{Level: ConfigEnv}, first, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.release()
				second, _, err := acquireRegistrationNamespaceLock(context.Background(), Config{Level: ConfigEnv}, rootAlias, 20*time.Millisecond)
				if second != nil {
					_ = second.release()
				}
				if !errors.Is(err, fslock.ErrLockContended) {
					t.Fatalf("case-variant namespace lock error = %v, want ErrLockContended", err)
				}
			})

			t.Run("driver ID case variant contends", func(t *testing.T) {
				lock, err := acquireDriverInstallLockWith(context.Background(), probe.root, "Driver", time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.release()
				second, err := acquireDriverInstallLockWith(context.Background(), probe.root, "driver", 20*time.Millisecond)
				if second != nil {
					_ = second.release()
				}
				if !errors.Is(err, fslock.ErrLockContended) {
					t.Fatalf("Driver/driver lock error = %v, want ErrLockContended", err)
				}
			})
		})
		return
	}

	t.Run("case-sensitive filesystem", func(t *testing.T) {
		rootInfo, err := os.Stat(probe.root)
		if err != nil {
			t.Fatal(err)
		}
		variantInfo, err := os.Stat(probe.rootVariant)
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(rootInfo, variantInfo) {
			t.Fatal("case-sensitive root names unexpectedly identify the same directory")
		}
		fileInfo, err := os.Stat(probe.file)
		if err != nil {
			t.Fatal(err)
		}
		fileVariantInfo, err := os.Stat(probe.fileVariant)
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(fileInfo, fileVariantInfo) {
			t.Fatal("case-sensitive file names unexpectedly identify the same file")
		}
		if sameRuntimeID("Driver", "driver") {
			t.Fatal("case-sensitive runtime IDs unexpectedly compare equal")
		}

		first, _, err := acquireRegistrationNamespaceLock(context.Background(), Config{Level: ConfigEnv}, probe.root, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		second, _, err := acquireRegistrationNamespaceLock(context.Background(), Config{Level: ConfigEnv}, probe.rootVariant, time.Second)
		if err != nil {
			_ = first.release()
			t.Fatalf("distinct case-sensitive namespace acquired same lock: %v", err)
		}
		if err := second.release(); err != nil {
			t.Fatal(err)
		}
		if err := first.release(); err != nil {
			t.Fatal(err)
		}

		firstDriver, err := acquireDriverInstallLockWith(context.Background(), probe.root, "Driver", time.Second)
		if err != nil {
			t.Fatal(err)
		}
		secondDriver, err := acquireDriverInstallLockWith(context.Background(), probe.root, "driver", time.Second)
		if err != nil {
			_ = firstDriver.release()
			t.Fatalf("case-sensitive Driver/driver IDs unexpectedly contended: %v", err)
		}
		if err := secondDriver.release(); err != nil {
			t.Fatal(err)
		}
		if err := firstDriver.release(); err != nil {
			t.Fatal(err)
		}

		root := t.TempDir()
		cfg := Config{Level: ConfigEnv, Location: root}
		installInitialPackage(t, cfg)
		selected, err := GetDriver(cfg, "driver")
		if err != nil {
			t.Fatal(err)
		}
		generation := filepath.Dir(selected.Driver.Shared.Get(PlatformTuple()))
		borrower := driverMap{}
		borrower.Set(PlatformTuple(), filepath.Join(root, strings.ToUpper(filepath.Base(generation)), "missing", "ancestor", "borrowed.so"))
		if err := cleanupInstalledPackageWithReferences(cfg, root, selected, []driverMap{borrower}, true, packageCleanupOperations{}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(generation); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("case-distinct reference protected generation: %v", err)
		}
	})
}

type filesystemCaseProbe struct {
	root, rootVariant          string
	file, fileVariant          string
	directoriesCaseInsensitive bool
	filesCaseInsensitive       bool
}

func probeFilesystemCaseBehavior(t *testing.T) filesystemCaseProbe {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "CaseProbe")
	rootVariant := filepath.Join(parent, "caseprobe")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(rootVariant)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(rootVariant, 0o700); err != nil {
			t.Fatal(err)
		}
		rootInfo, err = os.Stat(rootVariant)
	}
	if err != nil {
		t.Fatalf("stat mixed-case root variant: %v", err)
	}
	originalRootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(root, "Library.so")
	fileVariant := filepath.Join(root, "library.so")
	if err := os.WriteFile(file, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(fileVariant)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(fileVariant, []byte("second"), 0o600); err != nil {
			t.Fatal(err)
		}
		fileInfo, err = os.Stat(fileVariant)
	}
	if err != nil {
		t.Fatalf("stat mixed-case file variant: %v", err)
	}
	originalFileInfo, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}

	return filesystemCaseProbe{
		root:                       root,
		rootVariant:                rootVariant,
		file:                       file,
		fileVariant:                fileVariant,
		directoriesCaseInsensitive: os.SameFile(originalRootInfo, rootInfo),
		filesCaseInsensitive:       os.SameFile(originalFileInfo, fileInfo),
	}
}

func samePathIdentity(a, b string) (bool, error) {
	aInfo, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bInfo, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(aInfo, bInfo), nil
}
