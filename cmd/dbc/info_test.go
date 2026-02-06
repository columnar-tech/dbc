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

	suite.validateOutput("\r ", "\nError: driver `non-existent-driver` not found in driver registry index", out)
}

func (suite *SubcommandTestSuite) TestInfoPartialRegistryFailure() {
	// Test that info command handles partial registry failure gracefully
	// (one registry succeeds, another fails - returns both drivers and error)
	partialFailingRegistry := func() ([]dbc.Driver, error) {
		// Get drivers from the test registry (simulating one successful registry)
		drivers, _ := getTestDriverRegistry()
		// But also return an error (simulating another registry that failed)
		return drivers, fmt.Errorf("registry https://secondary-registry.example.com: failed to fetch driver registry: DNS error")
	}

	// Should succeed if the requested driver is found in the available drivers
	m := InfoCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverRegistry: partialFailingRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmd(m)
	// Should display info successfully without printing the registry error
	suite.Contains(out, "Driver: test-driver-1")
	suite.Contains(out, "Version: 1.1.0")
}

func (suite *SubcommandTestSuite) TestInfoPartialRegistryFailureDriverNotFound() {
	// Test that info command shows registry errors when the requested driver is not found
	partialFailingRegistry := func() ([]dbc.Driver, error) {
		// Get drivers from the test registry (simulating one successful registry)
		drivers, _ := getTestDriverRegistry()
		// But also return an error (simulating another registry that failed)
		return drivers, fmt.Errorf("registry https://secondary-registry.example.com: failed to fetch driver registry: DNS error")
	}

	// Should fail with enhanced error message if the requested driver is not found
	m := InfoCmd{Driver: "nonexistent-driver"}.
		GetModelCustom(baseModel{getDriverRegistry: partialFailingRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmdErr(m)
	// Should show the driver not found error AND the registry error
	suite.Contains(out, "driver `nonexistent-driver` not found")
	suite.Contains(out, "Note: Some driver registries were unavailable")
	suite.Contains(out, "failed to fetch driver registry")
	suite.Contains(out, "DNS error")
}

func (suite *SubcommandTestSuite) TestInfoCompleteRegistryFailure() {
	// Test that info command handles complete registry failure (no drivers returned)
	completeFailingRegistry := func() ([]dbc.Driver, error) {
		return nil, fmt.Errorf("registry https://primary-registry.example.com: network unreachable")
	}

	m := InfoCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverRegistry: completeFailingRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmdErr(m)
	suite.Contains(out, "network unreachable")
}
