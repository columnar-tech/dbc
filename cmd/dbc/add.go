// Copyright (c) 2025 Columnar Technologies Inc.  All rights reserved.

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/columnar-tech/dbc/config"
	"github.com/pelletier/go-toml/v2"
)

var msgStyle = lipgloss.NewStyle().Faint(true)

func driverListPath(path string) (string, error) {
	p, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	if filepath.Ext(p) == "" {
		p = filepath.Join(p, "dbc.toml")
	}
	return p, nil
}

type AddCmd struct {
	Driver string `arg:"positional,required" help:"Driver to add"`
	Path   string `arg:"-p" placeholder:"FILE" default:"./dbc.toml" help:"Driver list to add to"`
}

func (c AddCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return addModel{
		baseModel: baseModel,
		Driver:    c.Driver,
		Path:      c.Path,
	}
}

func (c AddCmd) GetModel() tea.Model {
	return addModel{
		Driver: c.Driver,
		Path:   c.Path,
		baseModel: baseModel{
			getDriverList: getDriverList,
			downloadPkg:   downloadPkg,
		},
	}
}

type addModel struct {
	baseModel

	Driver string
	Path   string

	list DriversList
}

func (m addModel) Init() tea.Cmd {
	driverName, vers, err := parseDriverConstraint(m.Driver)
	if err != nil {
		return errCmd("invalid driver constraint: %w", err)
	}

	return func() tea.Msg {
		drivers, err := m.getDriverList()
		if err != nil {
			return fmt.Errorf("error getting driver list: %w", err)
		}

		drv, err := findDriver(driverName, drivers)
		if err != nil {
			return err
		}

		if vers != nil {
			_, err = drv.GetWithConstraint(vers, config.PlatformTuple())
			if err != nil {
				return fmt.Errorf("error getting driver: %w", err)
			}
		}

		p, err := driverListPath(m.Path)
		if err != nil {
			return err
		}

		f, err := os.Open(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("error opening driver list file: %s doesn't exist\nDid you run `dbc init`?", m.Path)
			} else {
				return fmt.Errorf("error opening driver list file at %s: %w", m.Path, err)
			}
		}
		defer f.Close()

		if err := toml.NewDecoder(f).Decode(&m.list); err != nil {
			return err
		}

		var result string
		if m.list.Drivers == nil {
			m.list.Drivers = make(map[string]driverSpec)
		}

		current, ok := m.list.Drivers[driverName]
		m.list.Drivers[driverName] = driverSpec{Version: vers}
		new := m.list.Drivers[driverName]
		currentString := func() string {
			if current.Version != nil {
				return current.Version.String()
			}
			return "any"
		}()
		newStr := func() string {
			if new.Version != nil {
				return new.Version.String()
			}
			return "any"
		}()
		if ok {
			result = msgStyle.Render(fmt.Sprintf("replacing existing driver %s (old constraint: %s; new constraint: %s)",
				driverName, currentString, newStr)) + "\n"
		}

		m.list.Drivers[driverName] = driverSpec{Version: vers}
		f, err = os.Create(p)
		if err != nil {
			return fmt.Errorf("error creating file %s: %w", p, err)
		}
		defer f.Close()

		if err := toml.NewEncoder(f).Encode(m.list); err != nil {
			return err
		}

		result += nameStyle.Render("added", driverName, "to driver list")
		if vers != nil {
			result += nameStyle.Render(" with constraint", vers.String())
		}
		result += "\nuse `dbc sync` to install the drivers in the list"
		return result
	}
}

func (m addModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case string:
		return m, tea.Sequence(tea.Println(msg), tea.Quit)
	default:
		bm, cmd := m.baseModel.Update(msg)
		m.baseModel = bm.(baseModel)

		return m, cmd
	}
}

func (m addModel) View() string { return "" }
