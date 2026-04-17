// Copyright 2026 Columnar Technologies Inc.
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
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/alexflint/go-arg"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/auth"
	"github.com/columnar-tech/dbc/cmd/dbc/completions"
	"github.com/columnar-tech/dbc/config"
	"github.com/mattn/go-isatty"
)

var (
	errStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	checkMark = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).SetString("✓")
	skipMark  = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).SetString("-")
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

// HasPreamble is implemented by models that want to print text to stdout before
// the TUI renderer starts. This is a workaround for a regression in bubbletea
// v2 where tea.Println calls inside a model's Update() destroys scrollback. It
// might be this issue: https://github.com/charmbracelet/bubbletea/issues/1571
// or https://github.com/charmbracelet/bubbletea/issues/1613.
type HasPreamble interface {
	Preamble() string
}

type HasStatus interface {
	Status() int
	Err() error
}

// NeedsRenderer is implemented by models that render a live TUI (spinners,
// progress bars, interactive lists). Models that only use tea.Println /
// tea.Printf and return an empty View do not need the renderer.
type NeedsRenderer interface {
	NeedsRenderer()
}

var (
	dbcClient     *dbc.Client
	dbcClientOnce sync.Once
)

func newDefaultClient() (*dbc.Client, error) {
	var opts []dbc.Option
	if val := os.Getenv("DBC_BASE_URL"); val != "" {
		opts = append(opts, dbc.WithBaseURL(val))
	}
	return dbc.NewClient(opts...)
}

// use this so we can override this in tests
var getDriverRegistry = func() ([]dbc.Driver, error) {
	dbcClientOnce.Do(func() {
		if dbcClient == nil {
			dbcClient, _ = newDefaultClient()
		}
	})
	return dbcClient.Search("")
}

func findDriver(name string, drivers []dbc.Driver) (dbc.Driver, error) {
	idx := slices.IndexFunc(drivers, func(d dbc.Driver) bool {
		return d.Path == name
	})

	if idx == -1 {
		return dbc.Driver{}, fmt.Errorf("driver `%s` not found in driver registry index; try: `dbc search` to list available drivers", name)
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
	err    error
}

func (m baseModel) Init() tea.Cmd       { return nil }
func (m baseModel) View() tea.View      { return tea.NewView("") }
func (m baseModel) Status() int         { return m.status }
func (m baseModel) Err() error          { return m.err }
func (m baseModel) FinalOutput() string { return "" }

func (m baseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+d", "esc":
			return m, tea.Quit
		}
	case error:
		m.status, m.err = 1, msg
		return m, tea.Quit
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

func formatErr(err error) string {
	switch {
	case errors.Is(err, auth.ErrTrialExpired):
		return errStyle.Render("Could not download license, trial has expired")
	case errors.Is(err, auth.ErrNoTrialLicense):
		return errStyle.Render("Could not download license, trial not started")
	case errors.Is(err, dbc.ErrUnauthorized):
		return errStyle.Render(err.Error()) + "\n" +
			msgStyle.Render("Did you run `dbc auth login`?")
	case errors.Is(err, dbc.ErrUnauthorizedColumnar):
		return errStyle.Render(err.Error()) + "\n" +
			msgStyle.Render("Installing this driver requires a license. Verify you have an active license at https://console.columnar.tech/licenses and try this command again. Contact support@columnar.tech if you believe this is an error.")
	default:
		return errStyle.Render("Error: " + err.Error())
	}
}

var subcommandSuggestions = map[string]string{
	"list": "search",
}

func failSubcommandAndSuggest(p *arg.Parser, msg string, subcommand ...string) {
	// Extract the invalid command from os.Args by scanning for the first non-flag
	// arg
	var invalidCmd string
	if len(os.Args) > 1 {
		for _, arg := range os.Args[1:] {
			if !strings.HasPrefix(arg, "-") {
				invalidCmd = arg
				break
			}
		}
	}

	p.WriteUsageForSubcommand(os.Stderr, subcommand...)
	fmt.Fprintf(os.Stderr, "error: %s", msg)

	// Optionally add suggestion
	if invalidCmd != "" {
		if suggestion, ok := subcommandSuggestions[invalidCmd]; ok {
			fmt.Fprintf(os.Stderr, ". Did you mean: dbc %s?\n", suggestion)
		}
	}

	os.Exit(2)
}

func main() {
	var (
		args cmds
	)

	var clientErr error
	dbcClient, clientErr = newDefaultClient()
	if clientErr != nil {
		fmt.Println("Error initializing client:", clientErr)
		os.Exit(1)
	}

	p, err := newParser(&args)
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
			failSubcommandAndSuggest(p, err.Error(), p.SubcommandNames()...)
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
	case *LicenseCmd:
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

	_, needsRenderer := m.(NeedsRenderer)
	// Work around https://github.com/columnar-tech/dbc/issues/351
	usedRenderer := false
	if !isatty.IsTerminal(os.Stdout.Fd()) || !needsRenderer {
		prog = tea.NewProgram(m, tea.WithoutRenderer(), tea.WithInput(nil))
	} else if args.Quiet {
		// Quiet still prints stderr as GNU standard is to suppress "usual" output
		prog = tea.NewProgram(m, tea.WithoutRenderer(), tea.WithInput(nil), tea.WithOutput(os.Stderr))
	} else {
		prog = tea.NewProgram(m)
		usedRenderer = true
	}

	if !args.Quiet {
		if hp, ok := m.(HasPreamble); ok {
			if preamble := hp.Preamble(); preamble != "" {
				lipgloss.Print(preamble)
			}
		}
	}

	if m, err = prog.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error running program:", err)
		os.Exit(1)
	}

	// Work around https://github.com/columnar-tech/dbc/issues/351
	if usedRenderer {
		suppressTerminalProbeResponses()
	}

	if !args.Quiet {
		if fo, ok := m.(HasFinalOutput); ok {
			if output := fo.FinalOutput(); output != "" {
				// Use lipgloss.Println instead of fmt.Println so that
				// ANSI codes are automatically stripped when stdout is
				// not a terminal (e.g. piping to less or grep).
				lipgloss.Println(output)
			}
		}
	}

	if h, ok := m.(HasStatus); ok {
		if err := h.Err(); err != nil {
			lipgloss.Println(formatErr(err))
		}
		os.Exit(h.Status())
	}
}

func newParser(args *cmds) (*arg.Parser, error) {
	return arg.NewParser(arg.Config{Program: "dbc", EnvPrefix: "DBC_"}, args)
}
