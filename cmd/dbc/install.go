// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/tree"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/mod/semver"
)

var (
	errStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
)

type version string

func (v *version) UnmarshalText(text []byte) error {
	if !semver.IsValid(string(text)) {
		return fmt.Errorf("invalid version arg: %s", text)
	}

	*v = version(semver.Canonical(string(text)))
	return nil
}

type InstallCmd struct {
	// URI    url.URL `arg:"-u" placeholder:"URL" help:"Base URL for fetching drivers"`
	Driver  string             `arg:"positional,required" help:"Driver to install"`
	Version version            `arg:"-v" help:"Version to install"`
	Level   config.ConfigLevel `arg:"-l" help:"Config level to install to" default:"user"`
}

func (c InstallCmd) GetModel() tea.Model {
	return simpleInstallModel{
		Driver:       c.Driver,
		VersionInput: string(c.Version),
		cfg:          config.Get()[c.Level],
	}
}

type installState int

const (
	installStateNone installState = iota
	installStateConflict
	installStateConfirm
	installStateDownloading
	installStateInstalling
	installStateVerifySignature
	installStateDone
)

type downloadedMsg struct {
	file *os.File
	err  error
}

type conflictMsg config.DriverInfo

type simpleInstallModel struct {
	Driver       string
	VersionInput string
	cfg          config.Config

	state         installState
	DriverPackage dbc.PkgInfo
	confirmModel  textinput.Model

	downloaded downloadedMsg
	conflict   conflictMsg
	spinner    spinner.Model
}

func (m simpleInstallModel) Init() tea.Cmd {
	return tea.Sequence(
		tea.Printf(archStyle.Render("Current System: %s"), platformTuple),
		tea.Printf(archStyle.Render("Install To: %s"), m.cfg.Location),
		tea.Println(),
		func() tea.Msg {
			drivers, err := dbc.GetDriverList()
			if err != nil {
				return err
			}
			return drivers
		})
}

func createConfirmModel(prompt string) textinput.Model {
	confirmModel := textinput.New()
	confirmModel.Prompt = prompt
	confirmModel.CharLimit = 1
	confirmModel.Width = 3
	confirmModel.Validate = func(s string) error {
		v := strings.ToLower(s)
		switch v {
		case "", "y", "n":
			return nil
		}

		return errors.New("please enter y or n")
	}
	return confirmModel
}

func (m simpleInstallModel) toConfirmState(msg []dbc.Driver) (tea.Model, tea.Cmd) {
	for _, d := range msg {
		if d.Path == m.Driver {
			m.state = installStateConfirm
			m.DriverPackage = d.GetPackage(m.VersionInput, platformTuple)
			m.confirmModel = createConfirmModel("Install driver? (y/[N]): ")

			t := tree.Root(m.DriverPackage.Driver.Title).
				RootStyle(nameStyle).
				Child(m.DriverPackage.Version).
				Child(descStyle.Render(m.DriverPackage.Driver.Desc)).
				Child(archStyle.Render(m.DriverPackage.Platform)).
				Child(path.Base(m.DriverPackage.Path.Path))

			cmds := []tea.Cmd{
				tea.Println(descStyle.Render("Located driver..."), ""),
				tea.Println(t.String())}

			di, err := config.GetDriver(m.cfg.Location, m.Driver)
			if !errors.Is(err, fs.ErrNotExist) {
				cmds = append(cmds, func() tea.Msg {
					return conflictMsg(di)
				})
			} else {
				cmds = append(cmds, m.confirmModel.Focus())
			}

			return m, tea.Sequence(cmds...)
		}
	}

	return m, tea.Sequence(
		tea.Println(errStyle.Render("Driver not found")), tea.Quit)
}

func (m simpleInstallModel) handleConflict(msg conflictMsg) (tea.Model, tea.Cmd) {
	m.conflict, m.state = msg, installStateConflict
	var s string
	switch semver.Compare(m.DriverPackage.Version, msg.Version) {
	case -1:
		s = "newer"
	case 0:
		s = "the same"
	case 1:
		s = "older"
	}

	m.confirmModel = createConfirmModel("Remove existing driver? (y/[N]): ")
	return m, tea.Sequence(
		tea.Printf("\nFound %s existing local driver in %s", s, m.cfg.Location),
		tea.Println("Local Driver: ", msg.Name, " (", msg.Version, ")"),
		m.confirmModel.Focus())
}

func (m simpleInstallModel) removeConflictingDriver() (tea.Model, tea.Cmd) {
	prev := m.confirmModel.View()
	m.confirmModel = createConfirmModel("Install new driver? (y/[N]): ")
	m.state = installStateConfirm

	toRemove := m.conflict.Driver.Shared
	msg := "Removing driver: " + m.conflict.Driver.Shared
	if m.conflict.Source == "dbc" {
		toRemove = filepath.Dir(m.conflict.Driver.Shared)
		msg = "Removing directory: " + toRemove
	}

	return m, tea.Sequence(tea.Println(prev),
		tea.Println(msg),
		func() tea.Msg {
			if err := os.RemoveAll(toRemove); err != nil {
				return fmt.Errorf("could not remove existing driver (%s): %w", toRemove, err)
			}

			if err := os.Remove(filepath.Join(m.cfg.Location, m.Driver+".toml")); err != nil {
				return fmt.Errorf("could not remove existing driver manifest: %w", err)
			}

			return nil
		},
		tea.Println("Driver removed successfully!"),
		m.confirmModel.Focus(),
	)
}

