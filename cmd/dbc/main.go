// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"fmt"
	"os"

	"github.com/alexflint/go-arg"
	tea "github.com/charmbracelet/bubbletea"
)

type TuiCmd struct{}

func (TuiCmd) GetModel() tea.Model {
	return getTuiModel()
}

type modelCmd interface {
	GetModel() tea.Model
}

func main() {
	var args struct {
		Fetch   *FetchCmd      `arg:"subcommand" help:"Fetch list of drivers"`
		Config  *ViewConfigCmd `arg:"subcommand" help:"View driver config"`
		Install *InstallCmd    `arg:"subcommand" help:"Install driver"`
		Tui     *TuiCmd        `arg:"subcommand"`
	}

	p := arg.MustParse(&args)
	if p.Subcommand() == nil {
		p.WriteHelp(os.Stdout)
		os.Exit(1)
	}

	m := p.Subcommand().(modelCmd).GetModel()
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}
