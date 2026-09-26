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

//go:build windows && test_registry

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/config"
	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/windows/registry"
)

// This test suite is only run when the "test_registry" build tag is set.
// Only run these tests if you're able to modify the windows registry and won't be broken
// if the ADBC registry keys are cleared/modified/etc.
type RegistryTestSuite struct {
	suite.Suite

	cfgUserPath string
}

func (s *RegistryTestSuite) run(m tea.Model) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out bytes.Buffer
	p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
		tea.WithContext(ctx), tea.WithoutRenderer())

	var err error
	m, err = p.Run()
	s.Require().NoError(err)

	var extra string
	if fo, ok := m.(HasFinalOutput); ok {
		extra = fo.FinalOutput()
	}
	status := m.(HasStatus)
	s.Equal(0, status.Status(), "exited with a non-zero status: error=%v, final output=%q, captured output=%q",
		status.Err(), extra, out.String())
	return out.String() + extra
}

func (s *RegistryTestSuite) clearRegistry() {
	// Clear out any existing ADBC registry keys to ensure a clean slate.
	k, err := registry.OpenKey(registry.CURRENT_USER, "SOFTWARE\\ADBC\\Drivers", registry.ALL_ACCESS)
	if errors.Is(err, registry.ErrNotExist) {
		return
	}
	s.Require().NoError(err)
	defer k.Close()

	names, err := k.ReadSubKeyNames(-1) // Ensure the key is readable
	s.Require().NoError(err)
	for _, name := range names {
		s.Require().NoError(registry.DeleteKey(k, name))
	}
}

func (s *RegistryTestSuite) SetupSuite() {
	s.cfgUserPath = config.Get()[config.ConfigUser].Location
	os.RemoveAll(s.cfgUserPath)
}

func (s *RegistryTestSuite) TearDownSuite() {
	s.clearRegistry()
	os.RemoveAll(s.cfgUserPath)
}

func (s *RegistryTestSuite) SetupTest() {
	s.clearRegistry()
}

func (s *RegistryTestSuite) TearDownTest() {
	os.RemoveAll(s.cfgUserPath)
}

func (s *RegistryTestSuite) assertRegisteredPackagePath() {
	k, err := registry.OpenKey(registry.CURRENT_USER, "SOFTWARE\\ADBC\\Drivers\\test-driver-1", registry.READ)
	s.Require().NoError(err)
	defer k.Close()

	path, _, err := k.GetStringValue("driver")
	s.Require().NoError(err)
	s.FileExists(path)

	receipt, managed, present, valid := config.InspectInstallReceipt(s.cfgUserPath, "test-driver-1", path)
	s.Require().True(managed, "registry driver path should point into a dbc-managed package generation: %s", path)
	s.Require().True(present, "managed package generation should contain an install receipt: %s", path)
	s.Require().True(valid, "managed package receipt should match the registered library path: %s", path)
	s.Equal("test-driver-1", receipt.DriverID)
	s.Equal("1.1.0", receipt.DriverVersion)
}

func (s *RegistryTestSuite) TestInstallDriver() {
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigUser}.
		GetModelCustom(testBaseModel())
	out := s.run(m)
	s.Equal("\nInstalled test-driver-1 1.1.0 to "+s.cfgUserPath, out)

	k, err := registry.OpenKey(registry.CURRENT_USER, "SOFTWARE\\ADBC\\Drivers\\test-driver-1", registry.READ)
	s.Require().NoError(err)
	defer k.Close()

	// Verify the registry key was created with the expected values.
	val, _, err := k.GetStringValue("version")
	s.Require().NoError(err)
	s.Equal("1.1.0", val)

	s.assertRegisteredPackagePath()

	installed, err := config.GetDriver(config.Config{Level: config.ConfigUser, Location: s.cfgUserPath}, "test-driver-1")
	s.Require().NoError(err)
	s.Require().NotNil(installed.AdbcInfo.Version)
	s.Equal("1.1.0", installed.AdbcInfo.Version.String())
}

func (s *RegistryTestSuite) TestRegistryPersistsAndClearsADBCMetadata() {
	cfg := config.Config{Level: config.ConfigUser, Location: s.cfgUserPath}
	driver := config.DriverInfo{
		ID: "test-registry-metadata", Name: "Test Registry Metadata", Publisher: "Example",
		License: "Apache-2.0", Source: "dbc", Version: semver.MustParse("1.2.3"),
	}
	driver.AdbcInfo.Version = semver.MustParse("0.9.1")
	driver.AdbcInfo.Features.Supported = []string{"feature-b", "feature-a"}
	driver.AdbcInfo.Features.Unsupported = []string{"feature-c"}
	driver.Driver.Entrypoint = "AdbcDriverInit"
	driver.Driver.Shared.Set(config.PlatformTuple(), filepath.Join(s.cfgUserPath, "driver.dll"))
	s.Require().NoError(config.CreateManifest(cfg, driver))

	loaded, err := config.GetDriver(cfg, driver.ID)
	s.Require().NoError(err)
	s.Require().NotNil(loaded.AdbcInfo.Version)
	s.Equal("0.9.1", loaded.AdbcInfo.Version.String())
	s.Equal([]string{"feature-b", "feature-a"}, loaded.AdbcInfo.Features.Supported)
	s.Equal([]string{"feature-c"}, loaded.AdbcInfo.Features.Unsupported)

	// Replacing a registration without optional ADBC metadata must remove old
	// registry values instead of leaving stale values that change its identity.
	driver.AdbcInfo.Version = nil
	driver.AdbcInfo.Features.Supported = nil
	driver.AdbcInfo.Features.Unsupported = nil
	s.Require().NoError(config.CreateManifest(cfg, driver))
	loaded, err = config.GetDriver(cfg, driver.ID)
	s.Require().NoError(err)
	s.Nil(loaded.AdbcInfo.Version)
	s.Empty(loaded.AdbcInfo.Features.Supported)
	s.Empty(loaded.AdbcInfo.Features.Unsupported)

	k, err := registry.OpenKey(registry.CURRENT_USER, "SOFTWARE\\ADBC\\Drivers\\"+driver.ID, registry.ALL_ACCESS)
	s.Require().NoError(err)
	defer k.Close()
	s.Require().NoError(k.SetStringValue("adbc_version", "not-a-version"))
	_, err = config.GetDriver(cfg, driver.ID)
	s.ErrorContains(err, "invalid ADBC version in registry")
	s.Require().NoError(k.SetStringValue("adbc_version", "0.9.1"))
	s.Require().NoError(k.SetStringValue("adbc_supported_features", "wrong-registry-type"))
	_, err = config.GetDriver(cfg, driver.ID)
	s.Error(err, "a present feature value with the wrong registry type must be rejected")
}

func (s *RegistryTestSuite) TestPartialReinstallDriver() {
	// First install the driver normally.
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigUser}.
		GetModelCustom(testBaseModel())
	out := s.run(m)
	s.Equal("\nInstalled test-driver-1 1.1.0 to "+s.cfgUserPath, out)

	s.clearRegistry()

	// Now reinstall the driver, which should succeed even though the registry key is missing.
	m = InstallCmd{Driver: "test-driver-1", Level: config.ConfigUser}.
		GetModelCustom(testBaseModel())
	out = s.run(m)
	s.Equal("\nInstalled test-driver-1 1.1.0 to "+s.cfgUserPath, out)
	s.assertRegisteredPackagePath()
}

func TestRegistryKeyHandling(t *testing.T) {
	suite.Run(t, new(RegistryTestSuite))
}
