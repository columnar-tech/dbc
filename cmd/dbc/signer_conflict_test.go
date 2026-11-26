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
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc"
)

// signerRegistries serves test-driver-no-sig, whose package is signed with
// the key of signedArchiveFile, from two registries with the given signing
// policies. With missingSecond, the merged index lacks the second registry,
// as when its index cannot be fetched.
type signerRegistries struct {
	*twoRegistries
	archive       string
	missingSecond bool
	downloads     int
}

// newSignerRegistries returns registries with the Columnar key, the key
// that signs their package and its armored form.
func (suite *SubcommandTestSuite) newSignerRegistries() (*signerRegistries, *dbc.SigningKey, string) {
	archive, key, armored := suite.signedArchive()
	return &signerRegistries{twoRegistries: newTwoRegistries(), archive: archive}, key, armored
}

// commandModel is a command that runs with a custom base model.
type commandModel interface {
	GetModelCustom(baseModel) tea.Model
}

func (r *signerRegistries) base() baseModel {
	base := r.twoRegistries.base()
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		r.merged++
		drivers, err := r.index(&r.first, true)
		if err != nil {
			return nil, err
		}
		if r.missingSecond {
			return drivers, &dbc.RegistryIndexError{Registry: &r.second, Err: dbc.ErrUnauthorized}
		}
		second, err := r.index(&r.second, false)
		if err != nil {
			return nil, err
		}
		return append(drivers, second...), nil
	}
	base.downloadPkg = func(dbc.PkgInfo) (*os.File, error) {
		r.downloads++
		data, err := os.ReadFile(r.archive)
		if err != nil {
			return nil, err
		}
		dir, err := os.MkdirTemp("", "adbc-drivers-test-*")
		if err != nil {
			return nil, err
		}
		p := filepath.Join(dir, filepath.Base(r.archive))
		if err := os.WriteFile(p, data, 0o600); err != nil {
			return nil, err
		}
		return os.Open(p)
	}
	return base
}

// assertImplicitResolutionFails checks that add, install, lock and sync of
// test-driver-no-sig without a chosen registry fail with want and the hint
// of each command, before anything is downloaded or written.
func (suite *SubcommandTestSuite) assertImplicitResolutionFails(regs *signerRegistries, want string) {
	listPath := suite.writeList("[drivers]\n[drivers.test-driver-no-sig]\n")
	list, err := os.ReadFile(listPath)
	suite.Require().NoError(err)

	out := suite.runCmdErr(AddCmd{Driver: []string{"test-driver-no-sig"}, Path: listPath}.GetModelCustom(regs.base()))
	suite.Contains(out, want)
	suite.Contains(out, registryHint)
	after, err := os.ReadFile(listPath)
	suite.Require().NoError(err)
	suite.Equal(list, after, "the driver list must stay untouched")

	out = suite.runCmdErr(InstallCmd{Driver: "test-driver-no-sig", Level: suite.configLevel}.GetModelCustom(regs.base()))
	suite.Contains(out, want)
	suite.Contains(out, registryHint)
	suite.driverIsNotInstalled("test-driver-no-sig")

	for _, cmd := range []commandModel{
		LockCmd{Path: listPath},
		SyncCmd{Path: listPath, Level: suite.configLevel},
	} {
		out = suite.runCmdErr(cmd.GetModelCustom(regs.base()))
		suite.Contains(out, want)
		suite.Contains(out, sourceHint)
		suite.NoFileExists(suite.lockPath())
	}
	suite.Zero(regs.downloads)
}

func (suite *SubcommandTestSuite) TestSignerConflictBetweenKeys() {
	regs, key, _ := suite.newSignerRegistries()
	regs.second.Signing = dbc.SignWith(key)
	suite.assertImplicitResolutionFails(regs, "different signers")
}

func (suite *SubcommandTestSuite) TestSignerConflictBetweenUnsignedRegistries() {
	regs, _, _ := suite.newSignerRegistries()
	regs.first.Signing, regs.second.Signing = dbc.Unsigned(), dbc.Unsigned()
	suite.assertImplicitResolutionFails(regs, "different signers")
}

// A registry with another signer whose index is missing might list the
// driver, so resolution without a chosen registry stops.
func (suite *SubcommandTestSuite) TestMissingIndexWithAnotherSigner() {
	regs, key, _ := suite.newSignerRegistries()
	regs.second.Signing = dbc.SignWith(key)
	regs.missingSecond = true
	suite.assertImplicitResolutionFails(regs, dbc.ErrIncompleteIndex.Error())
}

