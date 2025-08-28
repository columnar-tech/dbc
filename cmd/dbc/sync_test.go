// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/columnar-tech/dbc"
	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"
)

func getTestDriverList() ([]dbc.Driver, error) {
	drivers := struct {
		Drivers []dbc.Driver `yaml:"drivers"`
	}{}

	f, err := os.Open("testdata/test_manifest.yaml")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return drivers.Drivers, yaml.NewDecoder(f).Decode(&drivers)
}

func downloadTestPkg(pkg dbc.PkgInfo) (*os.File, error) {
	switch pkg.Driver.Path {
	case "test-driver-1":
		if pkg.Version.Minor() == 1 {
			return os.Open(filepath.Join("testdata", "test-driver-1.1.tar.gz"))
		}
		return os.Open(filepath.Join("testdata", "test-driver-1.tar.gz"))
	case "test-driver-2":
		return os.Open(filepath.Join("testdata", "test-driver-2.tar.gz"))
	case "test-driver-manifest-only":
		return os.Open(filepath.Join("testdata", "test-driver-manifest-only.tar.gz"))
	default:
		return nil, fmt.Errorf("unknown driver: %s", pkg.Driver.Path)
	}
}

type SubcommandTestSuite struct {
	suite.Suite

	getDriverListFn func() ([]dbc.Driver, error)
	tempdir         string
}

func (suite *SubcommandTestSuite) SetupSuite() {
	suite.getDriverListFn = getDriverList
	getDriverList = getTestDriverList
}

func (suite *SubcommandTestSuite) SetupTest() {
	suite.tempdir = suite.T().TempDir()
	suite.Require().NoError(os.Setenv("ADBC_CONFIG_PATH", suite.tempdir))
}

func (suite *SubcommandTestSuite) TearDownTest() {
	suite.Require().NoError(os.Unsetenv("ADBC_CONFIG_PATH"))
}

func (suite *SubcommandTestSuite) TearDownSuite() {
	getDriverList = suite.getDriverListFn
}

func (suite *SubcommandTestSuite) runCmdErr(m tea.Model) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out bytes.Buffer
	p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
		tea.WithContext(ctx), tea.WithEnvironment(append(os.Environ(), "TERM=linux")))

	var err error
	m, err = p.Run()
	suite.Require().NoError(err)
	suite.Equal(1, m.(HasStatus).Status(), "The subcommand did not exit with a status of 1 as expected.")
	return out.String()
}

func (suite *SubcommandTestSuite) runCmd(m tea.Model) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out bytes.Buffer
	p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
		tea.WithContext(ctx), tea.WithEnvironment(append(os.Environ(), "TERM=linux")))

	var err error
	m, err = p.Run()
	suite.Require().NoError(err)
	suite.Equal(0, m.(HasStatus).Status(), "The command exited with a non-zero status.")
	return out.String()
}

func (suite *SubcommandTestSuite) validateOutput(expected, actual string) {
	const (
		terminalPrefix = "\x1b[?25l\x1b[?2004h"
		terminalSuffix = "\x1b[D\x1b[2K\r\x1b[?2004l\x1b[?25h\x1b[?1002l\x1b[?1003l\x1b[?1006l"
	)

	suite.Equal(terminalPrefix+expected+terminalSuffix, actual)
}

func (suite *SubcommandTestSuite) TestSync() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: "test-driver-1"}.GetModel()
	suite.runCmd(m)

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("✓ test-driver-1-1.1.0 already installed\r\n\rDone!\r\n", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSyncVirtualEnv() {
	os.Unsetenv("ADBC_CONFIG_PATH")

	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: "test-driver-1"}.GetModel()
	suite.runCmd(m)

	os.Setenv("VIRTUAL_ENV", suite.tempdir)
	defer os.Unsetenv("VIRTUAL_ENV")

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "etc", "adbc", "test-driver-1.toml"))

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("✓ test-driver-1-1.1.0 already installed\r\n\rDone!\r\n", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSyncCondaPrefix() {
	os.Unsetenv("ADBC_CONFIG_PATH")

	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: "test-driver-1"}.GetModel()
	suite.runCmd(m)

	os.Setenv("CONDA_PREFIX", suite.tempdir)
	defer os.Unsetenv("CONDA_PREFIX")

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "etc", "adbc", "test-driver-1.toml"))

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("✓ test-driver-1-1.1.0 already installed\r\n\rDone!\r\n", suite.runCmd(m))
}

func TestSubcommands(t *testing.T) {
	suite.Run(t, new(SubcommandTestSuite))
}
