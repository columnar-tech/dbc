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

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"github.com/columnar-tech/dbc"
)

// signedArchiveFile writes a package for test-driver-no-sig whose library
// is signed with a new key, and returns its path and the key.
func (suite *SubcommandTestSuite) signedArchiveFile() (string, *dbc.SigningKey) {
	key, err := crypto.PGP().KeyGeneration().AddUserId("acme", "acme@example.invalid").New().GenerateKey()
	suite.Require().NoError(err)
	pub, err := key.ToPublic()
	suite.Require().NoError(err)
	armored, err := pub.Armor()
	suite.Require().NoError(err)
	signingKey, err := dbc.NewSigningKey([]byte(armored))
	suite.Require().NoError(err)

	lib := []byte("library signed by acme")
	signer, err := crypto.PGP().Sign().SigningKey(key).Detached().New()
	suite.Require().NoError(err)
	sig, err := signer.Sign(lib, crypto.Armor)
	suite.Require().NoError(err)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		body []byte
	}{
		{"MANIFEST", []byte("name = 'Test Driver No Sig'\nversion = '1.1.0'\n\n[Files]\ndriver = 'acme.so'\nsignature = 'acme.so.sig'\n")},
		{"acme.so", lib},
		{"acme.so.sig", sig},
	} {
		suite.Require().NoError(tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write(f.body)
		suite.Require().NoError(err)
	}
	suite.Require().NoError(tw.Close())
	suite.Require().NoError(gz.Close())

	path := filepath.Join(suite.T().TempDir(), "acme.tar.gz")
	suite.Require().NoError(os.WriteFile(path, buf.Bytes(), 0o600))
	return path, signingKey
}

// signedBase is a base model whose only registry has the given signing
// policy. Every download returns a copy of archive.
func signedBase(policy dbc.RegistrySigningPolicy, archive string) baseModel {
	registry := testRegistry
	registry.Signing = policy
	index := func() ([]dbc.Driver, error) {
		drivers, err := getTestDriverRegistry()
		for i := range drivers {
			drivers[i].Registry = &registry
		}
		return drivers, err
	}
	download := func(dbc.PkgInfo) (*os.File, error) {
		data, err := os.ReadFile(archive)
		if err != nil {
			return nil, err
		}
		dir, err := os.MkdirTemp("", "adbc-drivers-test-*")
		if err != nil {
			return nil, err
		}
		p := filepath.Join(dir, filepath.Base(archive))
		if err := os.WriteFile(p, data, 0o600); err != nil {
			return nil, err
		}
		return os.Open(p)
	}
	return baseModel{
		getDriverRegistry: index,
		getRegistries:     func() ([]dbc.Registry, error) { return []dbc.Registry{registry}, nil },
		getRegistryIndex:  func(dbc.Registry) ([]dbc.Driver, error) { return index() },
		downloadPkg:       download,
	}
}

// assertSignedRoundTrip locks and syncs test-driver-no-sig from a registry
// with policy, syncs again without downloading, and then checks that the
// lock is not replayed once the registry is configured with changed.
func (suite *SubcommandTestSuite) assertSignedRoundTrip(policy, changed dbc.RegistrySigningPolicy, archive string, want lockedSigner) {
	listPath := suite.writeDriverList("test-driver-no-sig")

	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(signedBase(policy, archive)))
	suite.Equal(want, suite.lockedDriverEntry("test-driver-no-sig").Signer)

	out := suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(signedBase(policy, archive)))
	suite.driverIsInstalled("test-driver-no-sig", false)
	suite.Contains(out, fmt.Sprintf(`"signature_verified":%t`, !policy.IsUnsigned()))

	var reuse syncCounters
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(reuse.counted(signedBase(policy, archive))))
	suite.Zero(reuse.downloads, "an installed driver is reused")

	lock := suite.readLockBytes()
	var rejected syncCounters
	out = suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel, NoVerify: true}.GetModelCustom(rejected.counted(signedBase(changed, archive))))
	suite.Contains(out, "dbc lock")
	suite.Zero(rejected.downloads)
	suite.Equal(lock, suite.readLockBytes(), "the lock file must stay untouched")
}

func (suite *SubcommandTestSuite) TestSyncWithCustomSigningKey() {
	archive, key := suite.signedArchiveFile()
	suite.assertSignedRoundTrip(dbc.SignWith(key), dbc.RegistrySigningPolicy{}, archive,
		lockedSigner{Type: lockSignerOpenPGP, Fingerprint: key.Fingerprint()})
}

func (suite *SubcommandTestSuite) TestSyncWithUnsignedRegistry() {
	archive, key := suite.signedArchiveFile()
	suite.assertSignedRoundTrip(dbc.Unsigned(), dbc.SignWith(key), archive, lockedSigner{Type: lockSignerNone})
}
