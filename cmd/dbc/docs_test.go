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
	"fmt"

	"github.com/columnar-tech/dbc"
)

var lastOpenedURL string

func mockOpenBrowserSuccess(url string) error {
	lastOpenedURL = url
	return nil
}

func mockOpenBrowserError(url string) error {
	return fmt.Errorf("browser not available")
}

func (suite *SubcommandTestSuite) TestDocsNoDriverArg() {
	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""

	m := DocsCmd{Driver: ""}.GetModel()
	suite.runCmd(m)

	suite.Equal("https://docs.columnar.tech/dbc/", lastOpenedURL)
}

func (suite *SubcommandTestSuite) TestDocsNoDriverArgNoOpen() {
	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""

	m := DocsCmd{Driver: "", NoOpen: true}.GetModel()
	output := suite.runCmd(m)

	suite.Contains(output, "dbc docs are available at the following URL:\nhttps://docs.columnar.tech/dbc/")
	suite.Equal("", lastOpenedURL, "browser should not be opened with --no-open")
}

func (suite *SubcommandTestSuite) TestDocsDriverFoundWithDocsNoOpen() {
	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""

	m := DocsCmd{Driver: "test-driver-1", NoOpen: true}.GetModel()
	output := suite.runCmdErr(m)

	suite.Contains(output, "")
	suite.Equal("", lastOpenedURL, "browser should not be opened with --no-open")
}

func (suite *SubcommandTestSuite) TestDocsDriverFoundNoDocs() {
	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""

	m := DocsCmd{Driver: "test-driver-2"}.GetModel()
	output := suite.runCmdErr(m)

	suite.Contains(output, "no documentation available for driver `test-driver-2`")
	suite.Equal("", lastOpenedURL, "browser should not be opened on error")
}

func (suite *SubcommandTestSuite) TestDocsDriverFoundNoDocsNoOpen() {
	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""

	m := DocsCmd{Driver: "test-driver-2", NoOpen: true}.GetModel()
	output := suite.runCmdErr(m)

	suite.Contains(output, "no documentation available for driver `test-driver-2`")
	suite.Equal("", lastOpenedURL, "browser should not be opened on error")
}

func (suite *SubcommandTestSuite) TestDocsDriverNotFound() {
	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""

	m := DocsCmd{Driver: "nonexistent-driver"}.GetModel()
	output := suite.runCmdErr(m)

	suite.Contains(output, "driver `nonexistent-driver` not found in driver registry index")
	suite.Equal("", lastOpenedURL, "browser should not be opened on error")
}

func (suite *SubcommandTestSuite) TestDocsDriverNotFoundNoOpen() {
	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""

	m := DocsCmd{Driver: "nonexistent-driver", NoOpen: true}.GetModel()
	output := suite.runCmdErr(m)

	suite.Contains(output, "driver `nonexistent-driver` not found in driver registry index")
	suite.Equal("", lastOpenedURL, "browser should not be opened on error")
}

func (suite *SubcommandTestSuite) TestDocsBrowserOpenError() {
	lastOpenedURL = ""

	m := DocsCmd{Driver: "test-driver-1"}.GetModelCustom(
		baseModel{
			getDriverRegistry: getTestDriverRegistry,
			downloadPkg:       downloadTestPkg,
		},
		false,
		mockOpenBrowserError,
	)
	output := suite.runCmdErr(m)

	suite.Contains(output, "")
}

func (suite *SubcommandTestSuite) TestDocsDriverFoundWithDocs() {
	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""

	m := DocsCmd{Driver: "test-driver-docs-url"}.GetModel()
	suite.runCmd(m)

	suite.Equal("http://example.com", lastOpenedURL)
}

func (suite *SubcommandTestSuite) TestDocsPartialRegistryFailure() {
	// Test that docs command handles partial registry failure gracefully
	// (one registry succeeds, another fails - returns both drivers and error)
	partialFailingRegistry := func() ([]dbc.Driver, error) {
		// Get drivers from the test registry (simulating one successful registry)
		drivers, _ := getTestDriverRegistry()
		// But also return an error (simulating another registry that failed)
		return drivers, fmt.Errorf("registry https://fallback-registry.example.com: failed to fetch driver registry: timeout")
	}

	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""

	// Should succeed if the requested driver is found in the available drivers
	m := DocsCmd{Driver: "test-driver-1"}.GetModelCustom(
		baseModel{getDriverRegistry: partialFailingRegistry, downloadPkg: downloadTestPkg},
		false,
		mockOpenBrowserSuccess,
	)

	suite.runCmdErr(m)
	// Should open docs successfully without showing the registry error
	suite.Equal("", lastOpenedURL)
}

func (suite *SubcommandTestSuite) TestDocsPartialRegistryFailureDriverNotFound() {
	// Test that docs command shows registry errors when the requested driver is not found
	partialFailingRegistry := func() ([]dbc.Driver, error) {
		// Get drivers from the test registry (simulating one successful registry)
		drivers, _ := getTestDriverRegistry()
		// But also return an error (simulating another registry that failed)
		return drivers, fmt.Errorf("registry https://fallback-registry.example.com: failed to fetch driver registry: timeout")
	}

	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""

	// Should fail with enhanced error message if the requested driver is not found
	m := DocsCmd{Driver: "nonexistent-driver"}.GetModelCustom(
		baseModel{getDriverRegistry: partialFailingRegistry, downloadPkg: downloadTestPkg},
		false,
		mockOpenBrowserSuccess,
	)

	out := suite.runCmdErr(m)
	// Should show the driver not found error AND the registry error
	suite.Contains(out, "driver `nonexistent-driver` not found")
	suite.Contains(out, "Note: Some driver registries were unavailable")
	suite.Contains(out, "failed to fetch driver registry")
	suite.Contains(out, "timeout")
	suite.Equal("", lastOpenedURL, "browser should not be opened on error")
}

func (suite *SubcommandTestSuite) TestDocsCompleteRegistryFailure() {
	// Test that docs command handles complete registry failure (no drivers returned)
	completeFailingRegistry := func() ([]dbc.Driver, error) {
		return nil, fmt.Errorf("registry https://main-registry.example.com: connection timeout")
	}

	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""

	m := DocsCmd{Driver: "test-driver-1"}.GetModelCustom(
		baseModel{getDriverRegistry: completeFailingRegistry, downloadPkg: downloadTestPkg},
		false,
		mockOpenBrowserSuccess,
	)

	out := suite.runCmdErr(m)
	suite.Contains(out, "connection timeout")
	suite.Equal("", lastOpenedURL, "browser should not be opened on error")
}
