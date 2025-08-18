// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/pelletier/go-toml/v2"
)

type InstallCmd struct {
	// URI    url.URL `arg:"-u" placeholder:"URL" help:"Base URL for fetching drivers"`
	Driver  string             `arg:"positional,required" help:"Driver to install"`
	Version *semver.Version    `arg:"-v" help:"Version to install"`
	Level   config.ConfigLevel `arg:"-l" help:"Config level to install to" default:"user"`
}

func (c InstallCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return progressiveInstallModel{
		Driver:       c.Driver,
		VersionInput: c.Version,
		spinner:      spinner.New(),
		cfg:          config.Get()[c.Level],
		baseModel:    baseModel,
	}
}

func (c InstallCmd) GetModel() tea.Model {
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	return progressiveInstallModel{
		Driver:       c.Driver,
		VersionInput: c.Version,
		spinner:      s,
		cfg:          config.Get()[c.Level],
		baseModel: baseModel{
			getDriverList: getDriverList,
			downloadPkg:   downloadPkg,
		},
	}
}

type Manifest struct {
	config.DriverInfo

	Files struct {
		Driver    string `toml:"driver"`
		Signature string `toml:"signature"`
	} `toml:"Files"`
}

func verifySignature(m Manifest) error {
	lib, err := os.Open(m.Driver.Shared.Get(platformTuple))
	if err != nil {
		return fmt.Errorf("could not open driver file: %w", err)
	}
	defer lib.Close()

	sig, err := os.Open(filepath.Join(filepath.Dir(m.Driver.Shared.Get(platformTuple)), m.Files.Signature))
	if err != nil {
		return fmt.Errorf("could not open signature file: %w", err)
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

func inflateTarball(f *os.File, outDir string) (Manifest, error) {
	defer f.Close()
	var m Manifest

	rdr, err := gzip.NewReader(f)
	if err != nil {
		return m, fmt.Errorf("could not create gzip reader: %w", err)
	}
	defer rdr.Close()

	t := tar.NewReader(rdr)
	for {
		hdr, err := t.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return m, fmt.Errorf("error reading tarball: %w", err)
		}

		if hdr.Name != "MANIFEST" {
			next, err := os.Create(filepath.Join(outDir, hdr.Name))
			if err != nil {
				return m, fmt.Errorf("could not create file %s: %w", hdr.Name, err)
			}

			if _, err = io.Copy(next, t); err != nil {
				next.Close()
				return m, fmt.Errorf("could not write file from tarball %s: %w", hdr.Name, err)
			}
			next.Close()
		} else {
			if err := toml.NewDecoder(t).Decode(&m); err != nil {
				return m, fmt.Errorf("could not decode manifest: %w", err)
			}
		}
	}

	return m, nil
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
	cfg          config.Config

	DriverPackage   dbc.PkgInfo
	conflictingInfo config.DriverInfo

	state   installState
	spinner spinner.Model
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

func (m progressiveInstallModel) searchForDriver(d dbc.Driver) tea.Cmd {
	return func() tea.Msg {
		pkg, err := d.GetPackage(m.VersionInput, platformTuple)
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
			if err := config.DeleteDriver(m.cfg, m.conflictingInfo); err != nil {
				return err
			}
		}

		var (
			loc string
			err error
		)
		if loc, err = config.EnsureLocation(m.cfg); err != nil {
			return fmt.Errorf("could not ensure config location: %w", err)
		}

		base := strings.TrimSuffix(path.Base(m.DriverPackage.Path.Path), ".tar.gz")
		finalDir := filepath.Join(loc, base)
		if err := os.MkdirAll(finalDir, 0o755); err != nil {
			return fmt.Errorf("failed to create driver directory %s: %w", finalDir, err)
		}

		downloaded.Seek(0, io.SeekStart)
		manifest, err := inflateTarball(downloaded, finalDir)
		if err != nil {
			return fmt.Errorf("failed to extract tarball: %w", err)
		}

		driverPath := filepath.Join(finalDir, manifest.Files.Driver)

		manifest.DriverInfo.ID = m.Driver
		manifest.DriverInfo.Source = "dbc"
		manifest.DriverInfo.Driver.Shared.Set(platformTuple, driverPath)

		return manifest
	}
}

func (m progressiveInstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
	case Manifest:
		m.state = stVerifying
		return m, func() tea.Msg {
			if err := verifySignature(msg); err != nil {
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

func (m progressiveInstallModel) View() string {
	if m.status != 0 {
		return ""
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

	if m.state == stDone {
		if m.conflictingInfo.ID != "" && m.conflictingInfo.Version != nil {
			if m.conflictingInfo.Version.Equal(m.DriverPackage.Version) {
				b.WriteString(fmt.Sprintf("\nDriver %s v%s already installed at %s\n",
					m.conflictingInfo.ID, m.conflictingInfo.Version, filepath.SplitList(m.cfg.Location)[0]))
				return b.String()
			}

			b.WriteString(fmt.Sprintf("\nRemoved conflicting driver: %s (version: v%s)",
				m.conflictingInfo.ID, m.conflictingInfo.Version))
		}

		b.WriteString(fmt.Sprintf("\nInstalled %s v%s to %s\n",
			m.Driver, m.DriverPackage.Version, filepath.SplitList(m.cfg.Location)[0]))
	}
	return b.String()
}
