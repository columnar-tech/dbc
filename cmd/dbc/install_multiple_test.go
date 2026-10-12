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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/stretchr/testify/assert"
)

// decodeInstallJSON decodes `dbc install --json` output: the install.status
// envelopes (one per driver, in order) and the final install.result envelope.
// It also returns the raw install.result payload.
func (suite *SubcommandTestSuite) decodeInstallJSON(out string) ([]jsonschema.InstallStatus, jsonschema.InstallResult, string) {
	var statuses []jsonschema.InstallStatus
	var result jsonschema.InstallResult
	var rawResult string
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	for i, line := range lines {
		var env jsonschema.Envelope
		suite.Require().NoError(json.Unmarshal([]byte(line), &env), "output line must be valid JSON: %s", line)
		suite.Equal(1, env.SchemaVersion)
		switch env.Kind {
		case "install.status":
			var status jsonschema.InstallStatus
			suite.Require().NoError(json.Unmarshal(env.Payload, &status))
			statuses = append(statuses, status)
		case "install.result":
			suite.Require().Equal(len(lines)-1, i, "install.result must be the last line: %s", out)
			suite.Require().NoError(json.Unmarshal(env.Payload, &result))
			rawResult = string(env.Payload)
		}
	}
	suite.Require().NotEmpty(rawResult, "expected an install.result envelope: %s", out)
	return statuses, result, rawResult
}

// assertInstallStatusesBeforeError checks that a failed `dbc install --json`
// reported exactly the drivers installed before the error, as install.status
// envelopes, and no install.result.
func (suite *SubcommandTestSuite) assertInstallStatusesBeforeError(out string, drivers ...string) {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		var env jsonschema.Envelope
		suite.Require().NoError(json.Unmarshal([]byte(line), &env))
		suite.NotEqual("install.result", env.Kind, "install.result must not be emitted on failure")
		if env.Kind == "install.status" {
			var status jsonschema.InstallStatus
			suite.Require().NoError(json.Unmarshal(env.Payload, &status))
			got = append(got, status.Driver)
		}
	}
	suite.Equal(drivers, got)
}

// downloadUnlicensed behaves like downloadTestPkg except that downloading the
// driver named private fails the way an unlicensed private registry download
// does.
func downloadUnlicensed(private string) func(dbc.PkgInfo) (*os.File, error) {
	return func(pkg dbc.PkgInfo) (*os.File, error) {
		if pkg.Driver.Path == private {
			return nil, fmt.Errorf("dbc-cdn-private.columnar.tech/%s: %w", private, dbc.ErrUnauthorizedColumnar)
		}
		return downloadTestPkg(pkg)
	}
}

