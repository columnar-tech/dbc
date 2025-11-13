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
	"fmt"
)

var testFallbackUrls = map[string]string{
	"test-driver-1": "https://test.example.com/driver1",
}

var lastOpenedURL string

func mockOpenBrowserSuccess(url string) error {
	lastOpenedURL = url
	return nil
}

func mockOpenBrowserError(url string) error {
	return fmt.Errorf("browser not available")
}

// No-open mode tests - should print URL instead of opening browser
func (suite *SubcommandTestSuite) TestDocsNoOpenNoDriver() {
	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""
	fallbackDriverDocsUrl = testFallbackUrls

	m := DocsCmd{Driver: "", NoOpen: true}.GetModel()
	output := suite.runCmd(m)

	suite.Contains(output, "dbc docs are available at the following URL:\nhttps://docs.columnar.tech/dbc/")
	suite.Equal("", lastOpenedURL, "browser should not be opened with --no-open")
}

func (suite *SubcommandTestSuite) TestDocsNoOpenDriverFound() {
	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""
	fallbackDriverDocsUrl = testFallbackUrls

	m := DocsCmd{Driver: "test-driver-1", NoOpen: true}.GetModel()
	output := suite.runCmd(m)

	suite.Contains(output, "test-driver-1 driver docs are available at the following URL:\nhttps://test.example.com/driver1")
	suite.Equal("", lastOpenedURL, "browser should not be opened with --no-open")
}

func (suite *SubcommandTestSuite) TestDocsNoOpenDriverNotInFallbackMap() {
	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""
	fallbackDriverDocsUrl = testFallbackUrls

	m := DocsCmd{Driver: "test-driver-2", NoOpen: true}.GetModel()
	output := suite.runCmdErr(m)

	suite.Contains(output, "no documentation available for driver `test-driver-2`")
}

func (suite *SubcommandTestSuite) TestDocsNoOpenDriverNotFound() {
	openBrowserFunc = mockOpenBrowserSuccess
	lastOpenedURL = ""
	fallbackDriverDocsUrl = testFallbackUrls

	m := DocsCmd{Driver: "nonexistent-driver", NoOpen: true}.GetModel()
	output := suite.runCmdErr(m)

	suite.Contains(output, "driver `nonexistent-driver` not found in driver registry index")
}

// Interactive mode tests - should open browser
func (suite *SubcommandTestSuite) TestDocsInteractiveNoDriver() {
	lastOpenedURL = ""

	m := DocsCmd{Driver: ""}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
		},
		false, // noOpen = false
		mockOpenBrowserSuccess,
		testFallbackUrls,
	)
	suite.runCmd(m)

	suite.Equal("https://docs.columnar.tech/dbc/", lastOpenedURL)
}

func (suite *SubcommandTestSuite) TestDocsInteractiveDriverFound() {
	lastOpenedURL = ""

	m := DocsCmd{Driver: "test-driver-1"}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
		},
		false, // noOpen = false
		mockOpenBrowserSuccess,
		testFallbackUrls,
	)
	suite.runCmd(m)

	suite.Equal("https://test.example.com/driver1", lastOpenedURL)
}

func (suite *SubcommandTestSuite) TestDocsInteractiveDriverNotInFallbackMap() {
	lastOpenedURL = ""

	m := DocsCmd{Driver: "test-driver-2"}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
		},
		false, // noOpen = false
		mockOpenBrowserSuccess,
		testFallbackUrls,
	)
	output := suite.runCmdErr(m)

	suite.Contains(output, "no documentation available for driver `test-driver-2`")
	suite.Equal("", lastOpenedURL, "browser should not be opened on error")
}

func (suite *SubcommandTestSuite) TestDocsInteractiveBrowserOpenError() {
	lastOpenedURL = ""

	m := DocsCmd{Driver: "test-driver-1"}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
		},
		false, // noOpen = false
		mockOpenBrowserError,
		testFallbackUrls,
	)
	output := suite.runCmdErr(m)

	suite.Contains(output, "failed to open browser: browser not available")
}
