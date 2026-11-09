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
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fileSHA256(t *testing.T, f *os.File) string {
	t.Helper()
	data, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func installedReceipt(t *testing.T, cfg Config) packageInstallReceipt {
	t.Helper()
	registered, err := GetDriver(cfg, "driver")
	require.NoError(t, err)
	generationDir := filepath.Dir(registered.Driver.Shared.Get(PlatformTuple()))
	receipt, ok := readPackageInstallReceipt(cfg.Location, generationDir)
	require.True(t, ok, "receipt should be valid")
	return receipt
}

func TestInstallPackageArchiveHash(t *testing.T) {
	ctx := context.Background()

	t.Run("matching expected hash records the measured digest", func(t *testing.T) {
		cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
		archive := testPackageArchive(t, "new")
		digest := fileSHA256(t, archive)
		_, err := InstallPackage(ctx, cfg, "driver", archive, InstallPackageOptions{ExpectedArchiveSHA256: digest})
		require.NoError(t, err)
		assert.Equal(t, digest, installedReceipt(t, cfg).ArchiveSHA256)
	})

	t.Run("no expected hash still records the measured digest", func(t *testing.T) {
		cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
		archive := testPackageArchive(t, "new")
		digest := fileSHA256(t, archive)
		_, err := InstallPackage(ctx, cfg, "driver", archive, InstallPackageOptions{})
		require.NoError(t, err)
		receipt := installedReceipt(t, cfg)
		assert.Equal(t, digest, receipt.ArchiveSHA256)
		assert.False(t, receipt.SignatureVerified)
	})

	t.Run("mismatch fails before publication and keeps the previous install", func(t *testing.T) {
		cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
		installInitialPackage(t, cfg)
		previous, err := GetDriver(cfg, "driver")
		require.NoError(t, err)
		previousPath := previous.Driver.Shared.Get(PlatformTuple())

		archive := testPackageArchive(t, "new")
		actual := fileSHA256(t, archive)
		expected := strings.Repeat("0", 64)
		verifierRan := false
		_, err = InstallPackage(ctx, cfg, "driver", archive, InstallPackageOptions{
			ExpectedArchiveSHA256: expected,
			Verifier:              func(string, Manifest) error { verifierRan = true; return nil },
		})
		var mismatch *ArchiveHashMismatchError
		require.ErrorAs(t, err, &mismatch)
		assert.Equal(t, expected, mismatch.Expected)
		assert.Equal(t, actual, mismatch.Actual)
		assert.Contains(t, err.Error(), "archive SHA-256 mismatch")
		assert.False(t, verifierRan)
		assertArchiveClosed(t, archive)
		assertPreviousPackage(t, cfg, previousPath)
		assertOnlyPackageGeneration(t, cfg.Location, filepath.Base(filepath.Dir(previousPath)))
	})

	t.Run("hash mismatch takes precedence over malformed archives", func(t *testing.T) {
		cases := []struct {
			name    string
			corrupt func(*testing.T, *os.File)
		}{
			{
				name: "truncated archive",
				corrupt: func(t *testing.T, archive *os.File) {
					t.Helper()
					require.NoError(t, os.Truncate(archive.Name(), 8))
				},
			},
			{
				name: "HTML response",
				corrupt: func(t *testing.T, archive *os.File) {
					t.Helper()
					require.NoError(t, os.WriteFile(archive.Name(), []byte("<html>download failed</html>"), 0o600))
				},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
				installInitialPackage(t, cfg)
				previous, err := GetDriver(cfg, "driver")
				require.NoError(t, err)
				previousPath := previous.Driver.Shared.Get(PlatformTuple())

				archive := testPackageArchive(t, "new")
				tc.corrupt(t, archive)
				actual := fileSHA256(t, archive)
				expected := strings.Repeat("0", 64)
				require.NotEqual(t, expected, actual)

				_, err = InstallPackage(ctx, cfg, "driver", archive, InstallPackageOptions{ExpectedArchiveSHA256: expected})
				var mismatch *ArchiveHashMismatchError
				require.ErrorAs(t, err, &mismatch)
				assert.Equal(t, expected, mismatch.Expected)
				assert.Equal(t, actual, mismatch.Actual)
				assertArchiveClosed(t, archive)
				assertPreviousPackage(t, cfg, previousPath)
				assertOnlyPackageGeneration(t, cfg.Location, filepath.Base(filepath.Dir(previousPath)))
			})
		}
	})

	t.Run("matching corrupted archive digest reports extraction error", func(t *testing.T) {
		cases := []struct {
			name    string
			corrupt func(*testing.T, *os.File)
		}{
			{
				name: "truncated archive",
				corrupt: func(t *testing.T, archive *os.File) {
					t.Helper()
					require.NoError(t, os.Truncate(archive.Name(), 8))
				},
			},
			{
				name: "HTML response",
				corrupt: func(t *testing.T, archive *os.File) {
					t.Helper()
					require.NoError(t, os.WriteFile(archive.Name(), []byte("<html>download failed</html>"), 0o600))
				},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
				installInitialPackage(t, cfg)
				previous, err := GetDriver(cfg, "driver")
				require.NoError(t, err)
				previousPath := previous.Driver.Shared.Get(PlatformTuple())

				archive := testPackageArchive(t, "new")
				tc.corrupt(t, archive)
				digest := fileSHA256(t, archive)

				_, err = InstallPackage(ctx, cfg, "driver", archive, InstallPackageOptions{ExpectedArchiveSHA256: digest})
				require.Error(t, err)
				var mismatch *ArchiveHashMismatchError
				assert.False(t, errors.As(err, &mismatch), "matching archive digest should reach extraction")
				assert.Contains(t, err.Error(), "failed to extract package")
				assertArchiveClosed(t, archive)
				assertPreviousPackage(t, cfg, previousPath)
				assertOnlyPackageGeneration(t, cfg.Location, filepath.Base(filepath.Dir(previousPath)))
			})
		}
	})

	t.Run("digest covers bytes after the first gzip member", func(t *testing.T) {
		cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
		archive := testPackageArchive(t, "new")
		appended, err := os.OpenFile(archive.Name(), os.O_APPEND|os.O_WRONLY, 0)
		require.NoError(t, err)
		gz := gzip.NewWriter(appended)
		require.NoError(t, gz.Close())
		require.NoError(t, appended.Close())
		whole := fileSHA256(t, archive)

		_, err = InstallPackage(ctx, cfg, "driver", archive, InstallPackageOptions{ExpectedArchiveSHA256: whole})
		require.NoError(t, err)
		assert.Equal(t, whole, installedReceipt(t, cfg).ArchiveSHA256)
	})

	t.Run("malformed expected hash is rejected", func(t *testing.T) {
		for _, bad := range []string{"abc", strings.Repeat("A", 64), strings.Repeat("g", 64), "sha256:" + strings.Repeat("0", 64)} {
			cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
			archive := testPackageArchive(t, "new")
			_, err := InstallPackage(ctx, cfg, "driver", archive, InstallPackageOptions{ExpectedArchiveSHA256: bad})
			require.Error(t, err, bad)
			var mismatch *ArchiveHashMismatchError
			assert.False(t, errors.As(err, &mismatch))
			assertArchiveClosed(t, archive)
			_, err = GetDriver(cfg, "driver")
			assert.Error(t, err)
		}
	})
}

