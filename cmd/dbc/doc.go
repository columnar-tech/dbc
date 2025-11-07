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
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cli/browser"
	"github.com/columnar-tech/dbc"
	"github.com/mattn/go-isatty"
)

var dbcDocsUrl = "https://docs.columnar.tech/dbc/"

// Support drivers without a docs URL defined in the index
var fallbackDriverDocsUrl = map[string]string{
	"bigquery":   "http://example.com",
	"duckdb":     "https://arrow.apache.org/adbc/current/driver/duckdb.html",
	"flightsql":  "https://arrow.apache.org/adbc/current/driver/flight_sql.html",
	"mssql":      "http://example.com",
	"mysql":      "http://example.com",
	"postgresql": "https://arrow.apache.org/adbc/current/driver/postgresql.html",
	"redshift":   "http://example.com",
	"snowflake":  "https://arrow.apache.org/adbc/current/driver/snowflake.html",
	"sqlite":     "https://arrow.apache.org/adbc/current/driver/sqlite.html",
	"trino":      "http://example.com",
}

type docsUrlFound string
type successMsg string

type DocCmd struct {
	Driver string `arg:"positional" help:"Driver to open documentation for"`
}

func (c DocCmd) GetModelCustom(baseModel baseModel, isHeadless bool, openBrowserFunc func(string) error, fallbackUrls map[string]string) tea.Model {
	return docModel{
		baseModel:    baseModel,
		driver:       c.Driver,
		isHeadless:   isHeadless,
		fallbackUrls: fallbackUrls,
		openBrowser:  openBrowserFunc,
	}
}

func (c DocCmd) GetModel() tea.Model {
	isHeadless := !isatty.IsTerminal(os.Stdout.Fd())
	return c.GetModelCustom(baseModel{
		getDriverList: getDriverList,
		downloadPkg:   downloadPkg,
	}, isHeadless, browser.OpenURL, fallbackDriverDocsUrl)
}

type docModel struct {
	baseModel

	driver         string
	drv            *dbc.Driver
	urlToOpen      string
	waitingForUser bool
	isHeadless     bool
	fallbackUrls   map[string]string
	openBrowser    func(string) error
}

func (m docModel) Init() tea.Cmd {
	return func() tea.Msg {
		if m.driver == "" {
			return docsUrlFound(dbcDocsUrl)
		}

		drivers, err := m.getDriverList()
		if err != nil {
			return err
		}

		drv, err := findDriver(m.driver, drivers)
		if err != nil {
			return err
		}

		return drv
	}
}

func (m docModel) openBrowserCmd(url string) tea.Cmd {
	return func() tea.Msg {
		if err := m.openBrowser(url); err != nil {
			return fmt.Errorf("failed to open browser: %w", err)
		}
		return successMsg("Opening documentation in browser...")
	}
}

func (m docModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case dbc.Driver:
		m.drv = &msg
		// TODO: Add logic for finding driver docs from index. For now, we only use
		// fallback URLs.
		url, keyExists := m.fallbackUrls[msg.Path]
		if !keyExists {
			return m, func() tea.Msg {
				return fmt.Errorf("no documentation available for driver `%s`", msg.Path)
			}
		} else {
			return m, func() tea.Msg {
				return docsUrlFound(url)
			}
		}

	case docsUrlFound:
		m.urlToOpen = string(msg)

		// In headless mode, just print the URL and quit
		if m.isHeadless {
			return m, tea.Sequence(tea.Println(m.urlToOpen), tea.Quit)
		}

		m.waitingForUser = true
		return m, nil
	case successMsg:
		return m, tea.Sequence(tea.Println(string(msg)), tea.Quit)
	case tea.KeyMsg:
		if m.waitingForUser {
			switch msg.Type {
			case tea.KeyCtrlC, tea.KeyCtrlD, tea.KeyEsc:
				return m, tea.Quit
			}

			switch msg.String() {
			case "y", "Y":
				m.waitingForUser = false
				return m, m.openBrowserCmd(m.urlToOpen)
			case "n", "N":
				return m, tea.Quit
			}
		}
	default:
		bm, cmd := m.baseModel.Update(msg)
		m.baseModel = bm.(baseModel)
		return m, cmd
	}

	return m, nil
}

func (m docModel) View() string {
	if m.waitingForUser {
		return fmt.Sprintf("Open browser to %s? (y/n): ", m.urlToOpen)
	}
	return ""
}
