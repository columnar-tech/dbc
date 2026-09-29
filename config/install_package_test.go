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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInstallPackagePublishesAndRegistersGeneration(t *testing.T) {
	location := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: location}
	archive := writeCustomPackageArchive(t, "name = \"Driver\"\nversion = \"1.0.0\"\n[Driver]\nshared = \"/external/default.so\"\n[Files]\ndriver = \"driver.so\"\n", packageFile("driver.so", "library"))
	var stagingPath string
	manifest, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{Verifier: func(stage string, m Manifest) error {
		stagingPath = stage
		got := m.Driver.Shared.Get(PlatformTuple())
		if got == filepath.Join(stage, "driver.so") || !packageGenerationNameMatches(filepath.Base(filepath.Dir(got)), "driver") {
			return fmt.Errorf("verifier shared path = %q, want planned generation", got)
		}
		if _, err := os.Stat(got); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("planned generation unexpectedly exists: %v", err)
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Stat(); err == nil {
		t.Fatal("archive remains open after successful installation")
	}
	if _, err := os.Stat(stagingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging directory remains: %v", err)
	}
	registered, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	finalLibrary := registered.Driver.Shared.Get(PlatformTuple())
	if !packageGenerationNameMatches(filepath.Base(filepath.Dir(finalLibrary)), "driver") {
		t.Fatalf("registered library path = %q, want package generation", finalLibrary)
	}
	if filepath.Dir(filepath.Dir(finalLibrary)) != location {
		t.Fatalf("generation is outside primary root: %q", finalLibrary)
	}
	if got, err := os.ReadFile(finalLibrary); err != nil || string(got) != strings.Repeat("x", 7) {
		t.Fatalf("installed library = %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Dir(finalLibrary))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o755 {
			t.Fatalf("generation mode = %#o, want 0755", got)
		}
	}
	if manifest.Driver.Shared.Get(PlatformTuple()) != finalLibrary {
		t.Fatalf("returned manifest path = %q, registered path = %q", manifest.Driver.Shared.Get(PlatformTuple()), finalLibrary)
	}
}

func TestInstallPackageFailurePreservesPreviousGeneration(t *testing.T) {
	location := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: location}
	install := func(contents string, verifier PackageVerifier) error {
		archive := writeCustomPackageArchive(t, "name = \"Driver\"\nversion = \"1.0.0\"\n[Files]\ndriver = \"driver.so\"\n", packageFile("driver.so", contents))
		_, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{Verifier: verifier})
		if _, statErr := archive.Stat(); statErr == nil {
			t.Error("archive remains open")
		}
		return err
	}
	if err := install("old", nil); err != nil {
		t.Fatal(err)
	}
	previous, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	previousPath := previous.Driver.Shared.Get(PlatformTuple())

	t.Run("verifier failure", func(t *testing.T) {
		err := install("new", func(string, Manifest) error { return errors.New("rejected") })
		if err == nil || !strings.Contains(err.Error(), "verify package") {
			t.Fatalf("InstallPackage error = %v", err)
		}
		assertPreviousPackage(t, cfg, previousPath)
	})
	t.Run("invalid archive", func(t *testing.T) {
		archive := writeCustomPackageArchive(t, "name = \"Driver\"\nversion = \"1.0.0\"\n[Files]\ndriver = \"missing.so\"\n")
		_, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{})
		if err == nil {
			t.Fatal("expected invalid archive error")
		}
		if _, statErr := archive.Stat(); statErr == nil {
			t.Fatal("archive remains open")
		}
		assertPreviousPackage(t, cfg, previousPath)
	})
	t.Run("generation preparation failure", func(t *testing.T) {
		err := install("new", func(stage string, _ Manifest) error {
			return os.RemoveAll(stage)
		})
		if err == nil {
			t.Fatal("expected generation preparation failure")
		}
		assertPreviousPackage(t, cfg, previousPath)
	})
}

