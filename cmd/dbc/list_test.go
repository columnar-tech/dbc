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
	"os"
	"path/filepath"
	"runtime"

	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
)

func (suite *SubcommandTestSuite) TestListEmpty() {
	m := ListCmd{Level: config.ConfigEnv}.GetModel()
	out := suite.runCmd(m)
	suite.Contains(out, "No drivers installed.")
}

func (suite *SubcommandTestSuite) TestListInstalled() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	install := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(testBaseModel())
	suite.runCmd(install)

	m := ListCmd{Level: config.ConfigEnv}.GetModel()
	out := suite.runCmd(m)

	suite.Contains(out, "test-driver-1")
	suite.Contains(out, "1.1.0")
	suite.Contains(out, "env")
	suite.Contains(out, suite.tempdir)
}

func (suite *SubcommandTestSuite) TestListJSONEmpty() {
	m := ListCmd{Level: config.ConfigEnv, Json: true}.GetModel()
	out := suite.runCmd(m)

	var env jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(out), &env))
	suite.Equal(1, env.SchemaVersion)
	suite.Equal("list.results", env.Kind)

	var resp jsonschema.ListResponse
	suite.Require().NoError(json.Unmarshal(env.Payload, &resp))
	suite.Empty(resp.Drivers)
}

func (suite *SubcommandTestSuite) TestListJSONInstalled() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	install := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(testBaseModel())
	suite.runCmd(install)

	m := ListCmd{Level: config.ConfigEnv, Json: true}.GetModel()
	out := suite.runCmd(m)

	var env jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(out), &env))
	suite.Equal("list.results", env.Kind)

	var resp jsonschema.ListResponse
	suite.Require().NoError(json.Unmarshal(env.Payload, &resp))
	suite.Len(resp.Drivers, 1)
	entry := resp.Drivers[0]
	suite.Equal("test-driver-1", entry.Driver)
	suite.Equal("1.1.0", entry.Version)
	suite.Equal("env", entry.Level)
	suite.Equal(suite.tempdir, entry.Location)
	suite.NotEmpty(entry.Name)
}

func (suite *SubcommandTestSuite) TestListUnreadableConfig() {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		suite.T().Skip()
	}

	unreadable := filepath.Join(suite.T().TempDir(), "unreadable")
	suite.Require().NoError(os.Mkdir(unreadable, 0o000))
	suite.T().Cleanup(func() { _ = os.Chmod(unreadable, 0o700) })
	suite.T().Setenv("ADBC_DRIVER_PATH", filepath.Join(unreadable, "drivers"))

	m := ListCmd{Level: config.ConfigEnv}.GetModel()
	out := suite.runCmdErr(m)
	suite.Contains(out, "failed to list drivers")
}

func (suite *SubcommandTestSuite) TestListUnreadableConfigJSON() {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		suite.T().Skip()
	}

	unreadable := filepath.Join(suite.T().TempDir(), "unreadable")
	suite.Require().NoError(os.Mkdir(unreadable, 0o000))
	suite.T().Cleanup(func() { _ = os.Chmod(unreadable, 0o700) })
	suite.T().Setenv("ADBC_DRIVER_PATH", filepath.Join(unreadable, "drivers"))

	m := ListCmd{Level: config.ConfigEnv, Json: true}.GetModel()
	out := suite.runCmdErr(m)
	suite.assertJSONErrorEnvelope(out, "list_failed", "failed to load drivers")
}
