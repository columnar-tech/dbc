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

func mockBrowserOpen(openedURL *string) func(string) error {
	return func(url string) error {
		*openedURL = url
		return nil
	}
}

func mockBrowserOpenError() func(string) error {
	return func(url string) error {
		return fmt.Errorf("browser not available")
	}
}

func (suite *SubcommandTestSuite) TestDocNoDriver() {
	var openedURL string

	m := DocCmd{Driver: ""}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
			openBrowser:   mockBrowserOpen(&openedURL),
		},
		false, // Don't prompt in tests
	)

	out := suite.runCmd(m)
	suite.Contains(out, "Opening documentation in browser...")
	suite.Equal("https://docs.columnar.tech/dbc/", openedURL)
}

func (suite *SubcommandTestSuite) TestDocDriverFound() {
	var openedURL string

	m := DocCmd{Driver: "test-driver-1"}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
			openBrowser:   mockBrowserOpen(&openedURL),
		},
		false, // Don't prompt in tests
	)

	out := suite.runCmd(m)
	suite.Contains(out, "Opening documentation in browser...")
	suite.Equal("https://docs.columnar.tech/dbc/?driver=test-driver-1", openedURL)
}

func (suite *SubcommandTestSuite) TestDocDriverNotFound() {
	var openedURL string

	m := DocCmd{Driver: "nonexistent-driver"}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
			openBrowser:   mockBrowserOpen(&openedURL),
		},
		false, // Don't prompt in tests
	)

	out := suite.runCmdErr(m)
	suite.Contains(out, "driver `nonexistent-driver` not found in driver registry index")
}

func (suite *SubcommandTestSuite) TestDocBrowserOpenError() {
	m := DocCmd{Driver: "test-driver-1"}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
			openBrowser:   mockBrowserOpenError(),
		},
		false, // Don't prompt in tests
	)

	out := suite.runCmdErr(m)
	suite.Contains(out, "failed to open browser: browser not available")
}