func TestInstallPackagePipelineFailuresPreservePreviousGeneration(t *testing.T) {
	location := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: location}
	installInitialPackage(t, cfg)
	previous, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	previousPath := previous.Driver.Shared.Get(PlatformTuple())
	registrationErr := errors.New("registration failed")

	t.Run("rename publish failure", func(t *testing.T) {
		archive := testPackageArchive(t, "new")
		operations := testPackageInstallOperations()
		operations.rename = func(string, string) error { return errors.New("injected rename failure") }
		_, err := installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, operations)
		if err == nil || !strings.Contains(err.Error(), "publish package generation") {
			t.Fatalf("InstallPackage error = %v", err)
		}
		assertArchiveClosed(t, archive)
		assertPreviousPackage(t, cfg, previousPath)
		assertOnlyPackageGeneration(t, location, filepath.Base(filepath.Dir(previousPath)))
	})
	t.Run("registration failure removes published candidate", func(t *testing.T) {
		archive := testPackageArchive(t, "new")
		operations := testPackageInstallOperations()
		operations.register = func(Config, string, DriverInfo) error { return registrationErr }
		_, err := installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, operations)
		if !errors.Is(err, registrationErr) {
			t.Fatalf("InstallPackage error = %v", err)
		}
		assertArchiveClosed(t, archive)
		assertPreviousPackage(t, cfg, previousPath)
		assertOnlyPackageGeneration(t, location, filepath.Base(filepath.Dir(previousPath)))
	})
	t.Run("rollback failure preserves published candidate", func(t *testing.T) {
		archive := testPackageArchive(t, "new")
		operations := testPackageInstallOperations()
		var candidatePath string
		operations.register = func(_ Config, _ string, driver DriverInfo) error {
			candidatePath = filepath.Dir(driver.Driver.Shared.Get(PlatformTuple()))
			return errors.Join(registrationErr, errRegistrationRollbackFailed)
		}
		_, err := installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, operations)
		if !errors.Is(err, errRegistrationRollbackFailed) || !strings.Contains(err.Error(), packageGenerationNamePrefix) {
			t.Fatalf("InstallPackage error = %v", err)
		}
		assertArchiveClosed(t, archive)
		assertPreviousPackage(t, cfg, previousPath)
		if _, statErr := os.Stat(candidatePath); statErr != nil {
			t.Fatalf("rollback candidate %s was not preserved: %v", candidatePath, statErr)
		}
	})
	t.Run("candidate cleanup failure retains both errors", func(t *testing.T) {
		archive := testPackageArchive(t, "new")
		cleanupErr := errors.New("candidate cleanup failed")
		operations := testPackageInstallOperations()
		operations.register = func(Config, string, DriverInfo) error { return registrationErr }
		var candidatePath string
		operations.removeAll = func(path string) error {
			if packageGenerationNameMatches(filepath.Base(path), "driver") {
				candidatePath = path
				return cleanupErr
			}
			return os.RemoveAll(path)
		}
		_, err := installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, operations)
		if !errors.Is(err, registrationErr) || !errors.Is(err, cleanupErr) {
			t.Fatalf("InstallPackage error = %v", err)
		}
		assertArchiveClosed(t, archive)
		assertPreviousPackage(t, cfg, previousPath)
		if _, statErr := os.Stat(candidatePath); statErr != nil {
			t.Fatalf("candidate %s was not preserved after cleanup failure: %v", candidatePath, statErr)
		}
	})
}

func TestInstallPackageReportsPrecommitCleanupFailures(t *testing.T) {
	location := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: location}
	installInitialPackage(t, cfg)
	previous, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	previousPath := previous.Driver.Shared.Get(PlatformTuple())

	t.Run("staging cleanup", func(t *testing.T) {
		archive := testPackageArchive(t, "new")
		verificationErr := errors.New("verification failed")
		cleanupErr := errors.New("staging cleanup failed")
		operations := testPackageInstallOperations()
		operations.removeAll = func(path string) error {
			if strings.HasPrefix(filepath.Base(path), ".dbc-package-stage-") {
				return cleanupErr
			}
			return os.RemoveAll(path)
		}
		_, err := installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{Verifier: func(string, Manifest) error {
			return verificationErr
		}}, operations)
		if !errors.Is(err, verificationErr) || !errors.Is(err, cleanupErr) || !strings.Contains(err.Error(), ".dbc-package-stage-") {
			t.Fatalf("InstallPackage error = %v", err)
		}
		assertArchiveClosed(t, archive)
		assertPreviousPackage(t, cfg, previousPath)
	})

	t.Run("reservation cleanup", func(t *testing.T) {
		archive := testPackageArchive(t, "new")
		releaseErr := errors.New("reservation release failed")
		cleanupErr := errors.New("reservation cleanup failed")
		operations := testPackageInstallOperations()
		operations.remove = func(string) error { return releaseErr }
		operations.removeAll = func(path string) error {
			if packageGenerationNameMatches(filepath.Base(path), "driver") {
				return cleanupErr
			}
			return os.RemoveAll(path)
		}
		_, err := installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, operations)
		if !errors.Is(err, releaseErr) || !errors.Is(err, cleanupErr) || !strings.Contains(err.Error(), packageGenerationNamePrefix) {
			t.Fatalf("InstallPackage error = %v", err)
		}
		assertArchiveClosed(t, archive)
		assertPreviousPackage(t, cfg, previousPath)
	})
}

