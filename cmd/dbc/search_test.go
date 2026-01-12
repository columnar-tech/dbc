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
		baseModel{getDriverRegistry: getTestDriverRegistry,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("\r ",
		"test-driver-1                This is a test driver                                                                             \n"+
			"test-driver-2                This is another test driver                                                                       \n"+
			"test-driver-manifest-only    This is manifest-only driver                                                                      \n"+
			"test-driver-no-sig           Driver manifest missing Files.signature entry                                                     \n"+
			"test-driver-invalid-manifest This is test driver with an invalid manifest. See https://github.com/columnar-tech/dbc/issues/37. \n"+
			"test-driver-docs-url         This is manifest-only with its docs_url key set                                                   \n", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSearchCmdWithInstalled() {
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.runCmd(m)

	m = SearchCmd{}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("\r ",
		"test-driver-1                This is a test driver                                                                              [installed: env=>1.1.0]\n"+
			"test-driver-2                This is another test driver                                                                                               \n"+
			"test-driver-manifest-only    This is manifest-only driver                                                                                              \n"+
			"test-driver-no-sig           Driver manifest missing Files.signature entry                                                                             \n"+
			"test-driver-invalid-manifest This is test driver with an invalid manifest. See https://github.com/columnar-tech/dbc/issues/37.                         \n"+
			"test-driver-docs-url         This is manifest-only with its docs_url key set                                                                           \n", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSearchCmdVerbose() {
	m := SearchCmd{Verbose: true}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("\r ", "• test-driver-1\n   Title: Test Driver 1\n   "+
		"Description: This is a test driver\n   License: MIT\n   "+
		"Available Versions:\n    ├── 1.0.0\n    ╰── 1.1.0\n"+
		"• test-driver-2\n   Title: Test Driver 2\n   "+
		"Description: This is another test driver\n   License: Apache-2.0\n   "+
		"Available Versions:\n    ├── 2.0.0\n    ╰── 2.1.0\n"+
		"• test-driver-manifest-only\n   Title: Test Driver Manifest Only\n   "+
		"Description: This is manifest-only driver\n   License: Apache-2.0\n   "+
		"Available Versions:\n    ╰── 1.0.0\n"+
		"• test-driver-no-sig\n   Title: Test Driver No Signature\n   "+
		"Description: Driver manifest missing Files.signature entry\n   License: Apache-2.0\n   "+
		"Available Versions:\n    ╰── 1.0.0\n"+
		"• test-driver-invalid-manifest\n   Title: Test Driver Invalid Manifest\n   "+
		"Description: This is test driver with an invalid manifest. See https://github.com/columnar-tech/dbc/issues/37.\n   License: Apache-2.0\n   "+
		"Available Versions:\n    ╰── 1.0.0\n"+
		"• test-driver-docs-url\n   Title: Test Driver With Docs URL Set\n   "+
		"Description: This is manifest-only with its docs_url key set\n   License: Apache-2.0\n   "+
		"Available Versions:\n    ╰── 1.0.0\n", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSearchCmdVerboseWithInstalled() {
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.runCmd(m)

	m = SearchCmd{Verbose: true}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("\r ", "• test-driver-1\n   Title: Test Driver 1\n   "+
		"Description: This is a test driver\n   License: MIT\n   "+
		"Installed Versions:\n    ╰── 1.1.0\n        ╰── env => "+filepath.Join(suite.tempdir)+
		"\n   Available Versions:\n    ├── 1.0.0\n    ╰── 1.1.0\n"+
		"• test-driver-2\n   Title: Test Driver 2\n   "+
		"Description: This is another test driver\n   License: Apache-2.0\n   "+
		"Available Versions:\n    ├── 2.0.0\n    ╰── 2.1.0\n"+
		"• test-driver-manifest-only\n"+
		"   Title: Test Driver Manifest Only\n"+
		"   Description: This is manifest-only driver\n"+
		"   License: Apache-2.0\n"+
		"   Available Versions:\n"+
		"    ╰── 1.0.0\n"+
		"• test-driver-no-sig\n"+
		"   Title: Test Driver No Signature\n"+
		"   Description: Driver manifest missing Files.signature entry\n"+
		"   License: Apache-2.0\n"+
		"   Available Versions:\n"+
		"    ╰── 1.0.0\n"+
		"• test-driver-invalid-manifest\n"+
		"   Title: Test Driver Invalid Manifest\n"+
		"   Description: This is test driver with an invalid manifest. See https://github.com/columnar-tech/dbc/issues/37.\n"+
		"   License: Apache-2.0\n"+
		"   Available Versions:\n"+
		"    ╰── 1.0.0\n"+
		"• test-driver-docs-url\n"+
		"   Title: Test Driver With Docs URL Set\n"+
		"   Description: This is manifest-only with its docs_url key set\n"+
		"   License: Apache-2.0\n"+
		"   Available Versions:\n"+
		"    ╰── 1.0.0\n", suite.runCmd(m))
}
