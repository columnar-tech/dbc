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
	Level config.ConfigLevel `arg:"-l" help:"Config level to install to" default:"user"`
}

func (c SyncCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return syncModel{
		baseModel: baseModel,
		Path:      c.Path,
		cfg:       config.Get()[c.Level],
	}
}

func (c SyncCmd) GetModel() tea.Model {
	return syncModel{
		Path: c.Path,
		cfg:  config.Get()[c.Level],
		baseModel: baseModel{
			getDriverList: getDriverList,
			downloadPkg:   downloadPkg,
		},
	}
}

type syncModel struct {
	baseModel

	Path         string
	LockFilePath string
	locked       LockFile
	cfg          config.Config

	driverIndex  []dbc.Driver
	installItems []installItem
	index        int

	spinner       spinner.Model
	progress      progress.Model
	width, height int

	done bool
}

func (s syncModel) Init() tea.Cmd {
	return func() tea.Msg {
		drivers, err := s.getDriverList()
		if err != nil {
			return err
		}
		return drivers
	}
}

func loadDriverList(path string) (DriversList, error) {
	f, err := os.Open(path)
	if err != nil {
		return DriversList{}, fmt.Errorf("error opening drivers list file %s: %w\ndid you run `dbc init`?",
			path, err)
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
	// Load the lock file
	lf, err := loadLockFile(s.LockFilePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	var items []installItem
	for name, spec := range list.Drivers {
		var info lockInfo
		if lf.lockinfo != nil {
			info = lf.lockinfo[name]
		}

		drv, err := findDriver(name, s.driverIndex)
		if err != nil {
			return nil, err
		}

		var pkg dbc.PkgInfo
		if info.Version != nil && (spec.Version == nil || spec.Version.Check(info.Version)) {
			// install the locked version and verify checksum
			pkg, err = drv.GetPackage(info.Version, platformTuple)
		} else {
			// no locked version or driver list doesn't match locked file
			if spec.Version != nil {
				pkg, err = drv.GetWithConstraint(spec.Version, platformTuple)
			} else {
				pkg, err = drv.GetPackage(nil, platformTuple)
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

func ensureConfigLocation(cfg config.Config) (string, error) {
	loc := cfg.Location
	if cfg.Level == config.ConfigEnv {
		list := filepath.SplitList(loc)
		if len(list) == 0 {
			return "", fmt.Errorf("invalid config location: %s", loc)
		}
		loc = list[0]
	}

	if _, err := os.Stat(loc); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.MkdirAll(loc, 0755); err != nil {
				return "", fmt.Errorf("failed to create config directory %s: %w", loc, err)
			}
		} else {
			return "", fmt.Errorf("failed to stat config directory %s: %w", loc, err)
		}
	}

	return loc, nil
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
		var removedDriver *config.DriverInfo
		if cfg.Exists {
			// is driver installed already?
			if drv, ok := cfg.Drivers[item.Driver.Path]; ok {
				if item.Package.Version.Equal(drv.Version) {
					chksum, err := checksum(drv.Driver.Shared.Get(platformTuple))
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
		if loc, err = ensureConfigLocation(cfg); err != nil {
			return fmt.Errorf("failed to ensure config location: %w", err)
		}

		base := strings.TrimSuffix(path.Base(item.Package.Path.Path), ".tar.gz")
		finalDir := filepath.Join(loc, base)
		if err := os.MkdirAll(finalDir, 0755); err != nil {
			return fmt.Errorf("failed to create driver directory %s: %w", finalDir, err)
		}

		output.Seek(0, io.SeekStart)
		manifest, err := inflateTarball(output, finalDir)
		if err != nil {
			return fmt.Errorf("failed to extract tarball: %w", err)
		}

		driverPath := filepath.Join(finalDir, manifest.Files.Driver)

		manifest.DriverInfo.ID = item.Driver.Path
		manifest.DriverInfo.Source = "dbc"
		manifest.DriverInfo.Driver.Shared.Set(platformTuple, driverPath)

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
	case []dbc.Driver:
		p, err := filepath.Abs(s.Path)
		if err != nil {
			s.status = 1
			return s, tea.Sequence(tea.Println("Error: ", err), tea.Quit)
		}

		if filepath.Ext(p) == "" {
			p = filepath.Join(p, "dbc.toml")
		}

		s.Path = p
		s.LockFilePath = strings.TrimSuffix(p, filepath.Ext(p)) + ".lock"
		s.driverIndex = msg
		return s, func() tea.Msg {
			list, err := loadDriverList(s.Path)
			if err != nil {
				return err
			}
			return list
		}
	case DriversList:
		return s, func() tea.Msg {
			items, err := s.createInstallList(msg)
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
			Platform: platformTuple,
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
		chksum, err := checksum(msg.info.Driver.Shared.Get(platformTuple))
		if err != nil {
			s.status = 1
			return s, tea.Sequence(tea.Println("Error: ", err), tea.Quit)
		}
		s.locked.Drivers = append(s.locked.Drivers, lockInfo{
			Name:     msg.info.ID,
			Version:  msg.info.Version,
			Platform: platformTuple,
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

	cellsRemaining := max(0, s.width-lipgloss.Width(spin+info+prog+driverCount))
	gap := strings.Repeat(" ", cellsRemaining)

	return spin + info + gap + prog + driverCount
}
