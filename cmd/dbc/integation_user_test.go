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

//go:build test_integration && test_user

package main

import (
	"path/filepath"

	"github.com/columnar-tech/dbc/config"
)

func (suite *IntegrationTestSuite) TestInstallUser() {
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigUser}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.run(m)

	loc := config.GetLocation(config.ConfigUser)
	suite.Equal("\nInstalled test-driver-1 1.1.0 to "+loc+"\n", out)
	suite.driverIsInstalled(config.ConfigUser, "test-driver-1")
	suite.FileExists(filepath.Join(loc, "test-driver-1.1", "test-driver-1-not-valid.so"))
	suite.FileExists(filepath.Join(loc, "test-driver-1.1", "test-driver-1-not-valid.so.sig"))
}
