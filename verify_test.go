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

package dbc_test

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/stretchr/testify/require"
)

const (
	signedDriver    = "test-driver-1-not-valid.so"
	signedSignature = "test-driver-1-not-valid.so.sig"
)

// extractSignedPackage writes the validly signed testdata driver and its
// signature into dir.
func extractSignedPackage(t *testing.T, dir string) {
	t.Helper()
	f, err := os.Open(filepath.Join("cmd", "dbc", "testdata", "test-driver-1.tar.gz"))
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return
		}
		require.NoError(t, err)
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, hdr.Name), data, 0o600))
	}
}

func TestVerifyPackageSignature(t *testing.T) {
	stage := t.TempDir()
	extractSignedPackage(t, stage)
	m := config.Manifest{}
	m.Files.Driver = signedDriver
	require.NoError(t, dbc.VerifyPackageSignature(stage, m), "the default <driver>.sig signature")
	m.Files.Signature = signedSignature
	require.NoError(t, dbc.VerifyPackageSignature(stage, m), "an explicit signature")
}

// VerifyPackageSignature is exported, so it can't rely on the package
// extractor having validated the manifest: a validly signed file outside the
// staging directory must not pass for the package's driver or its signature.
func TestVerifyPackageSignatureStaysInStagingDir(t *testing.T) {
	outside := t.TempDir()
	extractSignedPackage(t, outside)
	stage := filepath.Join(outside, "stage")
	require.NoError(t, os.Mkdir(stage, 0o700))
	// The driver itself is legitimately staged, so the signature cases fail
	// only if the signature is looked up inside the stage.
	driver, err := os.ReadFile(filepath.Join(outside, signedDriver))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(stage, signedDriver), driver, 0o600))

	for name, files := range map[string][2]string{
		"relative driver and signature": {"../" + signedDriver, "../" + signedSignature},
		"absolute driver and signature": {filepath.Join(outside, signedDriver), filepath.Join(outside, signedSignature)},
		"relative signature":            {signedDriver, "../" + signedSignature},
		"absolute signature":            {signedDriver, filepath.Join(outside, signedSignature)},
	} {
		t.Run(name, func(t *testing.T) {
			m := config.Manifest{}
			m.Files.Driver, m.Files.Signature = files[0], files[1]
			require.Error(t, dbc.VerifyPackageSignature(stage, m))
		})
	}

	symlink := func(t *testing.T, target, link string) {
		t.Helper()
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	t.Run("symlinked driver", func(t *testing.T) {
		link := filepath.Join(stage, "linked.so")
		symlink(t, filepath.Join(outside, signedDriver), link)
		symlink(t, filepath.Join(outside, signedSignature), link+".sig")
		m := config.Manifest{}
		m.Files.Driver = "linked.so"
		require.Error(t, dbc.VerifyPackageSignature(stage, m))
	})
	t.Run("symlinked signature", func(t *testing.T) {
		symlink(t, filepath.Join(outside, signedSignature), filepath.Join(stage, "linked.sig"))
		m := config.Manifest{}
		m.Files.Driver, m.Files.Signature = signedDriver, "linked.sig"
		require.Error(t, dbc.VerifyPackageSignature(stage, m))
	})
}
