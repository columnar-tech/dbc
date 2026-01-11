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

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
)

func parseDriverConstraint(driver string) (string, *semver.Constraints, error) {
	driver = strings.TrimSpace(driver)
	splitIdx := strings.IndexAny(driver, " ~^<>=!")
	if splitIdx == -1 {
		return driver, nil, nil
	}

	driverName := driver[:splitIdx]
	constraints, err := semver.NewConstraint(strings.TrimSpace(driver[splitIdx:]))
	if err != nil {
		return "", nil, fmt.Errorf("invalid version constraint: %w", err)
	}

	return driverName, constraints, nil
}

type InstallCmd struct {
	// URI    url.URL `arg:"-u" placeholder:"URL" help:"Base URL for fetching drivers"`
	Driver   string             `arg:"positional,required" help:"Driver to install"`
	Level    config.ConfigLevel `arg:"-l" help:"Config level to install to (user, system)"`
	Json     bool               `arg:"--json" help:"Output JSON instead of plaintext"`
	NoVerify bool               `arg:"--no-verify" help:"Allow installation of drivers without a signature file"`
}

func (c InstallCmd) GetModelCustom(baseModel baseModel) tea.Model {
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	return progressiveInstallModel{
		Driver:     c.Driver,
		NoVerify:   c.NoVerify,
		jsonOutput: c.Json,
		spinner:    s,
		cfg:        getConfig(c.Level),
		baseModel:  baseModel,
		p: dbc.NewFileProgress(
			progress.WithDefaultGradient(),
			progress.WithWidth(20),
			progress.WithoutPercentage(),
		),
	}
}

func (c InstallCmd) GetModel() tea.Model {
	return c.GetModelCustom(baseModel{
		getDriverRegistry: getDriverRegistry,
		downloadPkg:       downloadPkg,
	})
}

func verifySignature(m config.Manifest, noVerify bool) error {
	if m.Files.Driver == "" || (noVerify && m.Files.Signature == "") {
		return nil
	}

	path := filepath.Dir(m.Driver.Shared.Get(config.PlatformTuple()))

	lib, err := os.Open(filepath.Join(path, m.Files.Driver))
	if err != nil {
		return fmt.Errorf("could not open driver file: %w", err)
	}
	defer lib.Close()

	sigFile := m.Files.Signature
	if sigFile == "" {
		sigFile = m.Files.Driver + ".sig"
	}

	sig, err := os.Open(filepath.Join(path, sigFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("signature file '%s' for driver is missing", sigFile)
		}
		return fmt.Errorf("failed to open signature file: %w", err)
	}
	defer sig.Close()

	if err := dbc.SignedByColumnar(lib, sig); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

type writeDriverManifestMsg struct {
	DriverInfo config.DriverInfo
}

type installState int

const (
	stSearching installState = iota
	stDownloading
	stInstalling
	stVerifying
	stDone
)

func (s installState) String() string {
	switch s {
	case stSearching:
		return "searching"
	case stDownloading:
		return "downloading"
	case stVerifying:
		return "verifying signature"
	case stInstalling:
		return "installing"
	default:
		return "done"
	}
}

type progressiveInstallModel struct {
	baseModel

	Driver       string
	VersionInput *semver.Version
	NoVerify     bool
	jsonOutput   bool
	cfg          config.Config

	DriverPackage      dbc.PkgInfo
	conflictingInfo    config.DriverInfo
	postInstallMessage string

	state   installState
	spinner spinner.Model
	p       dbc.FileProgressModel

	width, height int
}

func (m progressiveInstallModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		drivers, err := m.getDriverRegistry()
		if err != nil {
			return err
		}
		return drivers
	})
}

func (m progressiveInstallModel) FinalOutput() string {
	if m.conflictingInfo.ID != "" && m.conflictingInfo.Version != nil {
		if m.conflictingInfo.Version.Equal(m.DriverPackage.Version) {
			if m.jsonOutput {
				return fmt.Sprintf(`{"status":"already installed","driver":"%s","version":"%s","location":"%s"}`,
					m.conflictingInfo.ID, m.conflictingInfo.Version, filepath.SplitList(m.cfg.Location)[0])
			}
			return fmt.Sprintf("\nDriver %s %s already installed at %s\n",
				m.conflictingInfo.ID, m.conflictingInfo.Version, filepath.SplitList(m.cfg.Location)[0])
		}
	}

	var b strings.Builder
	if m.state == stDone {
		var output struct {
			Status   string `json:"status"`
			Driver   string `json:"driver"`
			Version  string `json:"version"`
			Location string `json:"location"`
			Message  string `json:"message,omitempty"`
			Conflict string `json:"conflict,omitempty"`
		}

		output.Status = "installed"
		output.Driver = m.Driver
		output.Version = m.DriverPackage.Version.String()
		output.Location = filepath.SplitList(m.cfg.Location)[0]
		if m.conflictingInfo.ID != "" && m.conflictingInfo.Version != nil {
			output.Conflict = fmt.Sprintf("%s (version: %s)", m.conflictingInfo.ID, m.conflictingInfo.Version)
		}

		if m.postInstallMessage != "" {
			output.Message = m.postInstallMessage
		}

		if m.jsonOutput {
			jsonOutput, err := json.Marshal(output)
			if err != nil {
				return fmt.Sprintf(`{"status":"error","error":"%s"}`, err.Error())
			}
			return string(jsonOutput)
		}

		if output.Conflict != "" {
			fmt.Fprintf(&b, "\nRemoved conflicting driver: %s\n", output.Conflict)
		}

		fmt.Fprintf(&b, "\nInstalled %s %s to %s\n",
			output.Driver, output.Version, output.Location)

		if output.Message != "" {
			b.WriteString("\n" + postMsgStyle.Render(output.Message) + "\n")
		}
	}
	return b.String()
}

