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

//go:build test_integration

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/columnar-tech/dbc/config"
	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/windows/registry"
)

type IntegrationTestSuite struct {
	suite.Suite
}

func (suite *IntegrationTestSuite) SetupSuite() {
	// Integration tests require both the build tag AND environment variable
	if os.Getenv("DBC_RUN_INTEGRATION_TESTS") == "" {
		suite.T().Skip("Set DBC_RUN_INTEGRATION_TESTS=1 to run integration tests")
	}
}

func (s *IntegrationTestSuite) run(m tea.Model) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out bytes.Buffer
	p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
		tea.WithContext(ctx), tea.WithoutRenderer())

	var err error
	m, err = p.Run()
	s.Require().NoError(err)
	s.Equal(0, m.(HasStatus).Status(), "exited with a non-zero status")

	var extra string
	if fo, ok := m.(HasFinalOutput); ok {
		extra = fo.FinalOutput()
	}
	return out.String() + extra
}

// This test suite only runs when the "test_integration" build tag is set. These
// tests are intended to only be run in VMs or on CI because they may modify
// user and system files and the Windows registry.
func TestIntegration(t *testing.T) {
	suite.Run(t, new(IntegrationTestSuite))
}

func (suite *IntegrationTestSuite) TestInstallUser() {
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigUser}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.run(m)
	loc := config.GetLocation(config.ConfigUser)

	suite.Equal("\nInstalled test-driver-1 1.1.0 to "+loc+"\n", out)
	if runtime.GOOS != "windows" {
		suite.FileExists(filepath.Join(loc, "test-driver-1.toml"))
	} else {
		k, err := registry.OpenKey(registry.CURRENT_USER, `SOFTWARE\ADBC\Drivers\test-driver-1`, registry.QUERY_VALUE)
		suite.Require().NoError(err, "registry key should exist")
		defer k.Close()
	}
	suite.FileExists(filepath.Join(loc, "test-driver-1.1", "test-driver-1-not-valid.so"))
	suite.FileExists(filepath.Join(loc, "test-driver-1.1", "test-driver-1-not-valid.so.sig"))
}

func (suite *IntegrationTestSuite) TestInstallSystem() {
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigSystem}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.run(m)
	loc := config.GetLocation(config.ConfigSystem)

	suite.Equal("\nInstalled test-driver-1 1.1.0 to "+loc+"\n", out)
	if runtime.GOOS != "windows" {
		suite.FileExists(filepath.Join(loc, "test-driver-1.toml"))
	} else {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\ADBC\Drivers\test-driver-1`, registry.QUERY_VALUE)
		suite.Require().NoError(err, "registry key should exist")
		defer k.Close()
	}
	suite.FileExists(filepath.Join(loc, "test-driver-1.1", "test-driver-1-not-valid.so"))
	suite.FileExists(filepath.Join(loc, "test-driver-1.1", "test-driver-1-not-valid.so.sig"))
}
