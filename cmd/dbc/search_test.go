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

import (
	"path/filepath"

	"github.com/columnar-tech/dbc/config"
)

func (suite *SubcommandTestSuite) TestSearchCmd() {
	m := SearchCmd{}.GetModelCustom(
		baseModel{getDriverList: getTestDriverList,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("• test-driver-1 - This is a test driver\r\n"+
		"• test-driver-2 - This is another test driver\r\n"+
		"• test-driver-manifest-only - This is manifest-only driver\r\n"+
		"• test-driver-no-sig - Driver manifest missing Files.signature entry\r\n"+
		"• test-driver-invalid-manifest - This is test driver with an invalid manfiest. See https://github.com/columnar-tech/dbc/issues/37.\r\n\r ", "", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSearchCmdWithInstalled() {
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.runCmd(m)

	m = SearchCmd{}.GetModelCustom(
		baseModel{getDriverList: getTestDriverList,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("• test-driver-1 - This is a test driver [installed: env=>1.1.0]\r\n"+
		"• test-driver-2 - This is another test driver\r\n• test-driver-manifest-only - This is manifest-only driver\r\n"+
		"• test-driver-no-sig - Driver manifest missing Files.signature entry\r\n"+
		"• test-driver-invalid-manifest - This is test driver with an invalid manfiest. See https://github.com/columnar-tech/dbc/issues/37.\r\n\r ", "", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSearchCmdVerbose() {
	m := SearchCmd{Verbose: true}.GetModelCustom(
		baseModel{getDriverList: getTestDriverList,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("• test-driver-1\r\n   Title: Test Driver 1\r\n   "+
		"Description: This is a test driver\r\n   License: MIT\r\n   "+
		"Available Versions:\r\n    ├── 1.0.0\r\n    ╰── 1.1.0\r\n"+
		"• test-driver-2\r\n   Title: Test Driver 2\r\n   "+
		"Description: This is another test driver\r\n   License: Apache-2.0\r\n   "+
		"Available Versions:\r\n    ├── 2.0.0\r\n    ╰── 2.1.0\r\n"+
		"• test-driver-manifest-only\r\n   Title: Test Driver Manifest Only\r\n   "+
		"Description: This is manifest-only driver\r\n   License: Apache-2.0\r\n   "+
		"Available Versions:\r\n    ╰── 1.0.0\r\n"+
		"• test-driver-no-sig\r\n   Title: Test Driver No Signature\r\n   "+
		"Description: Driver manifest missing Files.signature entry\r\n   License: Apache-2.0\r\n   "+
		"Available Versions:\r\n    ╰── 1.0.0\r\n"+
		"• test-driver-invalid-manifest\r\n   Title: Test Driver Invalid Manifest\r\n   "+
		"Description: This is test driver with an invalid manfiest. See https://github.com/columnar-tech/dbc/issues/37.\r\n   License: Apache-2.0\r\n   "+
		"Available Versions:\r\n    ╰── 1.0.0\r\n\r ", "", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSearchCmdVerboseWithInstalled() {
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.runCmd(m)

	m = SearchCmd{Verbose: true}.GetModelCustom(
		baseModel{getDriverList: getTestDriverList,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("• test-driver-1\r\n   Title: Test Driver 1\r\n   "+
		"Description: This is a test driver\r\n   License: MIT\r\n   "+
		"Installed Versions:\r\n    ╰── 1.1.0\r\n        ╰── env => "+filepath.Join(suite.tempdir)+
		"\r\n   Available Versions:\r\n    ├── 1.0.0\r\n    ╰── 1.1.0\r\n"+
		"• test-driver-2\r\n   Title: Test Driver 2\r\n   "+
		"Description: This is another test driver\r\n   License: Apache-2.0\r\n   "+
		"Available Versions:\r\n    ├── 2.0.0\r\n    ╰── 2.1.0\r\n"+
		"• test-driver-manifest-only\r\n"+
		"   Title: Test Driver Manifest Only\r\n"+
		"   Description: This is manifest-only driver\r\n"+
		"   License: Apache-2.0\r\n"+
		"   Available Versions:\r\n"+
		"    ╰── 1.0.0\r\n"+
		"• test-driver-no-sig\r\n"+
		"   Title: Test Driver No Signature\r\n"+
		"   Description: Driver manifest missing Files.signature entry\r\n"+
		"   License: Apache-2.0\r\n"+
		"   Available Versions:\r\n"+
		"    ╰── 1.0.0\r\n"+
		"• test-driver-invalid-manifest\r\n"+
		"   Title: Test Driver Invalid Manifest\r\n"+
		"   Description: This is test driver with an invalid manfiest. See https://github.com/columnar-tech/dbc/issues/37.\r\n"+
		"   License: Apache-2.0\r\n"+
		"   Available Versions:\r\n"+
		"    ╰── 1.0.0\r\n\r ", "", suite.runCmd(m))
}
