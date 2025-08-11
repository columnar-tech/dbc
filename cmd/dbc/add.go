// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"

	"github.com/Masterminds/semver/v3"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/columnar-tech/dbc"
	"github.com/pelletier/go-toml/v2"
)

var msgStyle = lipgloss.NewStyle().Faint(true)

type AddCmd struct {
	Driver string `arg:"positional,required" help:"Driver to add"`
	Path   string `arg:"-p" placeholder:"FILE" default:"./dbc.toml" help:"Driver manifest list to add to"`
}

func (c AddCmd) GetModel() tea.Model {
	return addModel{Driver: c.Driver, Path: c.Path}
}

type addModel struct {
	Driver string
	Path   string

	list ManifestList

	status int
}

func (m addModel) Status() int {
	return m.status
}

var drvArgRegexp = regexp.MustCompile(`(\w+)([<>=]{1,2}\d+(\.\d+){0,2})?`)

func (m addModel) Init() tea.Cmd {
	matches := drvArgRegexp.FindStringSubmatch(m.Driver)
	if matches == nil {
		return errCmd("invalid driver argument: %s, should be of the form <driver>[<version spec>]", m.Driver)
	}

	var (
		err        error
		driverName = matches[1]
		vers       *semver.Constraints
	)

	if matches[2] != "" {
		vers, err = semver.NewConstraint(matches[2])
		if err != nil {
			return errCmd("invalid version constraint: %w", err)
		}
	}

	return func() tea.Msg {
		drivers, err := getDriverList()
		if err != nil {
			return fmt.Errorf("error getting driver list: %w", err)
		}

		idx := slices.IndexFunc(drivers, func(d dbc.Driver) bool {
			return d.Path == driverName
		})

		if idx == -1 {
			return fmt.Errorf("driver %s not found", driverName)
		}

		if vers != nil {
			_, err = drivers[idx].GetWithConstraint(vers, platformTuple)
			if err != nil {
				return fmt.Errorf("error getting driver: %w", err)
			}
		}

		f, err := os.Open(m.Path)
		if err != nil {
			return fmt.Errorf("error opening manifest file %s: %w", m.Path, err)
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
		if ok {
			result = msgStyle.Render(fmt.Sprintf("replacing existing driver %s (new constraint: %s)",
				driverName, current.Version)) + "\n"
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

		result += "use `dbc sync` to install the drivers in the list"
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
