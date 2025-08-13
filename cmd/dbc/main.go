// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"fmt"
	"os"

	"github.com/alexflint/go-arg"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/columnar-tech/dbc"
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

func main() {
	var args struct {
		List    *ListCmd       `arg:"subcommand" help:"List available drivers"`
		Config  *ViewConfigCmd `arg:"subcommand" help:"View driver config"`
		Init    *InitCmd       `arg:"subcommand" help:"Initialize a new DBC drivers list"`
		Add     *AddCmd        `arg:"subcommand" help:"Add a driver to the drivers list"`
		Install *InstallCmd    `arg:"subcommand" help:"Install driver"`
		Tui     *TuiCmd        `arg:"subcommand"`
	}

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