func TestInstallPackageManifestOnlyAndMalformedRegistration(t *testing.T) {
	location := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: location}
	if err := os.WriteFile(filepath.Join(location, "external.toml"), []byte("invalid = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := writeCustomPackageArchive(t, "name = \"External\"\nversion = \"1.0.0\"\n[Driver]\nshared = \"/external/lib.so\"\n")
	if _, err := InstallPackage(cfg, "external", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	registered, err := GetDriver(cfg, "external")
	if err != nil {
		t.Fatal(err)
	}
	if got := registered.Driver.Shared.Get(PlatformTuple()); got != "/external/lib.so" {
		t.Fatalf("external shared path = %q", got)
	}
}

func TestInstallPackageRejectsManifestOnlyWithoutCurrentPlatformLibrary(t *testing.T) {
	location := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: location}
	externalLibrary := filepath.Join(t.TempDir(), "external.so")
	if err := os.WriteFile(externalLibrary, []byte("external library"), 0o600); err != nil {
		t.Fatal(err)
	}
	quotedPath := strings.ReplaceAll(externalLibrary, `\`, `\\`)
	validManifest := fmt.Sprintf("name = \"External\"\nversion = \"1.0.0\"\n[Driver]\nshared = \"%s\"\n", quotedPath)
	archive := writeCustomPackageArchive(t, validManifest)
	if _, err := InstallPackage(cfg, "external", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	previous, err := GetDriver(cfg, "external")
	if err != nil {
		t.Fatal(err)
	}
	previousGenerationNames := packageGenerationNames(t, location, "external")
	if len(previousGenerationNames) != 1 {
		t.Fatalf("initial generation count = %d, want 1", len(previousGenerationNames))
	}

	invalidManifests := []struct {
		name     string
		manifest string
	}{
		{name: "no shared library", manifest: "name = \"External\"\nversion = \"2.0.0\"\n"},
		{name: "shared mapping omits current platform", manifest: "name = \"External\"\nversion = \"2.0.0\"\n[Driver.shared]\nother_platform = \"/external/other.so\"\n"},
	}
	for _, test := range invalidManifests {
		t.Run(test.name, func(t *testing.T) {
			invalidArchive := writeCustomPackageArchive(t, test.manifest)
			_, installErr := InstallPackage(cfg, "external", invalidArchive, InstallPackageOptions{})
			if installErr == nil || !strings.Contains(installErr.Error(), "no shared library for platform") {
				t.Fatalf("InstallPackage error = %v", installErr)
			}
			assertArchiveClosed(t, invalidArchive)
			current, getErr := GetDriver(cfg, "external")
			if getErr != nil {
				t.Fatal(getErr)
			}
			if got := current.Driver.Shared.Get(PlatformTuple()); got != previous.Driver.Shared.Get(PlatformTuple()) {
				t.Fatalf("registration changed to %q, want %q", got, previous.Driver.Shared.Get(PlatformTuple()))
			}
			data, readErr := os.ReadFile(externalLibrary)
			if readErr != nil || string(data) != "external library" {
				t.Fatalf("external library changed: %q, %v", data, readErr)
			}
			if got := packageGenerationNames(t, location, "external"); !equalStrings(got, previousGenerationNames) {
				t.Fatalf("generation names = %v, want %v", got, previousGenerationNames)
			}
		})
	}
}

func TestInstallPackageNormalizesRelativePayloadRoot(t *testing.T) {
	absoluteLocation := filepath.Join(t.TempDir(), "relative-root")
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeLocation, err := filepath.Rel(workingDirectory, absoluteLocation)
	if err != nil {
		t.Skipf("cannot create a relative fixture across volumes: %v", err)
	}
	cfg := Config{Level: ConfigEnv, Location: relativeLocation}
	archive := testPackageArchive(t, "library")
	if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	registered, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	sharedPath := registered.Driver.Shared.Get(PlatformTuple())
	if !filepath.IsAbs(sharedPath) {
		t.Fatalf("registered shared path = %q, want absolute path", sharedPath)
	}
}

func TestInstallPackageEnvOnlyTouchesPrimaryRoot(t *testing.T) {
	primary := filepath.Join(t.TempDir(), "primary")
	secondary := filepath.Join(t.TempDir(), "secondary")
	if err := os.WriteFile(secondary, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Level: ConfigEnv, Location: primary + string(filepath.ListSeparator) + secondary}
	archive := writeCustomPackageArchive(t, "name = \"Driver\"\nversion = \"1.0.0\"\n[Files]\ndriver = \"driver.so\"\n", packageFile("driver.so", "library"))
	if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(secondary)
	if err != nil || string(data) != "not a directory" {
		t.Fatalf("secondary root changed: %q, %v", data, err)
	}
}

func TestInstallPackageSerializesSameDriver(t *testing.T) {
	location := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: location}
	firstArchive := writeCustomPackageArchive(t, "name = \"Driver\"\nversion = \"1.0.0\"\n[Files]\ndriver = \"driver.so\"\n", packageFile("driver.so", "first"))
	secondArchive := writeCustomPackageArchive(t, "name = \"Driver\"\nversion = \"1.0.0\"\n[Files]\ndriver = \"driver.so\"\n", packageFile("driver.so", "second"))
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := InstallPackage(cfg, "driver", firstArchive, InstallPackageOptions{Verifier: func(string, Manifest) error {
			close(entered)
			<-release
			return nil
		}})
		firstDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first install did not enter verifier")
	}
	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		_, err := InstallPackage(cfg, "driver", secondArchive, InstallPackageOptions{Verifier: func(string, Manifest) error {
			close(secondEntered)
			return nil
		}})
		secondDone <- err
	}()
	select {
	case <-secondEntered:
		t.Fatal("second install entered verifier while first held the driver lock")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("second install did not proceed after first released the lock")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestInstallPackageSharesLockWithUninstall(t *testing.T) {
	location := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: location}
	installInitialPackage(t, cfg)
	selected, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	archive := testPackageArchive(t, "new")
	entered := make(chan struct{})
	release := make(chan struct{})
	installDone := make(chan error, 1)
	go func() {
		_, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{Verifier: func(string, Manifest) error {
			close(entered)
			<-release
			return nil
		}})
		installDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("install did not enter verifier")
	}
	uninstallDone := make(chan error, 1)
	go func() { uninstallDone <- UninstallDriver(cfg, selected) }()
	select {
	case err := <-uninstallDone:
		t.Fatalf("uninstall completed while install held lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-installDone; err != nil {
		t.Fatal(err)
	}
	if err := <-uninstallDone; !errors.Is(err, errDriverRegistrationChanged) {
		t.Fatalf("uninstall error = %v, want changed registration", err)
	}
	assertArchiveClosed(t, archive)
}

func assertPreviousPackage(t *testing.T, cfg Config, path string) {
	t.Helper()
	registered, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	if got := registered.Driver.Shared.Get(PlatformTuple()); got != path {
		t.Fatalf("registration path = %q, want %q", got, path)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "xxx" {
		t.Fatalf("previous library = %q, %v", data, err)
	}
}

func testPackageArchive(t *testing.T, contents string) *os.File {
	t.Helper()
	return writeCustomPackageArchive(t, "name = \"Driver\"\nversion = \"1.0.0\"\n[Files]\ndriver = \"driver.so\"\n", packageFile("driver.so", contents))
}

func testPackageInstallOperations() packageInstallOperations {
	return packageInstallOperations{
		rename:       os.Rename,
		remove:       os.Remove,
		removeAll:    os.RemoveAll,
		register:     createRuntimeRegistrationUnlocked,
		writeReceipt: writePackageInstallReceipt,
	}
}

func installInitialPackage(t *testing.T, cfg Config) {
	t.Helper()
	archive := testPackageArchive(t, "old")
	if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
}

func assertArchiveClosed(t *testing.T, archive *os.File) {
	t.Helper()
	if _, err := archive.Stat(); err == nil {
		t.Fatal("archive remains open")
	}
}

func assertOnlyPackageGeneration(t *testing.T, location, expected string) {
	t.Helper()
	entries, err := os.ReadDir(location)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if packageGenerationNameMatches(entry.Name(), "driver") || strings.HasPrefix(entry.Name(), ".dbc-package-stage-") {
			if entry.Name() != expected {
				t.Fatalf("unexpected package candidate remains: %s", entry.Name())
			}
		}
	}
}

func packageGenerationNames(t *testing.T, location, runtimeID string) []string {
	t.Helper()
	entries, err := os.ReadDir(location)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if packageGenerationNameMatches(entry.Name(), runtimeID) {
			names = append(names, entry.Name())
		}
	}
	return names
}

func testPackageGenerationPath(t *testing.T, root, runtimeID, suffix string) string {
	t.Helper()
	prefix, err := packageGenerationPrefix(runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, prefix+suffix)
}

func packageGenerationNameMatches(name, runtimeID string) bool {
	generationID, valid := parsePackageGenerationName(name)
	return valid && sameRuntimeID(generationID, runtimeID)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