// Choosing the registry avoids both the conflict and the missing index, and
// the package is verified with the key of that registry.
func (suite *SubcommandTestSuite) TestChosenRegistryAvoidsASignerConflict() {
	regs, key, _ := suite.newSignerRegistries()
	regs.second.Signing = dbc.SignWith(key)

	out := suite.runCmd(InstallCmd{Driver: "test-driver-no-sig", Level: suite.configLevel, Registry: secondRegistryURL, Json: true}.GetModelCustom(regs.base()))
	suite.Contains(out, `"signature_verified":true`)
	suite.runCmd(UninstallCmd{Driver: "test-driver-no-sig", Level: suite.configLevel}.GetModel())

	listPath := suite.writeList(secondByName + "[drivers]\n")
	suite.runCmd(AddCmd{Driver: []string{"test-driver-no-sig"}, Path: listPath, Registry: "second"}.GetModelCustom(regs.base()))
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(regs.base()))
	entry := suite.lockedDriverEntry("test-driver-no-sig")
	suite.Equal(secondRegistryURL, entry.Source.URL)
	suite.Equal(lockedSigner{Type: lockSignerOpenPGP, Fingerprint: key.Fingerprint()}, entry.Signer)
	out = suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(regs.base()))
	suite.Contains(out, `"signature_verified":true`)

	// The missing index of the other registry does not matter either.
	suite.Require().NoError(os.Remove(suite.lockPath()))
	regs.missingSecond = true
	regs.first.Signing = dbc.Unsigned()
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(regs.base()))
}

// Registries with the same certificate fall back to one another, also when
// the index of one of them is missing.
func (suite *SubcommandTestSuite) TestSameKeyToleratesAMissingIndex() {
	regs, key, armored := suite.newSignerRegistries()
	copyOfKey, err := dbc.NewSigningKey([]byte(armored))
	suite.Require().NoError(err)
	regs.first.Signing, regs.second.Signing = dbc.SignWith(key), dbc.SignWith(copyOfKey)
	listPath := suite.writeList("[drivers]\n[drivers.test-driver-no-sig]\n")

	regs.missingSecond = true
	out := suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(regs.base()))
	suite.Contains(out, `"signature_verified":true`)
	suite.Equal(firstRegistryURL, suite.lockedDriverEntry("test-driver-no-sig").Source.URL)
}

// TestSyncVerifiesWithTheCertificateOfTheSignerCheck replaces the key file
// after the signer check of two registries read it, before the package is
// downloaded: the package signed by the original key still verifies and the
// lock file pins it, while the next operation reads the new key.
func (suite *SubcommandTestSuite) TestSyncVerifiesWithTheCertificateOfTheSignerCheck() {
	regs, key, armored := suite.newSignerRegistries()
	keyPath := filepath.Join(suite.T().TempDir(), "acme.asc")
	suite.Require().NoError(os.WriteFile(keyPath, []byte(armored), 0o600))
	client, err := dbc.NewClient(dbc.WithGlobalConfig(&dbc.GlobalConfig{
		Registries: []dbc.RegistryEntry{
			{URL: firstRegistryURL, SigningKey: "acme"},
			{URL: secondRegistryURL, SigningKey: "acme"},
		},
		SigningKeys:              map[string]dbc.SigningKeyDefinition{"acme": {Type: dbc.SigningKeyTypeOpenPGP, PublicKeyFile: &keyPath}},
		ExcludeBuiltinRegistries: true,
	}))
	suite.Require().NoError(err)
	configured := client.Registries()
	suite.Require().Len(configured, 2)
	regs.first, regs.second = configured[0], configured[1]

	_, replacement, replacementArmored := suite.signedArchive()
	base := regs.base()
	download := base.downloadPkg
	base.downloadPkg = func(pkg dbc.PkgInfo) (*os.File, error) {
		suite.Require().NoError(os.WriteFile(keyPath, []byte(replacementArmored), 0o600))
		return download(pkg)
	}

	listPath := suite.writeList("[drivers]\n[drivers.test-driver-no-sig]\n")
	out := suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(base))
	suite.Contains(out, `"signature_verified":true`)
	suite.Equal(key.Fingerprint(), suite.lockedDriverEntry("test-driver-no-sig").Signer.Fingerprint)

	// The next operation reads the file again and pins the new key. The
	// package is signed by the old one, which the installation rejects.
	suite.Require().NoError(os.Remove(suite.lockPath()))
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(regs.base()))
	suite.Equal(replacement.Fingerprint(), suite.lockedDriverEntry("test-driver-no-sig").Signer.Fingerprint)
	out = suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(regs.base()))
	suite.Contains(out, "failed to verify signature")
}
