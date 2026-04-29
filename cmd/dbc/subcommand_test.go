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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/go-faster/yaml"
	"github.com/stretchr/testify/suite"
)

var testRegistry = dbc.Registry{
	Name:    "",
	BaseURL: must(url.Parse("https://registry.columnar.tech")),
}

func getTestDriverRegistry() ([]dbc.Driver, error) {
	drivers := struct {
		Drivers []dbc.Driver `yaml:"drivers"`
	}{}

	f, err := os.Open("testdata/test_index.yaml")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if err := yaml.NewDecoder(f).Decode(&drivers); err != nil {
		return nil, err
	}
	for i := range drivers.Drivers {
		drivers.Drivers[i].Registry = &testRegistry
	}
	return drivers.Drivers, nil
}

func testBaseModel() baseModel {
	return baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg}
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
	case "test-driver-no-sig":
		return os.Open(filepath.Join("testdata", "test-driver-no-sig.tar.gz"))
	case "test-driver-invalid-manifest":
		return os.Open(filepath.Join("testdata", "test-driver-invalid-manifest.tar.gz"))
	case "test-driver-only-pre":
		return os.Open(filepath.Join("testdata", "test-driver-only-pre.tar.gz"))
	default:
		return nil, fmt.Errorf("unknown driver: %s", pkg.Driver.Path)
	}
}

type SubcommandTestSuite struct {
	suite.Suite

	getDriverRegistryFn   func() ([]dbc.Driver, error)
	openBrowserFn         func(string) error
	fallbackDriverDocsUrl map[string]string
	tempdir               string

	configLevel config.ConfigLevel
}

func (suite *SubcommandTestSuite) SetupSuite() {
	suite.getDriverRegistryFn = getDriverRegistry
	getDriverRegistry = getTestDriverRegistry
	suite.openBrowserFn = openBrowserFunc
	suite.fallbackDriverDocsUrl = fallbackDriverDocsUrl

	if suite.configLevel == config.ConfigUnknown {
		suite.configLevel = config.ConfigEnv
	}
}

func (suite *SubcommandTestSuite) SetupTest() {
	suite.tempdir = suite.T().TempDir()
	suite.T().Setenv("ADBC_DRIVER_PATH", suite.tempdir)
}

func (suite *SubcommandTestSuite) TearDownSuite() {
	getDriverRegistry = suite.getDriverRegistryFn
	openBrowserFunc = suite.openBrowserFn
	fallbackDriverDocsUrl = suite.fallbackDriverDocsUrl
}

func (suite *SubcommandTestSuite) getFilesInTempDir() []string {
	var filelist []string
	suite.NoError(fs.WalkDir(os.DirFS(suite.tempdir), ".", func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			return nil
		}
		// Skip advisory lock files created by fslock
		if strings.HasSuffix(path, ".lock") {
			return nil
		}
		filelist = append(filelist, path)
		return nil
	}))
	return filelist
}

func (suite *SubcommandTestSuite) getDriverFilesInTempDir() []string {
	var filelist []string
	for _, f := range suite.getFilesInTempDir() {
		if f != ".dbc.install.lock" && f != ".dbc.project.lock" {
			filelist = append(filelist, f)
		}
	}
	return filelist
}

// Get the base directory for where drivers are installed. Use this instead of
// hardcoding checks to suite.tempdir to make tests support other config levels.
func (suite *SubcommandTestSuite) Dir() string {
	if suite.configLevel == config.ConfigEnv {
		return suite.tempdir
	}
	return suite.configLevel.ConfigLocation()
}

// HasJSONWriter is implemented by models that stream JSON events directly to an io.Writer.
type HasJSONWriter interface {
	WithJSONWriter(io.Writer) tea.Model
}

func (suite *SubcommandTestSuite) runCmdErr(m tea.Model) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out bytes.Buffer
	if jw, ok := m.(HasJSONWriter); ok {
		m = jw.WithJSONWriter(&out)
	}
	prog = tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
		tea.WithoutRenderer(), tea.WithContext(ctx))
	defer func() {
		prog = nil
	}()

	var err error
	m, err = prog.Run()
	prog.Wait()
	suite.Require().NoError(err)
	suite.Equal(1, m.(HasStatus).Status(), "The subcommand did not exit with a status of 1 as expected.")

	// Append FinalOutput so JSON error envelopes (stored in model state) are
	// included in the returned string, mirroring main.go's behaviour.
	var extra string
	if fo, ok := m.(HasFinalOutput); ok {
		extra = fo.FinalOutput()
	}

	// Mirror main.go: suppress plaintext error formatting in JSON mode.
	inJSONMode := false
	if jm, ok := m.(interface{ IsJSONMode() bool }); ok {
		inJSONMode = jm.IsJSONMode()
	}
	if cmdErr := m.(HasStatus).Err(); cmdErr != nil && !inJSONMode {
		extra += "\n" + formatErr(cmdErr)
	}
	return ansi.Strip(out.String() + extra)
}