func (suite *SubcommandTestSuite) TestInstallMultiple() {
	m := InstallCmd{Driver: []string{"test-driver-1", "test-driver-manifest-only"}, Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	out := suite.runCmd(m)

	suite.Equal("Resolved 2 drivers\nInstalled 2 drivers\n"+
		"├── ✓ test-driver-1 1.1.0\n"+
		"└── ✓ test-driver-manifest-only 1.0.0\n"+
		"    └── Must have libtest_driver installed to load this driver", out)
	suite.driverIsInstalled("test-driver-1", true)
	suite.driverIsInstalled("test-driver-manifest-only", false)
}

func (suite *SubcommandTestSuite) TestInstallMultiplePostInstallMessageOrder() {
	m := InstallCmd{Driver: []string{"test-driver-manifest-only", "test-driver-1"}, Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	out := suite.runCmd(m)

	suite.Equal("Resolved 2 drivers\nInstalled 2 drivers\n"+
		"├── ✓ test-driver-manifest-only 1.0.0\n"+
		"│   └── Must have libtest_driver installed to load this driver\n"+
		"└── ✓ test-driver-1 1.1.0", out)
}

func (suite *SubcommandTestSuite) TestInstallMultipleSkipsAlreadyInstalled() {
	m := InstallCmd{Driver: []string{"test-driver-1"}, Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	suite.runCmd(m)

	m = InstallCmd{Driver: []string{"test-driver-1", "test-driver-manifest-only"}, Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	out := suite.runCmd(m)

	suite.Equal("Resolved 2 drivers\nInstalled 2 drivers\n"+
		"├── ✓ test-driver-1 1.1.0 (already installed)\n"+
		"└── ✓ test-driver-manifest-only 1.0.0\n"+
		"    └── Must have libtest_driver installed to load this driver", out)
	suite.driverIsInstalled("test-driver-manifest-only", false)
}

func (suite *SubcommandTestSuite) TestInstallMultipleDuplicateDriver() {
	m := InstallCmd{Driver: []string{"test-driver-1", "test-driver-1=1.0.0"}, Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)

	suite.Contains(out, "driver `test-driver-1` specified more than once")
	suite.driverIsNotInstalled("test-driver-1")
}

func (suite *SubcommandTestSuite) TestInstallMultipleNotFoundInstallsNothing() {
	m := InstallCmd{Driver: []string{"test-driver-1", "foo"}, Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)

	suite.Contains(out, "could not find driver: driver `foo` not found in driver registry index")
	suite.driverIsNotInstalled("test-driver-1")
}

func (suite *SubcommandTestSuite) TestInstallMultipleNoMatchingVersionInstallsNothing() {
	m := InstallCmd{Driver: []string{"test-driver-1", "test-driver-manifest-only>=5"}, Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	suite.runCmdErr(m)

	suite.driverIsNotInstalled("test-driver-1")
	suite.driverIsNotInstalled("test-driver-manifest-only")
}

func (suite *SubcommandTestSuite) TestInstallJSONShapeSameForOneAndMany() {
	m := InstallCmd{Driver: []string{"test-driver-1"}, Level: suite.configLevel, Json: true}.
		GetModelCustom(testBaseModel())
	oneStatuses, one, onePayload := suite.decodeInstallJSON(suite.runCmd(m))

	// install.status keeps its v1, single-driver shape.
	suite.Require().Len(oneStatuses, 1)
	suite.Equal("installed", oneStatuses[0].Status)
	suite.Equal("test-driver-1", oneStatuses[0].Driver)
	suite.Require().Len(one.Installed, 1)
	suite.Equal(oneStatuses[0], one.Installed[0])
	suite.Equal("installed", one.Installed[0].Status)
	suite.Equal("test-driver-1", one.Installed[0].Driver)
	suite.Empty(one.Skipped)
	suite.Contains(onePayload, `"skipped":[]`)
	suite.Contains(onePayload, `"errors":[]`)

	// Avoid test-driver-manifest-only here: its checksum can't be computed in
	// JSON mode because its shared library isn't part of the package.
	m = InstallCmd{Driver: []string{"test-driver-1", "test-driver-only-pre"}, Level: suite.configLevel, Json: true, Pre: true}.
		GetModelCustom(testBaseModel())
	manyStatuses, many, manyPayload := suite.decodeInstallJSON(suite.runCmd(m))

	suite.Require().Len(manyStatuses, 2)
	suite.Equal("already installed", manyStatuses[0].Status)
	suite.Equal("test-driver-1", manyStatuses[0].Driver)
	suite.Equal("installed", manyStatuses[1].Status)
	suite.Equal("test-driver-only-pre", manyStatuses[1].Driver)

	suite.Require().Len(many.Skipped, 1)
	suite.Equal("already installed", many.Skipped[0].Status)
	suite.Equal("test-driver-1", many.Skipped[0].Driver)
	suite.NotEmpty(many.Skipped[0].Checksum)
	suite.Require().Len(many.Installed, 1)
	suite.Equal("installed", many.Installed[0].Status)
	suite.Equal("test-driver-only-pre", many.Installed[0].Driver)
	suite.NotEmpty(many.Installed[0].Checksum)
	suite.Contains(manyPayload, `"errors":[]`)
}

func (suite *SubcommandTestSuite) TestInstallPrivateThenPublicUnlicensed() {
	// test-driver-1 plays the private driver.
	m := InstallCmd{Driver: []string{"test-driver-1", "test-driver-manifest-only"}, Level: suite.configLevel}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadUnlicensed("test-driver-1")})
	out := suite.runCmdErr(m)

	suite.Contains(out, "active license")
	suite.driverIsNotInstalled("test-driver-1")
	suite.driverIsNotInstalled("test-driver-manifest-only")
}

func (suite *SubcommandTestSuite) TestInstallPublicThenPrivateUnlicensed() {
	// test-driver-1 plays the private driver.
	m := InstallCmd{Driver: []string{"test-driver-manifest-only", "test-driver-1"}, Level: suite.configLevel}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadUnlicensed("test-driver-1")})
	out := suite.runCmdErr(m)

	suite.Contains(out, "├── ✓ test-driver-manifest-only 1.0.0\n"+
		"│   └── Must have libtest_driver installed to load this driver\n"+
		"└── ✗ test-driver-1 → 1.1.0\n")
	suite.Contains(out, "Resolved 2 drivers\nInstalled 1 of 2 drivers\n")
	suite.Contains(out, "└── ✗ test-driver-1 → 1.1.0\n    └── dbc-cdn-private.columnar.tech/test-driver-1: not authorized to access\n")
	suite.Contains(out, "active license")
	suite.Equal(1, strings.Count(out, "not authorized to access"), "the error should only be shown once")
	suite.driverIsInstalled("test-driver-manifest-only", false)
	suite.driverIsNotInstalled("test-driver-1")
}

func (suite *SubcommandTestSuite) TestInstallPublicThenPrivateUnlicensedJSON() {
	// test-driver-only-pre plays the private driver.
	m := InstallCmd{Driver: []string{"test-driver-1", "test-driver-only-pre"}, Level: suite.configLevel, Json: true, Pre: true}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadUnlicensed("test-driver-only-pre")})
	out := suite.runCmdErr(m)

	suite.assertJSONErrorEnvelope(out, "install_failed", dbc.ErrUnauthorizedColumnar.Error())
	suite.assertInstallStatusesBeforeError(out, "test-driver-1")
	suite.driverIsInstalled("test-driver-1", true)
	suite.driverIsNotInstalled("test-driver-only-pre")
}

