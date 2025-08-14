// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Masterminds/semver/v3"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/pelletier/go-toml/v2"
)

var msgStyle = lipgloss.NewStyle().Faint(true)

type AddCmd struct {
	Driver string `arg:"positional,required" help:"Driver to add"`
	Path   string `arg:"-p" placeholder:"FILE" default:"./dbc.toml" help:"Drivers list to add to"`
}

func (c AddCmd) GetModel() tea.Model {
	return addModel{Driver: c.Driver, Path: c.Path}
}

type addModel struct {
	Driver string
	Path   string

	list DriversList

	status int
}

func (m addModel) Status() int {
	return m.status
}

func (m addModel) Init() tea.Cmd {
	m.Driver = strings.TrimSpace(m.Driver)
	splitIdx := strings.IndexAny(m.Driver, " <>=!")
	var (
		err        error
		driverName string
		vers       *semver.Constraints
	)

	if splitIdx == -1 {
		driverName = m.Driver
	} else {
		driverName = m.Driver[:splitIdx]
		vers, err = semver.NewConstraint(strings.TrimSpace(m.Driver[splitIdx:]))
		if err != nil {
			return errCmd("invalid version constraint: %w", err)
		}
	}

	return func() tea.Msg {
		drivers, err := getDriverList()
		if err != nil {
			return fmt.Errorf("error getting driver list: %w", err)
		}

		drv, err := findDriver(driverName, drivers)
		if err != nil {
			return err
		}

		if vers != nil {
			_, err = drv.GetWithConstraint(vers, platformTuple)
			if err != nil {
				return fmt.Errorf("error getting driver: %w", err)
			}
		}

		f, err := os.Open(m.Path)
		if err != nil {
			return fmt.Errorf("error opening drivers list file %s: %w\ndid you run `dbc init`?", m.Path, err)
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
		f, err = os.Create(m.Path)
		if err != nil {
			return fmt.Errorf("error creating file %s: %w", m.Path, err)
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
	case error:
		m.status = 1
		return m, tea.Sequence(tea.Println("Error: ", msg.Error()), tea.Quit)
	case string:
		return m, tea.Sequence(tea.Println(msg), tea.Quit)
	default:
		return m, nil
	}
}

func (m addModel) View() string { return "" }
