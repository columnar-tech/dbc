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
	"duckdb":     "https://arrow.apache.org/adbc/current/driver/duckdb.html",
	"flightsql":  "https://arrow.apache.org/adbc/current/driver/flight_sql.html",
	"postgresql": "https://arrow.apache.org/adbc/current/driver/postgresql.html",
	"snowflake":  "https://arrow.apache.org/adbc/current/driver/snowflake.html",
	"sqlite":     "https://arrow.apache.org/adbc/current/driver/sqlite.html",
}

var openBrowserFunc = browser.OpenURL

type docsUrlFound string

type DocsCmd struct {
	Driver string `arg:"positional" help:"Driver to open documentation for"`
	NoOpen bool   `arg:"--no-open" help:"Print the documentation URL instead of opening it in a web browser"`
}

func (c DocsCmd) GetModelCustom(baseModel baseModel, isHeadless bool, openBrowserFunc func(string) error, fallbackUrls map[string]string) tea.Model {
	return docsModel{
		baseModel:    baseModel,
		driver:       c.Driver,
		isHeadless:   isHeadless,
		fallbackUrls: fallbackUrls,
		openBrowser:  openBrowserFunc,
	}
}

func (c DocsCmd) GetModel() tea.Model {
	isHeadless := !isatty.IsTerminal(os.Stdout.Fd()) || c.NoOpen
	return c.GetModelCustom(baseModel{
		getDriverList: getDriverList,
		downloadPkg:   downloadPkg,
	}, isHeadless, openBrowserFunc, fallbackDriverDocsUrl)
}

type docsModel struct {
	baseModel

	driver       string
	drv          *dbc.Driver
	urlToOpen    string
	isHeadless   bool
	fallbackUrls map[string]string
	openBrowser  func(string) error
}

func (m docsModel) Init() tea.Cmd {
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

func (m docsModel) openBrowserCmd(url string) tea.Cmd {
	return func() tea.Msg {
		if err := m.openBrowser(url); err != nil {
			return fmt.Errorf("failed to open browser: %w", err)
		}
		return tea.Quit()
	}
}

func (m docsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

		// In headless mode, just quit and let FinalOutput handle printing
		if m.isHeadless {
			return m, tea.Quit
		}

		// Automatically open browser
		return m, m.openBrowserCmd(m.urlToOpen)
	default:
		bm, cmd := m.baseModel.Update(msg)
		m.baseModel = bm.(baseModel)
		return m, cmd
	}

	return m, nil
}

func (m docsModel) View() string {
	return ""
}

func (m docsModel) FinalOutput() string {
	if m.isHeadless && m.urlToOpen != "" {
		var docName string
		if m.driver == "" {
			docName = "dbc"
		} else {
			docName = m.driver + " driver"
		}
		return fmt.Sprintf("%s docs are available at the following URL:\n%s\n", docName, m.urlToOpen)
	}
	return ""
}
