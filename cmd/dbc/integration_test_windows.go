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

//go:build test_integration && windows

package main

import (
	"os"

	"github.com/columnar-tech/dbc/config"
	"golang.org/x/sys/windows/registry"
)

func (suite *IntegrationTestSuite) TearDownTest() {
	// Clean up the registry and filesystem between each test
	//
	// `regKeyADBC` isn't exported, we redefine it here
	regKeyADBC := "SOFTWARE\\ADBC\\Drivers"
	// We ignore registry errors. The `registry` package doesn't have a method to
	// explicitly check key existence we could either check the error or just try
	// and ignore
	registry.DeleteKey(registry.CURRENT_USER, regKeyADBC)
	registry.DeleteKey(registry.LOCAL_MACHINE, regKeyADBC)

	// Clean up filesystem
	os.RemoveAll(config.GetLocation(config.ConfigUser))
	os.RemoveAll(config.GetLocation(config.ConfigSystem))
}

func (suite *IntegrationTestSuite) driverIsInstalled(level config.ConfigLevel, driverID string) {
	var rootKey registry.Key
	if level == config.ConfigUser {
		rootKey = registry.CURRENT_USER
	} else {
		rootKey = registry.LOCAL_MACHINE
	}

	k, err := registry.OpenKey(rootKey, `SOFTWARE\ADBC\Drivers\`+driverID, registry.QUERY_VALUE)
	suite.Require().NoError(err, "registry key should exist")
	defer k.Close()

	// Get and verify driver path exists
	driverPath, _, err := k.GetStringValue("driver")
	suite.Require().NoError(err)
	suite.FileExists(driverPath)
}
