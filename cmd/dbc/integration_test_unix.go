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

//go:build test_integration && !windows

package main

import (
	"os"
	"path/filepath"

	"github.com/columnar-tech/dbc/config"
)

func (suite *IntegrationTestSuite) TearDownTest() {
	os.RemoveAll(config.GetLocation(config.ConfigUser))
	os.RemoveAll(config.GetLocation(config.ConfigSystem))
}

func (suite *IntegrationTestSuite) driverIsInstalled(level config.ConfigLevel, path string) {
	manifestPath := filepath.Join(cfg.Location, path+".toml")
	suite.FileExists(manifestPath)

	driverInfo, err := config.GetDriver(cfg, path)
	suite.Require().NoError(err, "should be able to load driver from manifest")
	driverPath := driverInfo.Driver.Shared.Get(config.PlatformTuple())

	suite.FileExists(driverPath)
}
