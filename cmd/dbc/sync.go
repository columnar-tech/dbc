// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/pelletier/go-toml/v2"
)

var (
	checkMark = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).SetString("✓")
)

type SyncCmd struct {
	Path  string             `arg:"-p" placeholder:"FILE" default:"./dbc.toml" help:"Drivers list to sync from"`
	Level config.ConfigLevel `arg:"-l" help:"Config level to install to (env, user, system)" default:"user"`
}

func (c SyncCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return syncModel{
		baseModel:   baseModel,
		Path:        c.Path,
		cfg:         config.Get()[c.Level],
		termProgram: os.Getenv("TERM_PROGRAM"),
	}
}

func (c SyncCmd) GetModel() tea.Model {
	return syncModel{
		Path:        c.Path,
		cfg:         config.Get()[c.Level],
		termProgram: os.Getenv("TERM_PROGRAM"),
		baseModel: baseModel{
			getDriverList: getDriverList,
			downloadPkg:   downloadPkg,
		},
	}
}

type syncModel struct {
	baseModel

	// path to drivers list
	Path         string
	LockFilePath string
	// information to write the new lockfile
	locked LockFile
	cfg    config.Config

	// the list of drivers in the drivers list file
	list DriversList
	// cdn driver index
	driverIndex []dbc.Driver
	// the list of package+version to install
	installItems []installItem
	// the index of the next driver to install in installItems
	index int

	spinner       spinner.Model
	progress      progress.Model
	width, height int

	termProgram string
	done        bool
}

type driversListMsg struct {
	path string
	list DriversList
}

func (s syncModel) Init() tea.Cmd {
	return func() tea.Msg {
		p, err := filepath.Abs(s.Path)
		if err != nil {
			return err
		}

		if filepath.Ext(p) == "" {
			p = filepath.Join(p, "dbc.toml")
		}

		drivers, err := loadDriverList(p)
		if err != nil {
			return err
		}
		return driversListMsg{
			path: p,
			list: drivers,
		}
	}
}

func loadDriverList(path string) (DriversList, error) {
	f, err := os.Open(path)
	if err != nil {
		var outError error
		if errors.Is(err, os.ErrNotExist) {
			outError = fmt.Errorf("error opening drivers list file: %s doesn't exist\ndid you run `dbc init`?", path)
		} else {
			outError = fmt.Errorf("error opening drivers list file at %s: %w", path, err)
		}
		return DriversList{}, outError
	}
	defer f.Close()

	var list DriversList
	if err := toml.NewDecoder(f).Decode(&list); err != nil {
		return DriversList{}, err
	}

	if len(list.Drivers) == 0 {
		return DriversList{}, fmt.Errorf("no drivers found in drivers list file %s", path)
	}
	return list, nil
}

type installItem struct {
	Driver   dbc.Driver
	Package  dbc.PkgInfo
	Checksum string
}

