// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"runtime"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/list"
	"github.com/charmbracelet/lipgloss/tree"
	"github.com/columnar-tech/dbc"
)

var (
	nameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("35"))
	descStyle = lipgloss.NewStyle().Italic(true)
	bold      = lipgloss.NewStyle().Bold(true)

	archStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))

	platformTuple string
)

func init() {
	os := runtime.GOOS
	switch os {
	case "darwin":
		os = "macosx"
	case "windows": // change this when we update the manifest.yaml
		os = "win"
	case "freebsd", "linux":
	default:
		os = "unknown"
	}

	arch := runtime.GOARCH
	switch arch {
	case "386":
		arch = "x86"
	case "riscv64":
		arch = "riscv"
	case "ppc64", "ppc64le":
		arch = "powerpc"
	case "390x", "arm64", "amd64", "arm":
	default:
		arch = "unknown"
	}

	platformTuple = os + "_" + arch
}

type ListCmd struct {
	// URI url.URL `arg:"-u" placeholder:"URL" help:"Base URL for fetching drivers"`
	Verbose bool `arg:"-v" help:"Enable verbose output"`
}

func (f ListCmd) GetModel() tea.Model {
	return simpleFetchModel{
		verbose: f.Verbose,
	}
}

type simpleFetchModel struct {
	status  int
	verbose bool
}

func (m simpleFetchModel) Status() int {
	return m.status
}

func (m simpleFetchModel) Init() tea.Cmd {
	return tea.Sequence(
		tea.Printf(archStyle.Render("Current System: %s"), platformTuple),
		tea.Println(),
		func() tea.Msg {
			drivers, err := getDriverList()
			if err != nil {
				return err
			}
			return drivers
		})
}

func (m simpleFetchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case []dbc.Driver:
		return m, tea.Sequence(
			tea.Println(viewDrivers(msg, m.verbose)), tea.Quit)
	case error:
		m.status = 1
		return m, tea.Sequence(
			tea.Println("Error fetching drivers:", msg),
			tea.Quit)
	}
	return m, nil
}

func (m simpleFetchModel) View() string {
	return ""
}

func emptyEnumerator(_ list.Items, _ int) string {
	return ""
}

func viewDrivers(d []dbc.Driver, verbose bool) string {
	l := list.New()
	for _, driver := range d {
		if !verbose {
			l.Item(nameStyle.Render(driver.Path) + " - " + descStyle.Render(driver.Desc))
			continue
		}

		versionTree := tree.Root(bold.Render("Versions:")).
			Enumerator(tree.RoundedEnumerator)
		for _, v := range driver.Versions(platformTuple) {
			versionTree.Child(v)
		}

		l.Item(driver.Path).Item(
			list.New(bold.Render("Title: ")+descStyle.Render(driver.Title), bold.Render("Description: ")+descStyle.Render(driver.Desc),
				bold.Render("License: ")+driver.License,
				versionTree,
			).Enumerator(emptyEnumerator))
	}

	return l.String()
}
