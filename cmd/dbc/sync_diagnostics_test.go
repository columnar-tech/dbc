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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
)

// runCmdWithRenderer captures tea.Println output, which the shared headless
// runner intentionally suppresses by disabling Bubble Tea's renderer.
func (suite *SubcommandTestSuite) runCmdWithRenderer(m tea.Model, status int) string {
	suite.T().Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	if jw, ok := m.(HasJSONWriter); ok {
		m = jw.WithJSONWriter(&out)
	}
	prog = tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out), tea.WithContext(ctx))
	defer func() { prog = nil }()
	result, err := prog.Run()
	prog.Wait()
	suite.Require().NoError(err)
	suite.Equal(status, result.(HasStatus).Status())
	if result.(HasStatus).Status() != 0 {
		if !result.(interface{ IsJSONMode() bool }).IsJSONMode() {
			return ansi.Strip(out.String() + "\n" + formatErr(result.(HasStatus).Err()))
		}
	}
	var extra string
	if fo, ok := result.(HasFinalOutput); ok {
		extra = fo.FinalOutput()
	}
	return ansi.Strip(out.String() + extra)
}

func shortSecretRegistryBase(base baseModel) (baseModel, dbc.Registry) {
	u := *testRegistry.BaseURL
	u.RawQuery = "v=1"
	registry := dbc.Registry{Name: testRegistry.Name, BaseURL: &u}
	base.getRegistries = func() ([]dbc.Registry, error) { return []dbc.Registry{registry}, nil }
	return base, registry
}

func bindTestDrivers(drivers []dbc.Driver, registry dbc.Registry) []dbc.Driver {
	for i := range drivers {
		drivers[i].Registry = &registry
	}
	return drivers
}

// The digit in this test name exercises short-value redaction regressions.
func (suite *SubcommandTestSuite) TestSyncRegistryQueryV1DoesNotRedactLocalDiagnostics() {
	listPath := suite.writeDriverList("test-driver-1")
	hash := strings.Repeat("0", 64)
	base, registry := shortSecretRegistryBase(hashedBaseModel(hash))
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		drivers, err := hashedRegistry(hash)()
		return bindTestDrivers(drivers, registry), err
	}

	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(base))
	suite.assertJSONErrorEnvelope(out, "checksum_failed", "archive SHA-256 mismatch", hash)

	suite.writeV1Lock("1.0.0", config.PlatformTuple(), suite.libraryChecksum("test-driver-1.tar.gz"))
	malformedLock := []byte("version = 1\n[[drivers]\nname = 'unterminated\n")
	suite.Require().NoError(os.WriteFile(suite.lockPath(), malformedLock, 0o644))
	out = suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(base))
	suite.assertJSONErrorEnvelope(out, "sync_failed", "error decoding lock file")
	suite.NotContains(out, "details are hidden")
}

func (suite *SubcommandTestSuite) TestSyncRegistryAccessErrorStillHidesShortSecret1() {
	listPath := suite.writeDriverList("test-driver-1")
	base, registry := shortSecretRegistryBase(testBaseModel())
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		return nil, fmt.Errorf("registry request failed for %s?%s", registry.BaseURL.Host, registry.BaseURL.RawQuery)
	}
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(base))
	suite.assertJSONErrorEnvelope(out, "sync_failed", "details are hidden")
	suite.NotContains(out, "v=1")
}

func (suite *SubcommandTestSuite) TestSyncGetPackageURLParseErrorHidesShortSecret1() {
	listPath := suite.writeDriverList("test-driver-1")
	base, registry := shortSecretRegistryBase(testBaseModel())
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		drivers, err := decodeTestIndex(`drivers:
  - name: Test Driver 1
    path: test-driver-1
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: ` + config.PlatformTuple() + `
            url: http://%31.invalid/archive
`)
		return bindTestDrivers(drivers, registry), err
	}
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(base))
	suite.assertJSONErrorEnvelope(out, "sync_failed", "details are hidden")
	suite.NotContains(out, "%31")
}

func (suite *SubcommandTestSuite) TestSyncGetPackageURLParseErrorWithNoRegistryIsGraceful() {
	listPath := suite.writeDriverList("test-driver-1")
	base, _ := shortSecretRegistryBase(testBaseModel())
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		drivers, err := decodeTestIndex(`drivers:
  - name: Test Driver 1
    path: test-driver-1
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: ` + config.PlatformTuple() + `
            url: http://%31.invalid/archive
`)
		if err != nil {
			return nil, err
		}
		for i := range drivers {
			drivers[i].Registry = nil
		}
		return drivers, nil
	}
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(base))
	suite.assertJSONErrorEnvelope(out, "sync_failed", "details are hidden")
	suite.NotContains(out, "%31")
}

func (suite *SubcommandTestSuite) TestSyncMalformedLockURLCredentialsAreStripped() {
	listPath := suite.writeDriverList("test-driver-1")
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(testBaseModel()))
	lock, err := os.ReadFile(suite.lockPath())
	suite.Require().NoError(err)
	identity := "https://user:VERYSECRET@registry.columnar.tech?token=VERYSECRET"
	contents := strings.Replace(string(lock), "https://registry.columnar.tech", identity, 1)
	suite.Require().NotEqual(string(lock), contents)
	suite.Require().NoError(os.WriteFile(suite.lockPath(), []byte(contents), 0o644))
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(testBaseModel()))
	suite.assertJSONErrorEnvelope(out, "sync_failed", "not canonical")
	suite.NotContains(out, "VERYSECRET")
	suite.NotContains(out, "user:")
	suite.NotContains(out, "token=")
}

func (suite *SubcommandTestSuite) TestSyncV1OtherPlatformWarningUsesTextOutput() {
	listPath := suite.writeDriverList("test-driver-1")
	suite.writeV1Lock("1.0.0", "plan9_riscv", strings.Repeat("0", 64))
	var output string
	stderr := captureStderr(suite, func() {
		output = suite.runCmdWithRenderer(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(testBaseModel()), 0)
	})
	suite.Contains(output, "warning: ignoring the checksum")
	suite.Contains(output, "plan9_riscv")
	suite.Empty(stderr)
}

func (suite *SubcommandTestSuite) TestSyncV1OtherPlatformWarningSurvivesDownloadFailure() {
	listPath := suite.writeDriverList("test-driver-1")
	suite.writeV1Lock("1.0.0", "plan9_riscv", strings.Repeat("0", 64))
	base := testBaseModel()
	base.downloadPkg = func(dbc.PkgInfo) (*os.File, error) { return nil, fmt.Errorf("mock download failed") }
	out := suite.runCmdWithRenderer(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(base), 1)
	suite.Contains(out, "warning: ignoring the checksum")
	suite.Contains(out, "plan9_riscv")
	suite.Contains(out, "mock download failed")
}

func (suite *SubcommandTestSuite) TestSyncJSONStreamV1WarningUsesStderr() {
	listPath := suite.writeDriverList("test-driver-1")
	suite.writeV1Lock("1.0.0", "plan9_riscv", strings.Repeat("0", 64))
	var output string
	stderr := captureStderr(suite, func() {
		output = suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, JsonStreamProgress: true}.GetModelCustom(testBaseModel()))
	})
	suite.Contains(stderr, "warning: ignoring the checksum")
	suite.Contains(stderr, "plan9_riscv")
	suite.NotContains(output, "warning")
	suite.Contains(output, `"kind":"sync.status"`)
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		suite.True(json.Valid([]byte(line)), line)
	}
}
