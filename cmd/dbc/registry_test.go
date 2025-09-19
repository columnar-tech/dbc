// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

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

	tea "github.com/charmbracelet/bubbletea"
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
	s.Equal(0, m.(HasStatus).Status(), "exited with a non-zero status")

	var extra string
	if fo, ok := m.(HasFinalOutput); ok {
		extra = fo.FinalOutput()
	}
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

func (s *RegistryTestSuite) SetupTest() {
	s.clearRegistry()
}

func (s *RegistryTestSuite) TearDownTest() {
	os.RemoveAll(s.cfgUserPath)
}

func (s *RegistryTestSuite) TestInstallDriver() {
	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	out := s.run(m)
	s.Equal("\nInstalled test-driver-1 1.1.0 to "+s.cfgUserPath+"\n", out)

	k, err := registry.OpenKey(registry.CURRENT_USER, "SOFTWARE\\ADBC\\Drivers\\test-driver-1", registry.READ)
	s.Require().NoError(err)
	defer k.Close()

	// Verify the registry key was created with the expected values.
	val, _, err := k.GetStringValue("version")
	s.Require().NoError(err)
	s.Equal("1.1.0", val)

	val, _, err = k.GetStringValue("driver")
	s.Require().NoError(err)
	s.Equal(filepath.Join(s.cfgUserPath, "test-driver-1.1", "test-driver-1-not-valid.so"), val)
}

func (s *RegistryTestSuite) TestPartialReinstallDriver() {
	// First install the driver normally.
	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	out := s.run(m)
	s.Equal("\nInstalled test-driver-1 1.1.0 to "+s.cfgUserPath+"\n", out)

	s.clearRegistry()

	// Now reinstall the driver, which should succeed even though the registry key is missing.
	m = InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	out = s.run(m)
	s.Equal("\nInstalled test-driver-1 1.1.0 to "+s.cfgUserPath+"\n", out)
}

func TestRegistryKeyHandling(t *testing.T) {
	suite.Run(t, new(RegistryTestSuite))
}
