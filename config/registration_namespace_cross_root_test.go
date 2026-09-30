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
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Masterminds/semver/v3"
)

func TestCollectRegistrationSharedMapsAcrossConfigEnvRoots(t *testing.T) {
	primary := t.TempDir()
	secondary := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: primary + string(filepath.ListSeparator) + secondary}

	owner := crossRootRegistration("driver", primary, filepath.Join(primary, "generation", "driver.so"))
	if err := createRuntimeRegistrationUnlocked(Config{Level: ConfigEnv, Location: primary}, primary, owner); err != nil {
		t.Fatal(err)
	}
	borrower := crossRootRegistration("driver", secondary, filepath.Join("relative", "borrowed.so"))
	if err := createRuntimeRegistrationUnlocked(Config{Level: ConfigEnv, Location: secondary}, secondary, borrower); err != nil {
		t.Fatal(err)
	}

	shared, certain, err := collectRegistrationSharedMapsExcluding(cfg, primary, primary, "driver")
	if err != nil || !certain {
		t.Fatalf("collect references = %v, certain=%t, err=%v", shared, certain, err)
	}
	if len(shared) != 1 {
		t.Fatalf("collected %d registrations, want lower-priority same-ID registration", len(shared))
	}
	want := filepath.Join(secondary, "relative", "borrowed.so")
	if got := shared[0].Get(PlatformTuple()); got != want {
		t.Fatalf("relative shared path = %q, want source-root path %q", got, want)
	}
}

func TestCollectRegistrationSharedMapsTreatsMissingAndUnreadableRootsConservatively(t *testing.T) {
	primary := t.TempDir()
	missing := filepath.Join(t.TempDir(), "not-created")
	cfg := Config{Level: ConfigEnv, Location: primary + string(filepath.ListSeparator) + missing}
	if _, certain, err := collectRegistrationSharedMaps(cfg, primary, ""); err != nil || !certain {
		t.Fatalf("missing root should have no references: certain=%t, err=%v", certain, err)
	}

	unreadable := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(unreadable, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Location = primary + string(filepath.ListSeparator) + unreadable
	if _, certain, err := collectRegistrationSharedMaps(cfg, primary, ""); err == nil || certain {
		t.Fatalf("unreadable root should make references uncertain: certain=%t, err=%v", certain, err)
	}

	malformed := t.TempDir()
	if err := os.WriteFile(filepath.Join(malformed, "broken.toml"), []byte("invalid = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Location = primary + string(filepath.ListSeparator) + malformed
	if _, certain, err := collectRegistrationSharedMaps(cfg, primary, ""); err == nil || certain {
		t.Fatalf("malformed registration should make references uncertain: certain=%t, err=%v", certain, err)
	}
}

func TestCollectRegistrationSharedMapsExcludesSymlinkAliasOfTargetRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory symlink creation is not generally available on Windows")
	}
	primary := t.TempDir()
	alias := filepath.Join(t.TempDir(), "primary-alias")
	if err := os.Symlink(primary, alias); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	owner := crossRootRegistration("driver", primary, filepath.Join(primary, "driver.so"))
	if err := createRuntimeRegistrationUnlocked(Config{Level: ConfigEnv, Location: primary}, primary, owner); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Level: ConfigEnv, Location: alias}
	shared, certain, err := collectRegistrationSharedMapsExcluding(cfg, primary, primary, owner.ID)
	if err != nil || !certain {
		t.Fatalf("collect references through symlink alias = %v, certain=%t, err=%v", shared, certain, err)
	}
	if len(shared) != 0 {
		t.Fatalf("target registration was counted through its root alias: %v", shared)
	}
}