func (s syncModel) createInstallList(list DriversList) ([]installItem, error) {
	// Load the lock file if it exists
	lf, err := loadLockFile(s.LockFilePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// construct our list of driver+version to install
	var items []installItem
	for name, spec := range list.Drivers {
		var info lockInfo
		if lf.lockinfo != nil {
			info = lf.lockinfo[name]
		}

		// locate the driver info in the CDN driver index
		drv, err := findDriver(name, s.driverIndex)
		if err != nil {
			return nil, err
		}

		var pkg dbc.PkgInfo
		// if the lockfile specified a version and either the driver list doesn't
		// specify a version constraint or the version in the locked file is valid
		// for that constraint, then we want to install the version in the lockfile
		if info.Version != nil && (spec.Version == nil || spec.Version.Check(info.Version)) {
			// install the locked version and verify checksum
			pkg, err = drv.GetPackage(info.Version, config.PlatformTuple())
		} else {
			// no locked version or driver list version doesn't match locked file
			if spec.Version != nil {
				pkg, err = drv.GetWithConstraint(spec.Version, config.PlatformTuple())
			} else {
				pkg, err = drv.GetPackage(nil, config.PlatformTuple())
			}
		}

		if err != nil {
			return nil, err
		}

		items = append(items, installItem{
			Driver:   drv,
			Package:  pkg,
			Checksum: info.Checksum,
		})
	}
	return items, nil
}

type installedDrvMsg struct {
	removed *config.DriverInfo
	info    config.DriverInfo
}

type alreadyInstalledDrvMsg struct {
	info config.DriverInfo
	item installItem
}

func (s syncModel) installDriver(cfg config.Config, item installItem) tea.Cmd {
	return func() tea.Msg {
		// TODO: Factor this out into config package, remove duplication with
		// config.InstallDriver
		var removedDriver *config.DriverInfo
		if cfg.Exists {
			// is driver installed already?
			if drv, ok := cfg.Drivers[item.Driver.Path]; ok {
				if item.Package.Version.Equal(drv.Version) {
					chksum, err := checksum(drv.Driver.Shared.Get(config.PlatformTuple()))
					if err != nil {
						return fmt.Errorf("failed to compute checksum: %w", err)
					}

					if item.Checksum != "" {
						if chksum != item.Checksum {
							return fmt.Errorf("checksum mismatch for driver %s: %s != %s",
								item.Driver.Path, chksum, item.Checksum)
						}
					} else {
						item.Checksum = chksum
					}

					return alreadyInstalledDrvMsg{info: drv, item: item}
				} else {
					if err := config.DeleteDriver(cfg, drv); err != nil {
						return fmt.Errorf("failed when deleting driver %s-%s: %w", drv.ID, drv.Version, err)
					}
					removedDriver = &drv
				}
			}
		}

		output, err := s.downloadPkg(item.Package)
		if err != nil {
			return fmt.Errorf("failed to download driver: %w", err)
		}

		var loc string
		if loc, err = config.EnsureLocation(cfg); err != nil {
			return fmt.Errorf("failed to ensure config location: %w", err)
		}

		base := strings.TrimSuffix(path.Base(item.Package.Path.Path), ".tar.gz")
		finalDir := filepath.Join(loc, base)
		if err := os.MkdirAll(finalDir, 0o755); err != nil {
			return fmt.Errorf("failed to create driver directory %s: %w", finalDir, err)
		}

		output.Seek(0, io.SeekStart)
		manifest, err := config.InflateTarball(output, finalDir)
		if err != nil {
			return fmt.Errorf("failed to extract tarball: %w", err)
		}

		driverPath := filepath.Join(finalDir, manifest.Files.Driver)

		manifest.DriverInfo.ID = item.Driver.Path
		manifest.DriverInfo.Source = "dbc"
		manifest.DriverInfo.Driver.Shared.Set(config.PlatformTuple(), driverPath)

		if err := verifySignature(manifest); err != nil {
			return fmt.Errorf("failed to verify signature: %w", err)
		}

		if err := config.CreateManifest(cfg, manifest.DriverInfo); err != nil {
			return fmt.Errorf("failed to create driver manifest: %w", err)
		}

		return installedDrvMsg{removed: removedDriver, info: manifest.DriverInfo}
	}
}

func (s syncModel) writeLockFile() error {
	f, err := os.Create(s.LockFilePath)
	if err != nil {
		return fmt.Errorf("failed to create lock file %s: %w", s.LockFilePath, err)
	}
	defer f.Close()

	s.locked.Version = lockFileVersion
	return toml.NewEncoder(f).Encode(s.locked)
}

func (s syncModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = msg.Width, msg.Height
	case spinner.TickMsg:
		var cmd tea.Cmd
		s.spinner, cmd = s.spinner.Update(msg)
		return s, cmd
	case progress.FrameMsg:
		newModel, cmd := s.progress.Update(msg)
		if newModel, ok := newModel.(progress.Model); ok {
			s.progress = newModel
		}
		return s, cmd
	case driversListMsg:
		s.Path = msg.path
		s.LockFilePath = strings.TrimSuffix(s.Path, filepath.Ext(s.Path)) + ".lock"
		s.list = msg.list
		return s, func() tea.Msg {
			drivers, err := s.getDriverList()
			if err != nil {
				return err
			}
			return drivers
		}
	case []dbc.Driver:
		s.driverIndex = msg
		return s, func() tea.Msg {
			items, err := s.createInstallList(s.list)
			if err != nil {
				return err
			}
			return items
		}
	case []installItem:
		s.spinner = spinner.New()
		s.progress = progress.New(
			progress.WithDefaultGradient(),
			progress.WithWidth(40),
			progress.WithoutPercentage(),
		)
		s.installItems = msg

		return s, tea.Batch(s.installDriver(s.cfg, s.installItems[s.index]), s.spinner.Tick)
	case alreadyInstalledDrvMsg:
		s.locked.Drivers = append(s.locked.Drivers, lockInfo{
			Name:     msg.info.ID,
			Version:  msg.info.Version,
			Platform: config.PlatformTuple(),
			Checksum: msg.item.Checksum,
		})

		if s.index >= len(s.installItems)-1 {
			s.done = true
			return s, tea.Sequence(
				tea.Printf("%s %s-%s already installed", checkMark, msg.info.ID, msg.info.Version),
				func() tea.Msg { return s.writeLockFile() },
				tea.Quit)
		}

		s.index++
		progressCmd := s.progress.SetPercent(float64(s.index) / float64(len(s.installItems)))
		return s, tea.Batch(
			progressCmd,
			tea.Printf("%s %s-%s already installed", checkMark, msg.info.ID, msg.info.Version),
			s.installDriver(s.cfg, s.installItems[s.index]),
		)
	case installedDrvMsg:
		chksum, err := checksum(msg.info.Driver.Shared.Get(config.PlatformTuple()))
		if err != nil {
			s.status = 1
			return s, tea.Sequence(tea.Println("Error: ", err), tea.Quit)
		}
		s.locked.Drivers = append(s.locked.Drivers, lockInfo{
			Name:     msg.info.ID,
			Version:  msg.info.Version,
			Platform: config.PlatformTuple(),
			Checksum: chksum,
		})

		printCmd := tea.Printf("%s %s-%s", checkMark, msg.info.ID, msg.info.Version)
		if msg.removed != nil {
			printCmd = tea.Sequence(
				printCmd,
				tea.Printf("%s   removed %s-%s", checkMark, msg.removed.ID, msg.removed.Version),
			)
		}

		if s.index >= len(s.installItems)-1 {
			s.done = true
			return s, tea.Sequence(
				printCmd,
				func() tea.Msg { return s.writeLockFile() },
				tea.Quit)
		}

		s.index++
		progressCmd := s.progress.SetPercent(float64(s.index) / float64(len(s.installItems)))
		return s, tea.Batch(
			progressCmd,
			printCmd,
			s.installDriver(s.cfg, s.installItems[s.index]),
		)
	}

	bm, cmd := s.baseModel.Update(msg)
	s.baseModel = bm.(baseModel)

	return s, cmd
}

func (s syncModel) View() string {
	n := len(s.installItems)
	if n == 0 {
		return "Determining drivers to install..."
	}
	w := lipgloss.Width(fmt.Sprintf("%d", n))

	if s.done {
		return "Done!\n"
	}

	driverCount := fmt.Sprintf(" %*d/%*d", w, s.index, w, n)

	spin := s.spinner.View() + " "
	prog := s.progress.View()
	cellsAvail := max(0, s.width-lipgloss.Width(spin+prog+driverCount))

	driverName := s.installItems[s.index].Driver.Path
	info := lipgloss.NewStyle().MaxWidth(cellsAvail).Render("Installing " + driverName)

	cellsRemaining := max(0, s.width-lipgloss.Width(spin+info+prog+driverCount)-1)
	gap := strings.Repeat(" ", max(0, cellsRemaining))

	return spin + info + gap + prog + driverCount
}
