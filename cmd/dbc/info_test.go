// Copyright 2025 Columnar Technologies Inc.
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

func (suite *SubcommandTestSuite) TestInfo() {
	m := InfoCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)

	suite.validateOutput("\r ", "Driver: test-driver-1\n"+
		"Version: 1.1.0\nTitle: Test Driver 1\n"+
		"License: MIT\nDescription: This is a test driver\n"+
		"Available Packages:\n"+
		"   - linux_amd64\n   - macos_amd64\n"+
		"   - macos_arm64\n   - windows_amd64\n", out)
}

func (suite *SubcommandTestSuite) TestInfo_DriverNotFound() {
	m := InfoCmd{Driver: "non-existent-driver"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmdErr(m)

	suite.validateOutput("Error: driver `non-existent-driver` not found in driver registry index\r\n\r ", "", out)
}