func TestCollectRegistrationSharedMapsSkipsUnreadableRoot(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("this platform does not enforce the POSIX directory permission fixture")
	}
	primary := t.TempDir()
	secondary := t.TempDir()
	borrower := crossRootRegistration("borrower", secondary, filepath.Join(secondary, "shared.so"))
	if err := createRuntimeRegistrationUnlocked(Config{Level: ConfigEnv, Location: secondary}, secondary, borrower); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(secondary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secondary, info.Mode().Perm()&^0o777); err != nil {
		t.Skipf("cannot remove all directory permissions: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(secondary, info.Mode().Perm()) })

	cfg := Config{Level: ConfigEnv, Location: primary + string(filepath.ListSeparator) + secondary}
	_, certain, err := collectRegistrationSharedMaps(cfg, primary, "")
	if err == nil && certain {
		if _, readErr := os.ReadDir(secondary); readErr == nil {
			t.Skip("filesystem permissions do not prevent this process from reading the directory")
		}
		t.Fatal("collector claimed certainty for a root that cannot be opened")
	}
	if certain {
		t.Fatalf("unreadable root returned certain=true, err=%v", err)
	}
}

func TestUninstallProtectsExternalLibraryReferencedFromSecondaryRoot(t *testing.T) {
	primary := t.TempDir()
	secondary := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: primary + string(filepath.ListSeparator) + secondary}
	shared := filepath.Join(primary, "shared.so")
	if err := os.WriteFile(shared, []byte("shared library"), 0o600); err != nil {
		t.Fatal(err)
	}
	owner := crossRootRegistration("owner", primary, shared)
	if err := createRuntimeRegistrationUnlocked(Config{Level: ConfigEnv, Location: primary}, primary, owner); err != nil {
		t.Fatal(err)
	}
	aliasDirectory := filepath.Join(secondary, "primary-alias")
	if err := os.Symlink(filepath.Dir(shared), aliasDirectory); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	borrower := crossRootRegistration("borrower", secondary, filepath.Join("primary-alias", filepath.Base(shared)))
	if err := createRuntimeRegistrationUnlocked(Config{Level: ConfigEnv, Location: secondary}, secondary, borrower); err != nil {
		t.Fatal(err)
	}
	secondaryEntries := makeDirectoryReadOnly(t, secondary)
	t.Setenv(adbcEnvVar, primary+string(filepath.ListSeparator)+secondary)
	t.Setenv("VIRTUAL_ENV", "")
	t.Setenv("CONDA_PREFIX", "")
	selected, err := GetDriver(Config{Level: ConfigEnv, Location: primary}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := UninstallDriverShared(selected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("standalone shared cleanup removed library despite secondary reference: %v", err)
	}
	assertDirectoryEntriesUnchanged(t, secondary, secondaryEntries)
	if err := UninstallDriver(cfg, selected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("external shared library was removed despite secondary reference: %v", err)
	}
	if _, err := os.Stat(filepath.Join(primary, owner.ID+".toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target registration remains after uninstall: %v", err)
	}
	assertDirectoryEntriesUnchanged(t, secondary, secondaryEntries)
}

func TestUninstallDriverSharedRetainsFilesWithoutPinnedCleanup(t *testing.T) {
	if supportsPinnedCleanup() {
		t.Skip("host provides pinned-root cleanup")
	}
	root := t.TempDir()
	shared := filepath.Join(root, "driver.so")
	if err := os.WriteFile(shared, []byte("external library"), 0o600); err != nil {
		t.Fatal(err)
	}
	info := crossRootRegistration("driver", root, shared)
	if err := UninstallDriverShared(info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("external payload was removed on a host without pinned cleanup: %v", err)
	}
}

func TestInstallPackageGCProtectsGenerationReferencedFromSecondaryRoot(t *testing.T) {
	primary := t.TempDir()
	secondary := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: primary + string(filepath.ListSeparator) + secondary}
	installInitialPackage(t, Config{Level: ConfigEnv, Location: primary})
	owner, err := GetDriver(Config{Level: ConfigEnv, Location: primary}, "driver")
	if err != nil {
		t.Fatal(err)
	}
	oldLibrary := owner.Driver.Shared.Get(PlatformTuple())
	aliasDirectory := filepath.Join(secondary, "primary-generation-alias")
	if err := os.Symlink(filepath.Dir(oldLibrary), aliasDirectory); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	borrower := crossRootRegistration("borrower", secondary, filepath.Join("primary-generation-alias", filepath.Base(oldLibrary)))
	if err := createRuntimeRegistrationUnlocked(Config{Level: ConfigEnv, Location: secondary}, secondary, borrower); err != nil {
		t.Fatal(err)
	}
	secondaryEntries := makeDirectoryReadOnly(t, secondary)
	archive := testPackageArchive(t, "new")
	if _, err := InstallPackage(context.Background(), cfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	if _, err := os.Stat(oldLibrary); err != nil {
		t.Fatalf("cross-root referenced generation was collected: %v", err)
	}
	assertDirectoryEntriesUnchanged(t, secondary, secondaryEntries)
}

func TestUninstallCountsLowerPriorityRegistrationWithSameID(t *testing.T) {
	primary := t.TempDir()
	secondary := t.TempDir()
	primaryCfg := Config{Level: ConfigEnv, Location: primary}
	cfg := Config{Level: ConfigEnv, Location: primary + string(filepath.ListSeparator) + secondary}
	installInitialPackage(t, primaryCfg)
	owner, err := GetDriver(primaryCfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	ownedLibrary := owner.Driver.Shared.Get(PlatformTuple())
	aliasDirectory := filepath.Join(secondary, "primary-generation-alias")
	if err := os.Symlink(filepath.Dir(ownedLibrary), aliasDirectory); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	borrower := crossRootRegistration("driver", secondary, filepath.Join("primary-generation-alias", filepath.Base(ownedLibrary)))
	if err := createRuntimeRegistrationUnlocked(Config{Level: ConfigEnv, Location: secondary}, secondary, borrower); err != nil {
		t.Fatal(err)
	}

	selected, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	if selected.FilePath != primary {
		t.Fatalf("selected registration root = %q, want primary root %q", selected.FilePath, primary)
	}
	if err := UninstallDriver(cfg, selected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ownedLibrary); err != nil {
		t.Fatalf("lower-priority same-ID registration lost its library: %v", err)
	}
	if _, err := os.Stat(filepath.Join(secondary, "driver.toml")); err != nil {
		t.Fatalf("lower-priority registration was removed: %v", err)
	}
}

func crossRootRegistration(id, root, sharedPath string) DriverInfo {
	info := DriverInfo{ID: id, FilePath: root, Name: "Cross-root test driver", Version: semver.MustParse("1.0.0"), Source: "external"}
	info.Driver.Shared.Set(PlatformTuple(), sharedPath)
	return info
}

func makeDirectoryReadOnly(t *testing.T, path string) []string {
	t.Helper()
	if runtime.GOOS == "windows" || runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("this platform does not support the POSIX read-only directory fixture")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	names := directoryEntryNames(t, path)
	if err := os.Chmod(path, info.Mode().Perm()&^0o222); err != nil {
		t.Skipf("cannot make secondary root read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, info.Mode().Perm()) })
	return names
}

func assertDirectoryEntriesUnchanged(t *testing.T, path string, want []string) {
	t.Helper()
	got := directoryEntryNames(t, path)
	if len(got) != len(want) {
		t.Fatalf("secondary root entries = %v, want unchanged entries %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("secondary root entries = %v, want unchanged entries %v", got, want)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("secondary root permissions became writable: %#o", info.Mode().Perm())
	}
}

func directoryEntryNames(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}
