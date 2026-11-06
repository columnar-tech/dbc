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
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reuseTreeSnapshot records every non-lock entry under root with its contents.
func reuseTreeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, ".dbc") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if entry.IsDir() {
			snapshot[rel] = "dir"
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		snapshot[rel] = string(data)
		return nil
	})
	require.NoError(t, err)
	return snapshot
}

func installReusePackage(t *testing.T, cfg Config, options InstallPackageOptions) (digest string) {
	t.Helper()
	archive := testPackageArchive(t, "new")
	digest = fileSHA256(t, archive)
	options.ExpectedArchiveSHA256 = digest
	_, err := InstallPackage(context.Background(), cfg, "driver", archive, options)
	require.NoError(t, err)
	return digest
}

func reuseExpectation(digest string) InstalledPackageExpectation {
	return InstalledPackageExpectation{Version: semver.MustParse("1.0.0"), ArchiveSHA256: digest}
}

func assertNotReusable(t *testing.T, cfg Config, expect InstalledPackageExpectation) {
	t.Helper()
	before := reuseTreeSnapshot(t, cfg.Location)
	info, ok, err := CheckInstalledPackage(context.Background(), cfg, "driver", expect)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, DriverInfo{}, info)
	assert.Equal(t, before, reuseTreeSnapshot(t, cfg.Location), "check must not modify anything")
}

func TestCheckInstalledPackageMatches(t *testing.T) {
	requireReuseSupported(t)
	ok := func(string, Manifest) error { return nil }
	cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
	digest := installReusePackage(t, cfg, InstallPackageOptions{SignatureVerifier: ok})
	before := reuseTreeSnapshot(t, cfg.Location)

	expect := reuseExpectation(digest)
	info, matched, err := CheckInstalledPackage(context.Background(), cfg, "driver", expect)
	require.NoError(t, err)
	require.True(t, matched)
	registered, err := GetDriver(cfg, "driver")
	require.NoError(t, err)
	// The check reads the registration from its symlink-resolved location
	// (for example /private/var on macOS or a long path on Windows), so
	// compare locations by the files they resolve to.
	assert.True(t, sameResolvedFilesystemPath(registered.FilePath, info.FilePath))
	shared := PlatformTuple()
	assert.True(t, sameResolvedFilesystemPath(registered.Driver.Shared.Get(shared), info.Driver.Shared.Get(shared)))
	registered.FilePath, registered.Driver.Shared = info.FilePath, info.Driver.Shared
	assert.True(t, sameDriverRegistration(registered, info))

	expect.RequireSignatureVerified = true
	_, matched, err = CheckInstalledPackage(context.Background(), cfg, "driver", expect)
	require.NoError(t, err)
	assert.True(t, matched)
	assert.Equal(t, before, reuseTreeSnapshot(t, cfg.Location))
}

func TestCheckInstalledPackageRejectsMismatches(t *testing.T) {
	t.Run("different version", func(t *testing.T) {
		cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
		expect := reuseExpectation(installReusePackage(t, cfg, InstallPackageOptions{}))
		expect.Version = semver.MustParse("1.0.1")
		assertNotReusable(t, cfg, expect)
	})
	t.Run("different digest", func(t *testing.T) {
		cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
		installReusePackage(t, cfg, InstallPackageOptions{})
		assertNotReusable(t, cfg, reuseExpectation(strings.Repeat("0", 64)))
	})
	t.Run("signature required but not verified", func(t *testing.T) {
		cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
		expect := reuseExpectation(installReusePackage(t, cfg, InstallPackageOptions{}))
		expect.RequireSignatureVerified = true
		assertNotReusable(t, cfg, expect)
	})
	t.Run("library bytes modified", func(t *testing.T) {
		cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
		expect := reuseExpectation(installReusePackage(t, cfg, InstallPackageOptions{}))
		registered, err := GetDriver(cfg, "driver")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(registered.Driver.Shared.Get(PlatformTuple()), []byte("tampered"), 0o755))
		assertNotReusable(t, cfg, expect)
	})
	t.Run("library missing", func(t *testing.T) {
		cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
		expect := reuseExpectation(installReusePackage(t, cfg, InstallPackageOptions{}))
		registered, err := GetDriver(cfg, "driver")
		require.NoError(t, err)
		require.NoError(t, os.Remove(registered.Driver.Shared.Get(PlatformTuple())))
		assertNotReusable(t, cfg, expect)
	})
	t.Run("receipt removed", func(t *testing.T) {
		cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
		expect := reuseExpectation(installReusePackage(t, cfg, InstallPackageOptions{}))
		registered, err := GetDriver(cfg, "driver")
		require.NoError(t, err)
		generation := filepath.Dir(registered.Driver.Shared.Get(PlatformTuple()))
		require.NoError(t, os.Remove(filepath.Join(generation, packageInstallReceiptFilename)))
		assertNotReusable(t, cfg, expect)
	})
	t.Run("receipt corrupted", func(t *testing.T) {
		cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
		expect := reuseExpectation(installReusePackage(t, cfg, InstallPackageOptions{}))
		registered, err := GetDriver(cfg, "driver")
		require.NoError(t, err)
		generation := filepath.Dir(registered.Driver.Shared.Get(PlatformTuple()))
		require.NoError(t, os.WriteFile(filepath.Join(generation, packageInstallReceiptFilename), []byte("{"), 0o600))
		assertNotReusable(t, cfg, expect)
	})
	t.Run("registration points at external library", func(t *testing.T) {
		for _, source := range []string{"", "dbc"} {
			cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
			expect := reuseExpectation(installReusePackage(t, cfg, InstallPackageOptions{}))
			external := filepath.Join(t.TempDir(), "external.so")
			require.NoError(t, os.WriteFile(external, []byte("external"), 0o755))
			info := DriverInfo{ID: "driver", Source: source, Version: semver.MustParse("1.0.0")}
			info.Driver.Shared.Set(PlatformTuple(), external)
			require.NoError(t, createDriverManifestUnlocked(cfg.Location, info))
			assertNotReusable(t, cfg, expect)
		}
	})
	t.Run("legacy layout without receipt", func(t *testing.T) {
		cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
		library := filepath.Join(cfg.Location, "driver.so")
		require.NoError(t, os.WriteFile(library, []byte("legacy"), 0o755))
		info := DriverInfo{ID: "driver", Source: "dbc", Version: semver.MustParse("1.0.0")}
		info.Driver.Shared.Set(PlatformTuple(), library)
		require.NoError(t, createDriverManifestUnlocked(cfg.Location, info))
		assertNotReusable(t, cfg, reuseExpectation(strings.Repeat("a", 64)))
	})
	t.Run("not installed", func(t *testing.T) {
		cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
		assertNotReusable(t, cfg, reuseExpectation(strings.Repeat("a", 64)))
	})
	t.Run("location does not exist", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "missing")
		cfg := Config{Level: ConfigEnv, Location: location}
		_, matched, err := CheckInstalledPackage(context.Background(), cfg, "driver", reuseExpectation(strings.Repeat("a", 64)))
		require.NoError(t, err)
		assert.False(t, matched)
		_, statErr := os.Stat(location)
		assert.ErrorIs(t, statErr, fs.ErrNotExist)
	})
}