func (m simpleInstallModel) startDownloading() (tea.Model, tea.Cmd) {
	m.spinner = spinner.New()
	m.spinner.Spinner = spinner.Dot
	return m, tea.Sequence(tea.Println(), tea.Println(m.confirmModel.View()), tea.Batch(m.spinner.Tick, func() tea.Msg {
		output, err := m.DriverPackage.DownloadPackage()
		return downloadedMsg{
			file: output,
			err:  err,
		}
	}))
}

type Manifest struct {
	config.DriverInfo

	Files struct {
		Driver    string `toml:"driver"`
		Signature string `toml:"signature"`
	} `toml:"Files"`
}

func verifySignature(m Manifest) tea.Cmd {
	return func() tea.Msg {
		lib, err := os.Open(m.Driver.Shared)
		if err != nil {
			return fmt.Errorf("could not open driver file: %w", err)
		}
		defer lib.Close()

		sig, err := os.Open(filepath.Join(filepath.Dir(m.Driver.Shared), m.Files.Signature))
		if err != nil {
			return fmt.Errorf("could not open signature file: %w", err)
		}
		defer sig.Close()

		if err := dbc.SignedByColumnar(lib, sig); err != nil {
			return fmt.Errorf("signature verification failed: %w", err)
		}

		return writeDriverManifestMsg{DriverInfo: m.DriverInfo}
	}
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

func (m simpleInstallModel) startInstalling(msg downloadedMsg) (tea.Model, tea.Cmd) {
	m.state, m.downloaded = installStateInstalling, msg

	return m, func() tea.Msg {
		if _, err := os.Stat(m.cfg.Location); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				if err = os.MkdirAll(m.cfg.Location, 0755); err != nil {
					return fmt.Errorf("could not create config dir: %w", err)
				}
			} else {
				return fmt.Errorf("could not stat config dir: %w", err)
			}
		}

		base := strings.TrimSuffix(path.Base(m.DriverPackage.Path.Path), ".tar.gz")

		finalDir := filepath.Join(m.cfg.Location, base)
		if err := os.Mkdir(finalDir, 0755); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("could not create driver dir: %w", err)
		}

		m.downloaded.file.Seek(0, io.SeekStart)
		manifest, err := inflateTarball(m.downloaded.file, finalDir)
		if err != nil {
			return fmt.Errorf("could not extract tarball: %w", err)
		}

		manifest.DriverInfo.Source = "dbc"
		manifest.DriverInfo.Driver.Shared = filepath.Join(m.cfg.Location, base, manifest.Files.Driver)
		return manifest
	}
}

func (m simpleInstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmds := make([]tea.Cmd, 0)

	switch msg := msg.(type) {
	case []dbc.Driver:
		return m.toConfirmState(msg)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+d", "esc":
			if m.downloaded.file != nil {
				m.downloaded.file.Close()
				os.RemoveAll(filepath.Dir(m.downloaded.file.Name()))
			}
			return m, tea.Quit
		case "enter":
			if m.state == installStateConfirm || m.state == installStateConflict {
				m.confirmModel.Blur()
				cmds = append(cmds,
					tea.Println(),
					tea.Println(m.confirmModel.View()))
				if m.confirmModel.Err != nil {
					cmds = append(cmds,
						tea.Println(errStyle.Render(m.confirmModel.Err.Error())),
						m.confirmModel.Focus())
					m.confirmModel.Reset()
				} else {
					if strings.ToLower(m.confirmModel.Value()) == "y" {
						switch m.state {
						case installStateConfirm:
							return m.startDownloading()
						case installStateConflict:
							return m.removeConflictingDriver()
						}
					} else {
						return m, tea.Quit
					}
				}
			}
		}

	case conflictMsg:
		return m.handleConflict(msg)

	case downloadedMsg:
		if msg.err != nil {
			return m, tea.Sequence(
				tea.Println("Error downloading driver: ", msg.err),
				tea.Quit)
		}

		return m.startInstalling(msg)

	case Manifest:
		m.state = installStateVerifySignature
		cmds = append(cmds,
			tea.Printf("%s Downloaded %s. Installing...", m.spinner.View(), path.Base(m.DriverPackage.Path.Path)),
			tea.Println("Verifying signature..."),
			verifySignature(msg))

	case writeDriverManifestMsg:
		m.state = installStateDone
		return m, tea.Sequence(func() tea.Msg {
			f, err := os.Create(filepath.Join(m.cfg.Location, m.Driver+".toml"))
			if err != nil {
				return fmt.Errorf("error creating driver manifest: %w", err)
			}
			defer f.Close()

			if err := toml.NewEncoder(f).Encode(msg.DriverInfo); err != nil {
				return fmt.Errorf("error writing driver manifest: %w", err)
			}

			return nil
		}, tea.Println("Driver installed successfully!"), tea.Quit)

	case error:
		return m, tea.Sequence(
			tea.Println(errStyle.Render(msg.Error())),
			tea.Println(""),
			tea.Quit)
	}

	var cmd tea.Cmd
	switch m.state {
	case installStateConfirm, installStateConflict:
		m.confirmModel, cmd = m.confirmModel.Update(msg)
	case installStateDownloading, installStateInstalling:
		m.spinner, cmd = m.spinner.Update(msg)
	}
	cmds = append(cmds, cmd)

	return m, tea.Sequence(cmds...)
}

func (m simpleInstallModel) View() string {
	switch m.state {
	case installStateConfirm, installStateConflict:
		return "\n" + m.confirmModel.View() + "\n"
	case installStateDownloading:
		return fmt.Sprintf("%s Downloading %s...",
			m.spinner.View(), path.Base(m.DriverPackage.Path.Path)) + "\n"
	case installStateInstalling:
		return fmt.Sprintf("%s Downloaded %s. Installing...",
			m.spinner.View(), path.Base(m.DriverPackage.Path.Path)) + "\n"
	}

	return ""
}
