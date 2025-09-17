// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
)

type InstallCmd struct {
	// URI    url.URL `arg:"-u" placeholder:"URL" help:"Base URL for fetching drivers"`
	Driver   string             `arg:"positional,required" help:"Driver to install"`
	Version  *semver.Version    `arg:"-v" help:"Version to install"`
	Level    config.ConfigLevel `arg:"-l" help:"Config level to install to (user, system)"`
	NoVerify bool               `arg:"--no-verify" help:"Allow installation of drivers without a signature file"`
}

func (c InstallCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return progressiveInstallModel{
		Driver:       c.Driver,
		VersionInput: c.Version,
		NoVerify:     c.NoVerify,
		spinner:      spinner.New(),
		cfg:          getConfig(c.Level),
		baseModel:    baseModel,
	}
}

func (c InstallCmd) GetModel() tea.Model {
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	return progressiveInstallModel{
		Driver:       c.Driver,
		VersionInput: c.Version,
		NoVerify:     c.NoVerify,
		spinner:      s,
		cfg:          getConfig(c.Level),
		baseModel: baseModel{
			getDriverList: getDriverList,
			downloadPkg:   downloadPkg,
		},
	}
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
	cfg          config.Config

	DriverPackage      dbc.PkgInfo
	conflictingInfo    config.DriverInfo
	postInstallMessage string

	state   installState
	spinner spinner.Model

	width, height int
}

func (m progressiveInstallModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		drivers, err := m.getDriverList()
		if err != nil {
			return err
		}
		return drivers
	})
}

func (m progressiveInstallModel) FinalOutput() string {
	if m.conflictingInfo.ID != "" && m.conflictingInfo.Version != nil {
		if m.conflictingInfo.Version.Equal(m.DriverPackage.Version) {
			return fmt.Sprintf("\nDriver %s %s already installed at %s\n",
				m.conflictingInfo.ID, m.conflictingInfo.Version, filepath.SplitList(m.cfg.Location)[0])
		}
	}

	var b strings.Builder
	if m.state == stDone {
		if m.conflictingInfo.ID != "" && m.conflictingInfo.Version != nil {
			b.WriteString(fmt.Sprintf("\nRemoved conflicting driver: %s (version: %s)",
				m.conflictingInfo.ID, m.conflictingInfo.Version))
		}

		b.WriteString(fmt.Sprintf("\nInstalled %s %s to %s\n",
			m.Driver, m.DriverPackage.Version, filepath.SplitList(m.cfg.Location)[0]))

		if m.postInstallMessage != "" {
			b.WriteString("\n" + postMsgStyle.Render(m.postInstallMessage) + "\n")
		}
	}
	return b.String()
}

func (m progressiveInstallModel) searchForDriver(d dbc.Driver) tea.Cmd {
	return func() tea.Msg {
		pkg, err := d.GetPackage(m.VersionInput, config.PlatformTuple())
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
	case []dbc.Driver:
		d, err := findDriver(m.Driver, msg)
		if err != nil {
			return m, errCmd("could not find driver: %w", err)
		}

		return m, m.searchForDriver(d)
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
			// return fmt.Sprintf("\nDriver %s %s already installed at %s\n",
			// 	m.conflictingInfo.ID, m.conflictingInfo.Version, filepath.SplitList(m.cfg.Location)[0])
		}
	}

	var b strings.Builder
	for s := range stDone {
		if s == m.state {
			b.WriteString(fmt.Sprintf("[%s] %s...", m.spinner.View(), s.String()))
		} else {
			b.WriteString(checkbox(s.String(), s < m.state))
		}
		b.WriteByte('\n')
	}

	// if m.state == stDone {
	// 	if m.conflictingInfo.ID != "" && m.conflictingInfo.Version != nil {
	// 		b.WriteString(fmt.Sprintf("\nRemoved conflicting driver: %s (version: %s)",
	// 			m.conflictingInfo.ID, m.conflictingInfo.Version))
	// 	}

	// 	b.WriteString(fmt.Sprintf("\nInstalled %s %s to %s\n",
	// 		m.Driver, m.DriverPackage.Version, filepath.SplitList(m.cfg.Location)[0]))

	// 	if m.postInstallMessage != "" {
	// 		b.WriteString("\n" + postMsgStyle.Width(m.width).Render(m.postInstallMessage) + "\n")
	// 	}
	// }
	return b.String()
}