func (suite *SubcommandTestSuite) TestInstallRegistryThenLocalBadSignature() {
	packagePath := filepath.Join("testdata", "test-driver-no-sig.tar.gz")
	m := InstallCmd{Driver: []string{"test-driver-1", packagePath}, Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)

	suite.Contains(out, "├── ✓ test-driver-1 1.1.0\n└── ✗ "+packagePath+"\n")
	suite.Contains(out, "signature file 'test-driver-1-not-valid.so.sig' for driver is missing")
	suite.driverIsInstalled("test-driver-1", true)
	suite.driverIsNotInstalled("test-driver-no-sig")
	suite.NoDirExists(filepath.Join(suite.Dir(), "test-driver-no-sig"))
}

func (suite *SubcommandTestSuite) TestInstallRegistryThenLocalBadSignatureJSON() {
	packagePath := filepath.Join("testdata", "test-driver-no-sig.tar.gz")
	m := InstallCmd{Driver: []string{"test-driver-1", packagePath}, Level: suite.configLevel, Json: true}.
		GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)

	suite.assertJSONErrorEnvelope(out, "install_failed", "signature file 'test-driver-1-not-valid.so.sig' for driver is missing")
	suite.assertInstallStatusesBeforeError(out, "test-driver-1")
	suite.driverIsInstalled("test-driver-1", true)
	suite.NoDirExists(filepath.Join(suite.Dir(), "test-driver-no-sig"))
}

func (suite *SubcommandTestSuite) TestInstallLocalBadSignatureThenRegistry() {
	packagePath := filepath.Join("testdata", "test-driver-no-sig.tar.gz")
	m := InstallCmd{Driver: []string{packagePath, "test-driver-1"}, Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)

	suite.Contains(out, "signature file 'test-driver-1-not-valid.so.sig' for driver is missing")
	suite.driverIsNotInstalled("test-driver-1")
	suite.NoDirExists(filepath.Join(suite.Dir(), "test-driver-no-sig"))
}

func (suite *SubcommandTestSuite) TestInstallMultipleMissingLocalPackageInstallsNothing() {
	packagePath := filepath.Join("testdata", "does-not-exist.tar.gz")
	m := InstallCmd{Driver: []string{"test-driver-1", packagePath}, Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)

	suite.Contains(out, "open "+packagePath)
	suite.NotContains(out, "Resolved", "a missing archive should fail before installing starts")
	suite.driverIsNotInstalled("test-driver-1")
}

func TestInstallCompactViewWhenTreeDoesNotFit(t *testing.T) {
	rows := make([]installRow, 4)
	for i := range rows {
		name := fmt.Sprintf("driver-%d", i)
		rows[i] = installRow{Label: name, Name: name, Version: "1.0.0", State: rowInstalled}
	}
	base := installModel{rows: rows, finished: true}

	// 2 header lines + 4 rows fit in a 10-line terminal.
	next, _ := base.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m := next.(installModel)
	assert.True(t, m.FinalOutputInView())
	assert.Equal(t, m.render()+"\n", m.View().Content)
	assert.Equal(t, m.render(), m.FinalOutput())

	// ...but not in a 5-line one: the view shows only the header, and the tree
	// is printed after exit.
	next, _ = base.Update(tea.WindowSizeMsg{Width: 80, Height: 5})
	m = next.(installModel)
	assert.False(t, m.FinalOutputInView())
	assert.Equal(t, "Resolved 4 drivers\nInstalled 4 drivers\n", m.View().Content)
	assert.Equal(t, m.renderTree(), m.FinalOutput())

	// Compact mode sticks, so the frame doesn't change size again.
	next, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 50})
	assert.False(t, next.(installModel).FinalOutputInView())
}
