// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"fmt"
	"os"
	"slices"

	"github.com/alexflint/go-arg"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
)

var (
	errStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
)

type TuiCmd struct{}

func (TuiCmd) GetModel() tea.Model {
	return getTuiModel()
}

type modelCmd interface {
	GetModel() tea.Model
}

func errCmd(format string, a ...any) tea.Cmd {
	return func() tea.Msg {
		return fmt.Errorf(format, a...)
	}
}

type HasStatus interface {
	Status() int
}

// use this so we can override this in tests
var getDriverList = dbc.GetDriverList

func findDriver(name string, drivers []dbc.Driver) (dbc.Driver, error) {
	idx := slices.IndexFunc(drivers, func(d dbc.Driver) bool {
		return d.Path == name
	})

	if idx == -1 {
		return dbc.Driver{}, fmt.Errorf("driver `%s` not found in driver index", name)
	}
	return drivers[idx], nil
}

func downloadPkg(p dbc.PkgInfo) (*os.File, error) {
	return p.DownloadPackage()
}

func getConfig(c config.ConfigLevel) config.Config {
	switch c {
	case config.ConfigSystem, config.ConfigUser:
		return config.Get()[c]
	default:
		cfg := config.Get()[config.ConfigEnv]
		if cfg.Location != "" {
			return cfg
		}
		return config.Get()[config.ConfigUser]
	}
}

type baseModel struct {
	getDriverList func() ([]dbc.Driver, error)
	downloadPkg   func(p dbc.PkgInfo) (*os.File, error)

	status int
}

func (m baseModel) Init() tea.Cmd { return nil }
func (m baseModel) View() string  { return "" }

func (m baseModel) Status() int {
	return m.status
}

func (m baseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyCtrlD, tea.KeyEsc:
			return m, tea.Quit
		}
	case error:
		m.status = 1
		return m, tea.Sequence(tea.Println("Error: ", msg.Error()), tea.Quit)
	}
	return m, nil
}

type cmds struct {
	Install   *InstallCmd   `arg:"subcommand" help:"Install a driver"`
	Uninstall *UninstallCmd `arg:"subcommand" help:"Uninstall a driver"`
	Init      *InitCmd      `arg:"subcommand" help:"Initialize a new dbc drivers list"`
	Add       *AddCmd       `arg:"subcommand" help:"Add a driver to the drivers list"`
	Sync      *SyncCmd      `arg:"subcommand" help:"Sync installed drivers with drivers in the drivers list"`
	Search    *SearchCmd    `arg:"subcommand" help:"Search for a driver"`
	Remove    *RemoveCmd    `arg:"subcommand" help:"Remove a driver from the drivers list"`
}

func (cmds) Version() string {
	return dbc.Version
}

func main() {
	var args cmds

	p := arg.MustParse(&args)
	if p.Subcommand() == nil {
		p.WriteHelp(os.Stdout)
		os.Exit(1)
	}

	// f, err := tea.LogToFile("debug.log", "debug")
	// if err != nil {
	// 	fmt.Println("Error creating log file:", err)
	// 	os.Exit(1)
	// }
	// defer f.Close()

	var err error
	m := p.Subcommand().(modelCmd).GetModel()
	if m, err = tea.NewProgram(m).Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}

	if h, ok := m.(HasStatus); ok {
		os.Exit(h.Status())
	}
}
