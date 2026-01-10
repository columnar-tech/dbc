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
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/alexflint/go-arg"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/auth"
	"github.com/columnar-tech/dbc/cmd/dbc/completions"
	"github.com/columnar-tech/dbc/config"
	"github.com/mattn/go-isatty"
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

type HasFinalOutput interface {
	FinalOutput() string
}

type HasStatus interface {
	Status() int
}

// use this so we can override this in tests
var getDriverRegistry = dbc.GetDriverList

func findDriver(name string, drivers []dbc.Driver) (dbc.Driver, error) {
	idx := slices.IndexFunc(drivers, func(d dbc.Driver) bool {
		return d.Path == name
	})

	if idx == -1 {
		return dbc.Driver{}, fmt.Errorf("driver `%s` not found in driver registry index", name)
	}
	return drivers[idx], nil
}

type progressMsg struct {
	total   int64
	written int64
}

func downloadPkg(p dbc.PkgInfo) (*os.File, error) {
	return p.DownloadPackage(func(written, total int64) {
		prog.Send(progressMsg{total: total, written: written})
	})
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
	getDriverRegistry func() ([]dbc.Driver, error)
	downloadPkg       func(p dbc.PkgInfo) (*os.File, error)

	status int
}

func (m baseModel) Init() tea.Cmd { return nil }
func (m baseModel) View() string  { return "" }

func (m baseModel) Status() int {
	return m.status
}

func (m baseModel) FinalOutput() string { return "" }

func (m baseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyCtrlD, tea.KeyEsc:
			return m, tea.Quit
		}
	case error:
		m.status = 1
		var cmd tea.Cmd
		switch {
		case errors.Is(msg, auth.ErrTrialExpired):
			cmd = tea.Println(errStyle.Render("Could not download license, trial has expired"))
		case errors.Is(msg, auth.ErrNoTrialLicense):
			cmd = tea.Println(errStyle.Render("Could not download license, trial not started"))
		default:
			cmd = tea.Println("Error: ", msg.Error())
		}
		return m, tea.Sequence(cmd, tea.Quit)
	}
	return m, nil
}

type cmds struct {
	Install    *InstallCmd      `arg:"subcommand" help:"Install a driver"`
	Uninstall  *UninstallCmd    `arg:"subcommand" help:"Uninstall a driver"`
	Init       *InitCmd         `arg:"subcommand" help:"Initialize a new dbc driver list"`
	Add        *AddCmd          `arg:"subcommand" help:"Add a driver to the driver list"`
	Sync       *SyncCmd         `arg:"subcommand" help:"Sync installed drivers with drivers in the driver list"`
	Search     *SearchCmd       `arg:"subcommand" help:"Search for a driver"`
	Info       *InfoCmd         `arg:"subcommand" help:"Get information about a driver"`
	Docs       *DocsCmd         `arg:"subcommand" help:"Open driver documentation in a web browser"`
	Remove     *RemoveCmd       `arg:"subcommand" help:"Remove a driver from the driver list"`
	Auth       *AuthCmd         `arg:"subcommand" help:"Manage driver registry credentials"`
	Completion *completions.Cmd `arg:"subcommand,hidden"`
	Quiet      bool             `arg:"-q,--quiet" help:"Suppress all output"`
}

func (cmds) Version() string {
	return dbc.Version
}

var prog *tea.Program

func main() {
	var (
		args cmds
	)

	p, err := arg.NewParser(arg.Config{Program: "dbc", EnvPrefix: "DBC_"}, &args)
	if err != nil {
		fmt.Println("Error creating argument parser:", err)
		os.Exit(1)
	}

	if err = p.Parse(os.Args[1:]); err != nil {
		switch {
		case err == arg.ErrHelp:
			if d, ok := p.Subcommand().(arg.Described); ok {
				fmt.Println(d.Description())
			}
			p.WriteHelpForSubcommand(os.Stdout, p.SubcommandNames()...)
			os.Exit(0)
		case err == arg.ErrVersion:
			fmt.Println(dbc.Version)
			os.Exit(0)
		default:
			p.FailSubcommand(err.Error(), p.SubcommandNames()...)
		}
	}

	if p.Subcommand() == nil {
		p.WriteHelp(os.Stdout)
		os.Exit(1)
	}

	var m tea.Model

	switch sub := p.Subcommand().(type) {
	case *AuthCmd:
		p.WriteHelpForSubcommand(os.Stdout, p.SubcommandNames()...)
		os.Exit(2)
	case *completions.Cmd: // "dbc completions" without specifying the shell type
		p.WriteHelpForSubcommand(os.Stdout, p.SubcommandNames()...)
		os.Exit(2)
	case completions.ShellImpl:
		fmt.Print(sub.GetScript())
		os.Exit(0)
	case modelCmd:
		m = sub.GetModel()
	}

	// f, err := tea.LogToFile("debug.log", "debug")
	// if err != nil {
	// 	fmt.Println("Error creating log file:", err)
	// 	os.Exit(1)
	// }
	// defer f.Close()

	if !isatty.IsTerminal(os.Stdout.Fd()) {
		prog = tea.NewProgram(m, tea.WithoutRenderer(), tea.WithInput(nil))
	} else if args.Quiet {
		// Quiet still prints stderr as GNU standard is to suppress "usual" output
		prog = tea.NewProgram(m, tea.WithoutRenderer(), tea.WithInput(nil), tea.WithOutput(os.Stderr))
	} else {
		prog = tea.NewProgram(m)
	}

	if m, err = prog.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error running program:", err)
		os.Exit(1)
	}

	if !args.Quiet {
		if fo, ok := m.(HasFinalOutput); ok {
			fmt.Println(fo.FinalOutput())
		}
	}

	if h, ok := m.(HasStatus); ok {
		os.Exit(h.Status())
	}
}