func TestCheckInstalledPackageRejectsSingleReceiptFieldMismatch(t *testing.T) {
	// Each case rewrites exactly one receipt field to another well-formed
	// value. runtime_id and generation also fail the reader's own validation
	// against the generation directory name, so those cases are rejected
	// before the explicit field comparison is reached.
	otherPlatform := "linux_arm64"
	if PlatformTuple() == otherPlatform {
		otherPlatform = "linux_amd64"
	}
	cases := map[string]func(current any) any{
		"runtime_id": func(any) any { return "other" },
		"platform":   func(any) any { return otherPlatform },
		"registration_scope": func(current any) any {
			if current == string(packageRegistrationFile) {
				return string(packageRegistrationRegistryUser)
			}
			return string(packageRegistrationFile)
		},
		"generation":               func(any) any { return "other-generation" },
		"registration_fingerprint": func(any) any { return strings.Repeat("0", 64) },
	}
	for field, change := range cases {
		t.Run(field, func(t *testing.T) {
			cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
			expect := reuseExpectation(installReusePackage(t, cfg, InstallPackageOptions{}))
			registered, err := GetDriver(cfg, "driver")
			require.NoError(t, err)
			generation := filepath.Dir(registered.Driver.Shared.Get(PlatformTuple()))
			receiptPath := filepath.Join(generation, packageInstallReceiptFilename)
			data, err := os.ReadFile(receiptPath)
			require.NoError(t, err)
			var receipt map[string]any
			require.NoError(t, json.Unmarshal(data, &receipt))
			require.Contains(t, receipt, field)
			changed := change(receipt[field])
			require.NotEqual(t, receipt[field], changed)
			receipt[field] = changed
			data, err = json.Marshal(receipt)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(receiptPath, data, 0o600))
			assertNotReusable(t, cfg, expect)
		})
	}
}

func TestCheckInstalledPackageInvalidInput(t *testing.T) {
	cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
	valid := reuseExpectation(strings.Repeat("a", 64))

	for _, digest := range []string{"", "abc", strings.Repeat("A", 64), "sha256:" + strings.Repeat("a", 64)} {
		expect := valid
		expect.ArchiveSHA256 = digest
		_, matched, err := CheckInstalledPackage(context.Background(), cfg, "driver", expect)
		require.Error(t, err, digest)
		assert.False(t, matched)
	}
	nilVersion := valid
	nilVersion.Version = nil
	_, _, err := CheckInstalledPackage(context.Background(), cfg, "driver", nilVersion)
	require.Error(t, err)
	for _, id := range []string{"", "../driver", "a/b"} {
		_, _, err := CheckInstalledPackage(context.Background(), cfg, id, valid)
		require.Error(t, err, id)
	}
}

func TestCheckInstalledPackageCanceledWhileLockHeld(t *testing.T) {
	requireReuseSupported(t)
	cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
	expect := reuseExpectation(installReusePackage(t, cfg, InstallPackageOptions{}))
	lock, err := acquireDriverInstallLockWith(context.Background(), cfg.Location, "driver", time.Second)
	require.NoError(t, err)
	released := false
	defer func() {
		if !released {
			_ = lock.release()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, checkErr := CheckInstalledPackage(ctx, cfg, "driver", expect)
		done <- checkErr
	}()
	select {
	case err := <-done:
		t.Fatalf("check returned while the driver lock was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("check did not return after cancellation")
	}

	require.NoError(t, lock.release())
	released = true
	_, matched, err := CheckInstalledPackage(context.Background(), cfg, "driver", expect)
	require.NoError(t, err)
	assert.True(t, matched)
}

// requireReuseSupported skips tests that expect a match on platforms where
// CheckInstalledPackage always reports no match.
func requireReuseSupported(t *testing.T) {
	t.Helper()
	if !supportsPinnedCleanup() {
		t.Skip("installed package reuse requires pinned directory handles")
	}
}