func (suite *SubcommandTestSuite) runCmd(m tea.Model) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out bytes.Buffer
	if jw, ok := m.(HasJSONWriter); ok {
		m = jw.WithJSONWriter(&out)
	}
	prog = tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
		tea.WithoutRenderer(), tea.WithContext(ctx))
	defer func() {
		prog = nil
	}()

	var err error
	m, err = prog.Run()
	prog.Wait()
	suite.Require().NoError(err)
	suite.Equal(0, m.(HasStatus).Status(), "The command exited with a non-zero status.")

	var extra string
	if fo, ok := m.(HasFinalOutput); ok {
		extra = fo.FinalOutput()
	}
	return ansi.Strip(out.String() + extra)
}

func (suite *SubcommandTestSuite) validateOutput(_ /* uiOutput */, finalOutput, actual string) {
	// With tea.WithoutRenderer(), we don't get the UI rendering output (spinner/progress bars)
	// Only the final output is present, so we ignore the first parameter (kept for API compatibility)
	suite.Equal(finalOutput, actual)
}

// The SubcommandTestSuite is only run for ConfigEnv by default but is
// parametrized by configLevel so tests can be run for other levels. Tests must
// opt into this behavior by instantiating subcommands with `suite.configLevel`
// like:
//
//	m := InstallCmd{Driver: "foo", Level: suite.configLevel}
//	                                      ^---- here
//
// and can opt out of this behavior by specifying it separately like:
//
//	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
//
// When any level is explicitly requested, tests are only run for that level.
// i.e., to run tests for multiple levels, each level must be specified
// separately.
func TestSubcommandsEnv(t *testing.T) {
	_, env := os.LookupEnv("DBC_TEST_LEVEL_ENV")
	_, user := os.LookupEnv("DBC_TEST_LEVEL_USER")
	_, system := os.LookupEnv("DBC_TEST_LEVEL_SYSTEM")

	// Run if explicitly requested, or if no levels were requested (default
	// behavior)
	if env || (!user && !system) {
		suite.Run(t, &SubcommandTestSuite{configLevel: config.ConfigEnv})
		return
	}
	t.Skip("skipping tests for config level: ConfigEnv")
}

func TestSubcommandsUser(t *testing.T) {
	if _, ok := os.LookupEnv("DBC_TEST_LEVEL_USER"); !ok {
		t.Skip("skipping tests for config level: ConfigUser")
	}
	suite.Run(t, &SubcommandTestSuite{configLevel: config.ConfigUser})
}

func TestSubcommandsSystem(t *testing.T) {
	if _, ok := os.LookupEnv("DBC_TEST_LEVEL_SYSTEM"); !ok {
		t.Skip("skipping tests for config level: ConfigSystem")
	}
	suite.Run(t, &SubcommandTestSuite{configLevel: config.ConfigSystem})
}

// assertJSONErrorEnvelope asserts that every non-empty line in output is valid
// JSON, and that the last JSON envelope has kind=="error" and the expected code.
// Any non-JSON line causes the assertion to fail so regressions that mix
// plaintext and structured output are caught immediately.
// Optional msgSubstrings are checked against the error payload's message field.
func (suite *SubcommandTestSuite) assertJSONErrorEnvelope(output, expectedCode string, msgSubstrings ...string) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var lastEnv jsonschema.Envelope
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		suite.Require().True(strings.HasPrefix(line, "{"),
			"expected all non-empty output lines to be JSON objects, got: %q (full output: %s)", line, output)
		suite.Require().NoError(json.Unmarshal([]byte(line), &lastEnv),
			"line must be valid JSON: %q", line)
	}
	suite.Require().NotEmpty(lastEnv.Kind, "expected at least one JSON envelope in output: %s", output)
	suite.Equal("error", lastEnv.Kind, "expected kind=error (full output: %s)", output)
	var errPayload jsonschema.ErrorResponse
	suite.Require().NoError(json.Unmarshal(lastEnv.Payload, &errPayload))
	suite.Equal(expectedCode, errPayload.Code, "expected error code %q, got %q", expectedCode, errPayload.Code)
	for _, sub := range msgSubstrings {
		suite.Contains(errPayload.Message, sub, "expected error message to contain %q", sub)
	}
}

func (suite *SubcommandTestSuite) driverIsInstalled(path string, checkShared bool) {
	cfg := config.Get()[suite.configLevel]

	driver, err := config.GetDriver(cfg, path)
	suite.Require().NoError(err, "driver manifest should exist for driver `%s`", path)

	if checkShared {
		sharedPath := driver.Driver.Shared.Get(config.PlatformTuple())
		suite.FileExists(sharedPath, "driver shared library should exist for driver `%s`", path)
	}
}

func (suite *SubcommandTestSuite) driverIsNotInstalled(path string) {
	cfg := config.Get()[suite.configLevel]

	_, err := config.GetDriver(cfg, path)
	suite.Require().Error(err, "driver manifest should not exist for driver `%s`", path)
}
