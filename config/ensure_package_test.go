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
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
)

func installEnsureFixture(t *testing.T, cfg Config, version, source string, archive []byte) DriverInfo {
	t.Helper()
	file := writeInstallArchive(t, archive, "ensure-fixture")
	_, err := InstallPackage(cfg, "example", file, expectedEnsurePackage("example", version, source, archive), InstallOptions{})
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	info, err := GetDriver(cfg, "example")
	if err != nil {
		t.Fatal(err)
	}
	if info.Version == nil || info.Version.String() != version {
		t.Fatalf("fixture version = %v, want %s", info.Version, version)
	}
	return info
}

func packageFileProvider(t *testing.T, data []byte) (*os.File, func() (*os.File, error)) {
	t.Helper()
	file := writeInstallArchive(t, data, "ensure-candidate")
	return file, func() (*os.File, error) { return file, nil }
}

func expectedEnsurePackage(id, version, source string, archive []byte) ExpectedPackageMetadata {
	expected := installExpected(id, source, archive)
	expected.Version = version
	return expected
}

func TestEnsurePackageSkipsWithoutRequestingArchive(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	archive := makeInstallArchive(t, "example", "1.0.0", "driver.so", []byte("same library"))
	initial := installEnsureFixture(t, cfg, "1.0.0", "source", archive)
	providerCalls := 0
	var validated EnsurePackageResult
	result, err := EnsurePackage(context.Background(), cfg, "example", installExpected("example", "source", archive), InstallOptions{}, EnsurePackageCallbacks{
		CurrentMatches: func(current *DriverInfo) (bool, error) {
			return current != nil && sameDriverRegistration(initial, *current), nil
		},
		Archive: func() (*os.File, error) {
			providerCalls++
			return nil, errors.New("archive provider must not run for a skip")
		},
		ValidateResult: func(result EnsurePackageResult) error {
			validated = result
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Skipped || providerCalls != 0 {
		t.Fatalf("EnsurePackage result skipped=%t provider calls=%d, want skip and zero calls", result.Skipped, providerCalls)
	}
	if result.Current == nil || result.Installed == nil || result.Previous != nil || result.Manifest != nil {
		t.Fatalf("skip result fields are inconsistent: %#v", result)
	}
	if validated.Skipped != result.Skipped || validated.Installed == nil {
		t.Fatalf("validator did not receive completed skip result: %#v", validated)
	}
}

func TestEnsurePackageInstallsExactProvidedArchiveAndLeavesItOpen(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	oldArchive := makeInstallArchive(t, "example", "1.0.0", "old.so", []byte("old library"))
	initial := installEnsureFixture(t, cfg, "1.0.0", "old-source", oldArchive)
	newArchive := makeInstallArchive(t, "example", "2.0.0", "new.so", []byte("new library"))
	archiveFile, provider := packageFileProvider(t, newArchive)
	providerCalls := 0
	result, err := EnsurePackage(context.Background(), cfg, "example", expectedEnsurePackage("example", "2.0.0", "new-source", newArchive), InstallOptions{}, EnsurePackageCallbacks{
		CurrentMatches: func(current *DriverInfo) (bool, error) { return false, nil },
		Archive: func() (*os.File, error) {
			providerCalls++
			return provider()
		},
		ValidateResult: func(result EnsurePackageResult) error {
			if result.Skipped || result.Current == nil || result.Previous == nil || result.Installed == nil || result.Manifest == nil {
				return errors.New("install result is missing distinct registration evidence")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Skipped || providerCalls != 1 {
		t.Fatalf("EnsurePackage result skipped=%t provider calls=%d, want install and one call", result.Skipped, providerCalls)
	}
	if result.Current == nil || result.Current.Driver.Shared.Get(PlatformTuple()) != initial.Driver.Shared.Get(PlatformTuple()) {
		t.Fatalf("current registration does not describe the pre-install generation: %#v", result.Current)
	}
	if result.Previous == nil || result.Previous.Driver.Shared.Get(PlatformTuple()) != initial.Driver.Shared.Get(PlatformTuple()) {
		t.Fatalf("previous registration does not describe the replaced generation: %#v", result.Previous)
	}
	if result.Installed == nil || result.Installed.Version.String() != "2.0.0" || result.Manifest == nil {
		t.Fatalf("installed registration/manifest does not describe the candidate: %#v", result)
	}
	if _, err := archiveFile.Stat(); err != nil {
		t.Fatalf("EnsurePackage closed the caller-owned archive: %v", err)
	}
	_ = archiveFile.Close()
}

func TestEnsurePackageErrorsBeforeMutationAndReportsPostInstallValidationFailure(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	oldArchive := makeInstallArchive(t, "example", "1.0.0", "old.so", []byte("old library"))
	installEnsureFixture(t, cfg, "1.0.0", "old-source", oldArchive)
	oldManifest, err := os.ReadFile(filepath.Join(root, "example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	newArchive := makeInstallArchive(t, "example", "2.0.0", "new.so", []byte("new library"))

	callbackErr := errors.New("predicate failed")
	providerCalls := 0
	_, err = EnsurePackage(context.Background(), cfg, "example", expectedEnsurePackage("example", "2.0.0", "new-source", newArchive), InstallOptions{}, EnsurePackageCallbacks{
		CurrentMatches: func(*DriverInfo) (bool, error) { return false, callbackErr },
		Archive: func() (*os.File, error) {
			providerCalls++
			return writeInstallArchive(t, newArchive, "unused"), nil
		},
	})
	if !errors.Is(err, callbackErr) || providerCalls != 0 {
		t.Fatalf("predicate error = %v, provider calls = %d", err, providerCalls)
	}

	providerErr := errors.New("provider failed")
	_, err = EnsurePackage(context.Background(), cfg, "example", expectedEnsurePackage("example", "2.0.0", "new-source", newArchive), InstallOptions{}, EnsurePackageCallbacks{
		CurrentMatches: func(*DriverInfo) (bool, error) { return false, nil },
		Archive:        func() (*os.File, error) { return nil, providerErr },
	})
	if !errors.Is(err, providerErr) {
		t.Fatalf("provider error = %v, want %v", err, providerErr)
	}
	currentManifest, err := os.ReadFile(filepath.Join(root, "example.toml"))
	if err != nil || !bytes.Equal(currentManifest, oldManifest) {
		t.Fatalf("pre-install callback/provider failure changed registration: %v", err)
	}

	validationErr := errors.New("post-install validation failed")
	archiveFile, provider := packageFileProvider(t, newArchive)
	result, err := EnsurePackage(context.Background(), cfg, "example", expectedEnsurePackage("example", "2.0.0", "new-source", newArchive), InstallOptions{}, EnsurePackageCallbacks{
		CurrentMatches: func(*DriverInfo) (bool, error) { return false, nil },
		Archive:        provider,
		ValidateResult: func(result EnsurePackageResult) error {
			if result.Manifest == nil || result.Installed == nil || result.Previous == nil {
				return errors.New("post-install validator received incomplete result")
			}
			return validationErr
		},
	})
	if !errors.Is(err, validationErr) || result.Manifest == nil || result.Installed == nil {
		t.Fatalf("post-install validation result=%#v error=%v", result, err)
	}
	installed, err := GetDriver(cfg, "example")
	if err != nil || installed.Version.String() != "2.0.0" {
		t.Fatalf("post-validation error rolled back committed install: version=%v error=%v", installed.Version, err)
	}
	release, err := acquireDriverInstallLock(filepath.Join(root), "example")
	if err != nil {
		t.Fatalf("driver lock remained held after validation error: %v", err)
	}
	release()
	if _, err := archiveFile.Stat(); err != nil {
		t.Fatalf("EnsurePackage closed caller archive after validation error: %v", err)
	}
	_ = archiveFile.Close()
}

func TestEnsurePackageRechecksRegistrationAfterAcquiringDriverLock(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	locks, err := func() (func(), error) {
		_, roots, err := resolvePackageInstallRoots(cfg)
		if err != nil {
			return nil, err
		}
		return acquireDriverInstallLocks(context.Background(), roots, "example")
	}()
	if err != nil {
		t.Fatal(err)
	}
	locksHeld := true
	defer func() {
		if locksHeld {
			locks()
		}
	}()
	archive := makeInstallArchive(t, "example", "1.0.0", "driver.so", []byte("library"))
	started := make(chan struct{})
	done := make(chan struct {
		result EnsurePackageResult
		err    error
	}, 1)
	providerCalls := 0
	go func() {
		close(started)
		result, err := EnsurePackage(context.Background(), cfg, "example", installExpected("example", "source", archive), InstallOptions{}, EnsurePackageCallbacks{
			CurrentMatches: func(current *DriverInfo) (bool, error) {
				return current != nil && current.Version.String() == "1.0.0", nil
			},
			Archive: func() (*os.File, error) {
				providerCalls++
				return nil, errors.New("provider must not run")
			},
		})
		done <- struct {
			result EnsurePackageResult
			err    error
		}{result, err}
	}()
	<-started
	time.Sleep(120 * time.Millisecond)
	latest := DriverInfo{ID: "example", Name: "Latest", Version: semver.MustParse("1.0.0"), Source: "latest"}
	latest.Driver.Shared.Set(PlatformTuple(), filepath.Join(root, "externally-managed.so"))
	if err := CreateManifest(cfg, latest); err != nil {
		t.Fatal(err)
	}
	locks()
	locksHeld = false
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if !result.result.Skipped || result.result.Current == nil || result.result.Current.Name != "Latest" || providerCalls != 0 {
			t.Fatalf("EnsurePackage did not recheck latest registration: result=%#v provider calls=%d", result.result, providerCalls)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("EnsurePackage did not finish after driver lock release")
	}
}

func TestEnsurePackageCancellationReleasesPartiallyAcquiredLocks(t *testing.T) {
	root := t.TempDir()
	early := filepath.Join(root, "a")
	late := filepath.Join(root, "z")
	if err := os.MkdirAll(early, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(late, 0o755); err != nil {
		t.Fatal(err)
	}
	// Reverse precedence relative to lock order: EnsurePackage should lock a
	// first, then wait for z. Holding z lets the test observe that a is already
	// acquired and that cancellation releases it.
	cfg := Config{Level: ConfigEnv, Location: late + string(os.PathListSeparator) + early}
	holdLate, err := acquireDriverInstallLock(late, "example")
	if err != nil {
		t.Fatal(err)
	}
	lateHeld := true
	defer func() {
		if lateHeld {
			holdLate()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	earlyLockAcquired := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		_, err := ensurePackageWithLockObserver(ctx, cfg, "example", ExpectedPackageMetadata{ID: "example"}, InstallOptions{}, EnsurePackageCallbacks{
			CurrentMatches: func(*DriverInfo) (bool, error) { return false, nil },
			Archive:        func() (*os.File, error) { return nil, errors.New("provider must not run") },
		}, func(root string) {
			if root == early {
				earlyLockAcquired <- struct{}{}
			}
		})
		done <- err
	}()
	select {
	case <-earlyLockAcquired:
	case <-time.After(5 * time.Second):
		t.Fatal("EnsurePackage did not acquire the earlier root lock")
	}
	contenderCtx, contenderCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	contenderRelease, contenderErr := acquireDriverInstallLockContext(contenderCtx, early, "example")
	contenderCancel()
	if contenderRelease != nil {
		contenderRelease()
	}
	if !errors.Is(contenderErr, context.DeadlineExceeded) {
		t.Fatalf("earlier root lock was not held after the acquisition observer signal: %v", contenderErr)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("EnsurePackage cancellation error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("EnsurePackage did not stop waiting after cancellation")
	}
	holdLate()
	lateHeld = false
	releaseEarly, err := acquireDriverInstallLockContext(context.Background(), early, "example")
	if err != nil {
		t.Fatalf("partial lock was not released after cancellation: %v", err)
	}
	releaseEarly()
}

func TestEnsurePackageCancellationWaitsForContextlessProviderAndInstall(t *testing.T) {
	t.Run("provider", func(t *testing.T) {
		root := t.TempDir()
		cfg := Config{Level: ConfigEnv, Location: root}
		oldArchive := makeInstallArchive(t, "example", "1.0.0", "old.so", []byte("old library"))
		installEnsureFixture(t, cfg, "1.0.0", "old", oldArchive)
		newArchive := makeInstallArchive(t, "example", "2.0.0", "new.so", []byte("new library"))
		providerArchive := writeInstallArchive(t, newArchive, "cancelled-provider")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		providerStarted := make(chan struct{})
		releaseProvider := make(chan struct{})
		var releaseOnce sync.Once
		unblockProvider := func() { releaseOnce.Do(func() { close(releaseProvider) }) }
		defer unblockProvider()
		done := make(chan error, 1)
		go func() {
			_, err := EnsurePackage(ctx, cfg, "example", expectedEnsurePackage("example", "2.0.0", "new", newArchive), InstallOptions{}, EnsurePackageCallbacks{
				CurrentMatches: func(*DriverInfo) (bool, error) { return false, nil },
				Archive: func() (*os.File, error) {
					close(providerStarted)
					<-releaseProvider
					return providerArchive, nil
				},
			})
			done <- err
		}()
		select {
		case <-providerStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("EnsurePackage did not enter provider")
		}
		cancel()
		select {
		case err := <-done:
			t.Fatalf("EnsurePackage returned before contextless provider finished: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		lockCtx, cancelLock := context.WithTimeout(context.Background(), 100*time.Millisecond)
		lockRelease, lockErr := acquireDriverInstallLockContext(lockCtx, root, "example")
		cancelLock()
		if lockRelease != nil {
			lockRelease()
		}
		if !errors.Is(lockErr, context.DeadlineExceeded) {
			t.Fatalf("driver lock was not held while provider was blocked: %v", lockErr)
		}
		if _, err := os.Stat(filepath.Join(root, "example.toml")); err != nil {
			t.Fatalf("provider cancellation changed the old registration: %v", err)
		}
		unblockProvider()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("provider cancellation error = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("EnsurePackage did not finish after provider returned")
		}
		current, err := GetDriver(cfg, "example")
		if err != nil || current.Version.String() != "1.0.0" {
			t.Fatalf("cancelled provider mutated registration: current=%#v error=%v", current, err)
		}
		release, err := acquireDriverInstallLock(root, "example")
		if err != nil {
			t.Fatalf("provider cancellation leaked driver lock: %v", err)
		}
		release()
		if _, err := providerArchive.Stat(); err != nil {
			t.Fatalf("EnsurePackage closed caller-owned archive: %v", err)
		}
		_ = providerArchive.Close()
	})

	t.Run("install", func(t *testing.T) {
		root := t.TempDir()
		cfg := Config{Level: ConfigEnv, Location: root}
		oldArchive := makeInstallArchive(t, "example", "1.0.0", "old.so", []byte("old library"))
		installEnsureFixture(t, cfg, "1.0.0", "old", oldArchive)
		newArchive := makeInstallArchive(t, "example", "2.0.0", "new.so", []byte("new library"))
		archiveFile := writeInstallArchive(t, newArchive, "cancelled-install")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		verifyStarted := make(chan struct{})
		releaseVerify := make(chan struct{})
		var releaseOnce sync.Once
		unblockVerify := func() { releaseOnce.Do(func() { close(releaseVerify) }) }
		defer unblockVerify()
		done := make(chan struct {
			result EnsurePackageResult
			err    error
		}, 1)
		go func() {
			result, err := EnsurePackage(ctx, cfg, "example", expectedEnsurePackage("example", "2.0.0", "new", newArchive), InstallOptions{
				Verify: func(string, Manifest) error {
					close(verifyStarted)
					<-releaseVerify
					return nil
				},
			}, EnsurePackageCallbacks{
				CurrentMatches: func(*DriverInfo) (bool, error) { return false, nil },
				Archive:        func() (*os.File, error) { return archiveFile, nil },
			})
			done <- struct {
				result EnsurePackageResult
				err    error
			}{result, err}
		}()
		select {
		case <-verifyStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("EnsurePackage did not enter install verification")
		}
		cancel()
		select {
		case result := <-done:
			t.Fatalf("EnsurePackage returned before contextless install finished: %#v", result)
		case <-time.After(100 * time.Millisecond):
		}
		lockCtx, cancelLock := context.WithTimeout(context.Background(), 100*time.Millisecond)
		lockRelease, lockErr := acquireDriverInstallLockContext(lockCtx, root, "example")
		cancelLock()
		if !errors.Is(lockErr, context.DeadlineExceeded) {
			t.Fatalf("driver lock was not held while install was blocked: %v", lockErr)
		}
		if lockRelease != nil {
			lockRelease()
		}
		unblockVerify()
		select {
		case result := <-done:
			if !errors.Is(result.err, context.Canceled) || result.result.Installed == nil || result.result.Installed.Version.String() != "2.0.0" {
				t.Fatalf("completed install cancellation result=%#v error=%v", result.result, result.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("EnsurePackage did not finish after install verifier returned")
		}
		current, err := GetDriver(cfg, "example")
		if err != nil || current.Version.String() != "2.0.0" {
			t.Fatalf("completed install was not retained after cancellation: current=%#v error=%v", current, err)
		}
		if _, err := archiveFile.Stat(); err != nil {
			t.Fatalf("EnsurePackage closed caller-owned archive: %v", err)
		}
		_ = archiveFile.Close()
	})
}

func TestEnsurePackageUsesConfigEnvPrecedenceAndExplicitlyRejectsMissingRoots(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	secondary := filepath.Join(root, "secondary")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(secondary, 0o755); err != nil {
		t.Fatal(err)
	}
	primaryArchive := makeInstallArchive(t, "example", "1.0.0", "primary.so", []byte("primary library"))
	secondaryArchive := makeInstallArchive(t, "example", "2.0.0", "secondary.so", []byte("secondary library"))
	installEnsureFixture(t, Config{Level: ConfigEnv, Location: primary}, "1.0.0", "primary", primaryArchive)
	installEnsureFixture(t, Config{Level: ConfigEnv, Location: secondary}, "2.0.0", "secondary", secondaryArchive)
	combined := Config{Level: ConfigEnv, Location: primary + string(os.PathListSeparator) + filepath.Join(primary, ".", "..", "primary") + string(os.PathListSeparator) + secondary}
	_, roots, err := resolvePackageInstallRoots(combined)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || roots[0] != primary || roots[1] != secondary {
		t.Fatalf("resolved ConfigEnv roots = %#v, want deduplicated precedence [%s %s]", roots, primary, secondary)
	}
	providerCalls := 0
	result, err := EnsurePackage(context.Background(), combined, "example", installExpected("example", "primary", primaryArchive), InstallOptions{}, EnsurePackageCallbacks{
		CurrentMatches: func(current *DriverInfo) (bool, error) {
			return current != nil && current.FilePath == primary && current.Version.String() == "1.0.0", nil
		},
		Archive: func() (*os.File, error) {
			providerCalls++
			return nil, errors.New("provider must not run")
		},
	})
	if err != nil || !result.Skipped || providerCalls != 0 || result.Current.FilePath != primary {
		t.Fatalf("ConfigEnv precedence result=%#v provider calls=%d error=%v", result, providerCalls, err)
	}

	missing := filepath.Join(root, "missing")
	_, err = EnsurePackage(context.Background(), Config{Level: ConfigEnv, Location: primary + string(os.PathListSeparator) + missing}, "example", installExpected("example", "primary", primaryArchive), InstallOptions{}, EnsurePackageCallbacks{
		CurrentMatches: func(*DriverInfo) (bool, error) { return true, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "configured driver config root") {
		t.Fatalf("missing secondary ConfigEnv root was silently ignored: %v", err)
	}
}

func TestEnsurePackageLocksAgainstConcurrentUninstall(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	oldArchive := makeInstallArchive(t, "example", "1.0.0", "old.so", []byte("old library"))
	oldInfo := installEnsureFixture(t, cfg, "1.0.0", "old", oldArchive)
	newArchive := makeInstallArchive(t, "example", "2.0.0", "new.so", []byte("new library"))
	archiveFile := writeInstallArchive(t, newArchive, "blocked-provider")
	providerStarted := make(chan struct{})
	releaseProvider := make(chan struct{})
	var releaseOnce sync.Once
	unblockProvider := func() { releaseOnce.Do(func() { close(releaseProvider) }) }
	defer unblockProvider()
	ensureDone := make(chan struct {
		result EnsurePackageResult
		err    error
	}, 1)
	go func() {
		result, err := EnsurePackage(context.Background(), cfg, "example", expectedEnsurePackage("example", "2.0.0", "new", newArchive), InstallOptions{}, EnsurePackageCallbacks{
			CurrentMatches: func(*DriverInfo) (bool, error) { return false, nil },
			Archive: func() (*os.File, error) {
				close(providerStarted)
				<-releaseProvider
				return archiveFile, nil
			},
		})
		ensureDone <- struct {
			result EnsurePackageResult
			err    error
		}{result, err}
	}()
	select {
	case <-providerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("EnsurePackage did not reach archive provider")
	}
	uninstallStarted := make(chan struct{})
	uninstallDone := make(chan error, 1)
	go func() {
		close(uninstallStarted)
		uninstallDone <- UninstallDriver(cfg, oldInfo)
	}()
	<-uninstallStarted
	select {
	case err := <-uninstallDone:
		t.Fatalf("uninstall escaped EnsurePackage driver lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	unblockProvider()
	select {
	case result := <-ensureDone:
		if result.err != nil || result.result.Installed == nil || result.result.Installed.Version.String() != "2.0.0" {
			t.Fatalf("EnsurePackage result=%#v error=%v", result.result, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("EnsurePackage did not finish after archive provider returned")
	}
	select {
	case err := <-uninstallDone:
		if err == nil || !strings.Contains(err.Error(), "registration changed before uninstall") {
			t.Fatalf("uninstall result = %v, want stale registration rejection", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("uninstall did not resume after EnsurePackage released its lock")
	}
	current, err := GetDriver(cfg, "example")
	if err != nil || current.Version.String() != "2.0.0" {
		t.Fatalf("concurrent uninstall removed ensured package: current=%#v err=%v", current, err)
	}
	if _, err := archiveFile.Stat(); err != nil {
		t.Fatalf("EnsurePackage closed caller-owned archive: %v", err)
	}
	_ = archiveFile.Close()
}

func TestEnsurePackageSerializesConcurrentInstall(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	oldArchive := makeInstallArchive(t, "example", "1.0.0", "old.so", []byte("old library"))
	installEnsureFixture(t, cfg, "1.0.0", "old", oldArchive)
	ensuredArchive := makeInstallArchive(t, "example", "2.0.0", "ensured.so", []byte("ensured library"))
	ensuredFile := writeInstallArchive(t, ensuredArchive, "ensure-provider")
	concurrentArchive := makeInstallArchive(t, "example", "3.0.0", "concurrent.so", []byte("concurrent library"))
	concurrentFile := writeInstallArchive(t, concurrentArchive, "concurrent-install")
	providerStarted := make(chan struct{})
	releaseProvider := make(chan struct{})
	var releaseOnce sync.Once
	unblockProvider := func() { releaseOnce.Do(func() { close(releaseProvider) }) }
	defer unblockProvider()
	ensureDone := make(chan error, 1)
	go func() {
		_, err := EnsurePackage(context.Background(), cfg, "example", expectedEnsurePackage("example", "2.0.0", "ensured", ensuredArchive), InstallOptions{}, EnsurePackageCallbacks{
			CurrentMatches: func(*DriverInfo) (bool, error) { return false, nil },
			Archive: func() (*os.File, error) {
				close(providerStarted)
				<-releaseProvider
				return ensuredFile, nil
			},
		})
		ensureDone <- err
	}()
	select {
	case <-providerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("EnsurePackage did not reach archive provider")
	}
	installStarted := make(chan struct{})
	installDone := make(chan error, 1)
	go func() {
		close(installStarted)
		_, err := InstallPackage(cfg, "example", concurrentFile, expectedEnsurePackage("example", "3.0.0", "concurrent", concurrentArchive), InstallOptions{})
		installDone <- err
	}()
	<-installStarted
	select {
	case err := <-installDone:
		t.Fatalf("concurrent install escaped EnsurePackage driver lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	unblockProvider()
	select {
	case err := <-ensureDone:
		if err != nil {
			t.Fatalf("EnsurePackage failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("EnsurePackage did not finish after provider returned")
	}
	select {
	case err := <-installDone:
		if err != nil {
			t.Fatalf("concurrent InstallPackage failed after lock release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent InstallPackage did not resume after EnsurePackage released its lock")
	}
	current, err := GetDriver(cfg, "example")
	if err != nil || current.Version.String() != "3.0.0" {
		t.Fatalf("concurrent install was not serialized after EnsurePackage: current=%#v error=%v", current, err)
	}
	_ = ensuredFile.Close()
	_ = concurrentFile.Close()
}

func TestPackageInstallRootKeyCaseSensitivity(t *testing.T) {
	first := filepath.Clean("C:\\Drivers\\Primary")
	second := filepath.Clean("c:\\drivers\\primary")
	if packageInstallRootKey(first, true) != packageInstallRootKey(second, true) {
		t.Fatalf("Windows root keys differ for case variants: %q and %q", packageInstallRootKey(first, true), packageInstallRootKey(second, true))
	}
	if packageInstallRootKey(first, false) == packageInstallRootKey(second, false) {
		t.Fatal("case-sensitive root key unexpectedly folded case")
	}
}
