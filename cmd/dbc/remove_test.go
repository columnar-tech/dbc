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
	"path/filepath"
)

func (suite *SubcommandTestSuite) TestRemoveOutput() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Add a driver first
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1"},
	}.GetModelCustom(
		testBaseModel())
	suite.runCmd(m)

	// Remove the driver and verify output
	m = RemoveCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: "test-driver-1",
	}.GetModelCustom(
		testBaseModel())

	out := suite.runCmd(m)
	suite.Contains(out, "removed 'test-driver-1' from driver list")
}

func (suite *SubcommandTestSuite) TestRemoveNonexistentDriverError() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Add a driver first so the list isn't empty
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1"},
	}.GetModelCustom(
		testBaseModel())
	suite.runCmd(m)

	// Try to remove a driver that doesn't exist
	m = RemoveCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: "nonexistent-driver",
	}.GetModelCustom(
		testBaseModel())

	out := suite.runCmdErr(m)
	suite.Contains(out, "driver 'nonexistent-driver' not found")
}

func (suite *SubcommandTestSuite) TestRemoveFromNonexistentFile() {
	// Try to remove from a file that doesn't exist
	m := RemoveCmd{
		Path:   filepath.Join(suite.tempdir, "nonexistent-dbc.toml"),
		Driver: "test-driver-1",
	}.GetModelCustom(
		testBaseModel())

	out := suite.runCmdErr(m)
	suite.Contains(out, "doesn't exist")
	suite.Contains(out, "Did you run `dbc init`?")
}
