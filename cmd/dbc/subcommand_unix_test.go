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

//go:build !windows

package main

import (
	"os"

	"github.com/columnar-tech/dbc/config"
)

func (suite *SubcommandTestSuite) TearDownTest() {
	suite.Require().NoError(os.Unsetenv("ADBC_DRIVER_PATH"))

	// Clean up filesystem after each test
	_, user := os.LookupEnv("DBC_TEST_LEVEL_USER")
	_, system := os.LookupEnv("DBC_TEST_LEVEL_SYSTEM")
	if user {
		suite.Require().NoError(os.RemoveAll(config.GetLocation(config.ConfigUser)))
	}
	if system {
		suite.Require().NoError(os.RemoveAll(config.GetLocation(config.ConfigSystem)))
	}
}