func TestInstallPackageSignatureEvidence(t *testing.T) {
	ctx := context.Background()
	ok := func(string, Manifest) error { return nil }

	cases := []struct {
		name string
		opts InstallPackageOptions
		want bool
	}{
		{"no verifiers", InstallPackageOptions{}, false},
		{"only Verifier", InstallPackageOptions{Verifier: ok}, false},
		{"SignatureVerifier", InstallPackageOptions{SignatureVerifier: ok}, true},
		{"both", InstallPackageOptions{Verifier: ok, SignatureVerifier: ok}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
			_, err := InstallPackage(ctx, cfg, "driver", testPackageArchive(t, "new"), tc.opts)
			require.NoError(t, err)
			assert.Equal(t, tc.want, installedReceipt(t, cfg).SignatureVerified)
		})
	}

	t.Run("SignatureVerifier failure keeps the previous install", func(t *testing.T) {
		cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
		installInitialPackage(t, cfg)
		previous, err := GetDriver(cfg, "driver")
		require.NoError(t, err)
		previousPath := previous.Driver.Shared.Get(PlatformTuple())

		archive := testPackageArchive(t, "new")
		_, err = InstallPackage(ctx, cfg, "driver", archive, InstallPackageOptions{
			SignatureVerifier: func(string, Manifest) error { return errors.New("bad signature") },
		})
		require.ErrorContains(t, err, "bad signature")
		assertArchiveClosed(t, archive)
		assertPreviousPackage(t, cfg, previousPath)
	})
}

func TestPackageInstallReceiptArchiveSHA256Validation(t *testing.T) {
	cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
	_, err := InstallPackage(context.Background(), cfg, "driver", testPackageArchive(t, "new"), InstallPackageOptions{})
	require.NoError(t, err)
	receipt := installedReceipt(t, cfg)

	assert.True(t, validPackageInstallReceiptForGeneration(receipt, receipt.Generation))

	empty := receipt
	empty.ArchiveSHA256 = ""
	assert.True(t, validPackageInstallReceiptForGeneration(empty, receipt.Generation))

	for _, bad := range []string{"abc", strings.ToUpper(receipt.ArchiveSHA256), "sha256:" + receipt.ArchiveSHA256, strings.Repeat("z", 64)} {
		invalid := receipt
		invalid.ArchiveSHA256 = bad
		assert.False(t, validPackageInstallReceiptForGeneration(invalid, receipt.Generation), bad)
	}
}