func (m progressiveInstallModel) searchForDriver(list []dbc.Driver) (tea.Model, tea.Cmd) {
	driverName, vers, err := parseDriverConstraint(m.Driver)
	if err != nil {
		return m, errCmd("%w", err)
	}

	m.Driver = driverName
	d, err := findDriver(m.Driver, list)
	if err != nil {
		return m, errCmd("could not find driver: %w", err)
	}

	return m, func() tea.Msg {
		if vers != nil {
			pkg, err := d.GetWithConstraint(vers, config.PlatformTuple())
			if err != nil {
				return err
			}
			return pkg
		}

		pkg, err := d.GetPackage(nil, config.PlatformTuple())
		if err != nil {
			return err
		}

		return pkg
	}
}

func (m progressiveInstallModel) startDownloading() (tea.Model, tea.Cmd) {
	m.state = stDownloading
	if m.conflictingInfo.ID != "" && m.conflictingInfo.Version != nil {
		if m.conflictingInfo.Version.Equal(m.DriverPackage.Version) {
			m.state = stDone
			return m, tea.Quit
		}
	}

	return m, func() tea.Msg {
		output, err := m.downloadPkg(m.DriverPackage)
		if err != nil {
			return err
		}
		return output
	}
}

func (m progressiveInstallModel) startInstalling(downloaded *os.File) (tea.Model, tea.Cmd) {
	m.state = stInstalling
	return m, func() tea.Msg {
		if m.conflictingInfo.ID != "" {
			if err := config.UninstallDriver(m.cfg, m.conflictingInfo); err != nil {
				return err
			}
		}

		manifest, err := config.InstallDriver(m.cfg, m.Driver, downloaded)
		if err != nil {
			return err
		}
		return manifest
	}
}

func (m progressiveInstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case progressMsg:
		cmd := m.p.SetPercent(msg.written, msg.total)
		return m, cmd
	case progress.FrameMsg:
		p, cmd := m.p.Update(msg)
		m.p = p.(dbc.FileProgressModel)
		return m, cmd
	case []dbc.Driver:
		return m.searchForDriver(msg)
	case dbc.PkgInfo:
		m.DriverPackage = msg
		di, err := config.GetDriver(m.cfg, m.Driver)
		if err == nil {
			m.conflictingInfo = di
		}

		return m.startDownloading()
	case *os.File:
		return m.startInstalling(msg)
	case config.Manifest:
		m.state = stVerifying
		m.postInstallMessage = strings.Join(msg.PostInstall.Messages, "\n")
		return m, func() tea.Msg {
			if err := verifySignature(msg, m.NoVerify); err != nil {
				path := filepath.Dir(msg.Driver.Shared.Get(config.PlatformTuple()))
				_ = os.RemoveAll(path)
				return err
			}
			return writeDriverManifestMsg{DriverInfo: msg.DriverInfo}
		}
	case writeDriverManifestMsg:
		m.state = stDone
		return m, tea.Sequence(func() tea.Msg {
			return config.CreateManifest(m.cfg, msg.DriverInfo)
		}, tea.Quit)
	}

	base, cmd := m.baseModel.Update(msg)
	m.baseModel = base.(baseModel)
	return m, cmd
}

func checkbox(label string, checked bool) string {
	if checked {
		return fmt.Sprintf("[%s] %s", checkMark, label)
	}
	return fmt.Sprintf("[ ] %s", label)
}

var postMsgStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

func (m progressiveInstallModel) View() string {
	if m.status != 0 {
		return ""
	}

	if m.conflictingInfo.ID != "" && m.conflictingInfo.Version != nil {
		if m.conflictingInfo.Version.Equal(m.DriverPackage.Version) {
			return ""
		}
	}

	var b strings.Builder
	for s := range stDone {
		if s == m.state {
			b.WriteString(fmt.Sprintf("[%s] %s...", m.spinner.View(), s.String()))
			if s == stDownloading {
				b.WriteString(" " + m.p.View())
			}
		} else {
			b.WriteString(checkbox(s.String(), s < m.state))
		}
		b.WriteByte('\n')
	}

	return b.String()
}
