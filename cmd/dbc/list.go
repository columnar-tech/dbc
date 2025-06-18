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
		// TODO use golang.org/x/sys/unix to check uts.Sysname/uts.Release
		// to find the actual release version such as 10_15 vs 11_00 etc.
		os = "macosx"
	case "windows":
		os = "win"
	}

	arch := runtime.GOARCH

	platformTuple = os + "_" + arch
}

type ListCmd struct {
	// URI url.URL `arg:"-u" placeholder:"URL" help:"Base URL for fetching drivers"`
}

func (f ListCmd) GetModel() tea.Model {
	return simpleFetchModel{}
}

type simpleFetchModel struct{}

func (m simpleFetchModel) Init() tea.Cmd {
	return tea.Sequence(
		tea.Printf(archStyle.Render("Current System: %s"), platformTuple),
		tea.Println(),
		func() tea.Msg {
			drivers, err := dbc.GetDriverList()
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
			tea.Println(viewDrivers(msg)), tea.Quit)
	case error:
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

func viewDrivers(d []dbc.Driver) string {
	l := list.New().ItemStyle(nameStyle)
	for _, driver := range d {
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
