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
	"strings"

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
			"test-driver-docs-url         This is manifest-only with its docs_url key set                                                   ", suite.runCmd(m))
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
			"test-driver-docs-url         This is manifest-only with its docs_url key set                                                                           ", suite.runCmd(m))
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
		"Available Versions:\n    ╰── 1.0.0", suite.runCmd(m))
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
		"    ╰── 1.0.0", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSearchCmdWithMissingVersionInManifest() {
	// Install a driver
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.runCmd(m)

	// Corrupt the manifest by removing the version key
	manifestPath := filepath.Join(suite.tempdir, "test-driver-1.toml")
	manifestData, err := os.ReadFile(manifestPath)
	suite.Require().NoError(err, "should be able to read manifest file")

	// Remove the version line from the manifest
	lines := []string{}
	for _, line := range strings.Split(string(manifestData), "\n") {
		if !strings.HasPrefix(line, "version =") {
			lines = append(lines, line)
		}
	}
	corruptedManifest := strings.Join(lines, "\n")

	err = os.WriteFile(manifestPath, []byte(corruptedManifest), 0644)
	suite.Require().NoError(err, "should be able to write corrupted manifest")

	suite.Require().NotPanics(func() {
		m = SearchCmd{}.GetModelCustom(
			baseModel{getDriverRegistry: getTestDriverRegistry,
				downloadPkg: downloadTestPkg})
		suite.runCmd(m)
	}, "Search should not panic when manifest is missing version key")
}

func (suite *SubcommandTestSuite) TestSearchCmdWithPre() {
	m := SearchCmd{Pre: true}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("\r ",
		"test-driver-1                This is a test driver                                                                             \n"+
			"test-driver-2                This is another test driver                                                                       \n"+
			"test-driver-only-pre         This driver only has prerelease versions                                                          \n"+
			"test-driver-manifest-only    This is manifest-only driver                                                                      \n"+
			"test-driver-no-sig           Driver manifest missing Files.signature entry                                                     \n"+
			"test-driver-invalid-manifest This is test driver with an invalid manifest. See https://github.com/columnar-tech/dbc/issues/37. \n"+
			"test-driver-docs-url         This is manifest-only with its docs_url key set                                                   ", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSearchCmdVerboseWithPre() {
	m := SearchCmd{Verbose: true, Pre: true}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("\r ", "• test-driver-1\n   Title: Test Driver 1\n   "+
		"Description: This is a test driver\n   License: MIT\n   "+
		"Available Versions:\n    ├── 1.0.0\n    ╰── 1.1.0\n"+
		"• test-driver-2\n   Title: Test Driver 2\n   "+
		"Description: This is another test driver\n   License: Apache-2.0\n   "+
		"Available Versions:\n    ├── 2.0.0\n    ├── 2.1.0-beta.1\n    ╰── 2.1.0\n"+
		"• test-driver-only-pre\n   Title: Test Driver Only Prerelease\n   "+
		"Description: This driver only has prerelease versions\n   License: MIT\n   "+
		"Available Versions:\n    ╰── 0.9.0-alpha.1\n"+
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
		"Available Versions:\n    ╰── 1.0.0", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSearchCmdWithInstalledPre() {
	m := InstallCmd{Driver: "test-driver-only-pre", Level: config.ConfigEnv, Pre: true}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.runCmd(m)

	m = SearchCmd{}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("\r ",
		"test-driver-1                This is a test driver                                                                                                             \n"+
			"test-driver-2                This is another test driver                                                                                                       \n"+
			"test-driver-only-pre         This driver only has prerelease versions                                                           [installed: env=>0.9.0-alpha.1]\n"+
			"test-driver-manifest-only    This is manifest-only driver                                                                                                      \n"+
			"test-driver-no-sig           Driver manifest missing Files.signature entry                                                                                     \n"+
			"test-driver-invalid-manifest This is test driver with an invalid manifest. See https://github.com/columnar-tech/dbc/issues/37.                                 \n"+
			"test-driver-docs-url         This is manifest-only with its docs_url key set                                                                                   ", suite.runCmd(m))
}
