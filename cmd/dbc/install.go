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
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"
	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
)

func manifestToPackageInfo(m config.Manifest) dbc.PkgInfo {
	return dbc.PkgInfo{
		Driver: dbc.Driver{
			Title:   m.Name,
			Path:    m.ID,
			License: m.License,
		},
		Version: m.Version,
	}
}

func parseDriverConstraint(driver string) (string, *semver.Constraints, error) {
	driver = strings.TrimSpace(driver)
	splitIdx := strings.IndexAny(driver, " ~^<>=!")
	if splitIdx == -1 {
		return driver, nil, nil
	}

	driverName := driver[:splitIdx]
	constraints, err := semver.NewConstraint(strings.TrimSpace(driver[splitIdx:]))
	if err != nil {
		return "", nil, fmt.Errorf("invalid version constraint: %w", err)
	}

	return driverName, constraints, nil
}

type InstallCmd struct {
	// URI    url.URL `arg:"-u" placeholder:"URL" help:"Base URL for fetching drivers"`
	Driver             []string           `arg:"positional,required" help:"One or more drivers to install, optionally with a version constraint (for example: mysql, mysql=0.1.0, mysql>=1,<2)"`
	Level              config.ConfigLevel `arg:"-l" help:"Config level to install to (user, system)"`
	Json               bool               `arg:"--json" help:"Print output as JSON instead of plaintext"`
	JsonStreamProgress bool               `arg:"--json-stream-progress" help:"Stream progress events as JSON lines (implies --json)"`
	NoVerify           bool               `arg:"--no-verify" help:"Allow installation of drivers without a signature file"`
	Pre                bool               `arg:"--pre" help:"Allow implicit installation of pre-release versions"`
	InsecureNoChecksum bool               `arg:"--insecure-no-checksum" help:"Skip sha256 checksum recording (not recommended)"`
}

func (InstallCmd) Description() string {
	return "Install one or more drivers.\n\n" +
		"Each `DRIVER` may include a version constraint, for example `dbc install mysql`, `dbc install \"mysql=0.1.0\"`, or `dbc install \"mysql>=1,<2\"`.\n" +
		"Drivers are installed in the order given. If a driver fails to install, the drivers after it are not installed.\n" +
		"See https://docs.columnar.tech/dbc/guides/installing/#version-constraints for more on version constraint syntax."
}

func (c InstallCmd) GetModelCustom(baseModel baseModel) tea.Model {
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	return installModel{
		baseModel:          baseModel,
		Drivers:            c.Driver,
		NoVerify:           c.NoVerify,
		jsonOutput:         c.Json || c.JsonStreamProgress,
		jsonStreamProgress: c.JsonStreamProgress,
		Pre:                c.Pre,
		insecureNoChecksum: c.InsecureNoChecksum,
		spinner:            s,
		cfg:                getConfig(c.Level),
	}
}

func (c InstallCmd) GetModel() tea.Model {
	return c.GetModelCustom(defaultBaseModel())
}

func verifySignature(m config.Manifest, noVerify bool) error {
	if m.Files.Driver == "" || noVerify {
		return nil
	}

	path := filepath.Dir(m.Driver.Shared.Get(config.PlatformTuple()))

	lib, err := os.Open(filepath.Join(path, m.Files.Driver))
	if err != nil {
		return fmt.Errorf("could not open driver file: %w", err)
	}
	defer lib.Close()

	sigFile := m.Files.Signature
	if sigFile == "" {
		sigFile = m.Files.Driver + ".sig"
	}

	sig, err := os.Open(filepath.Join(path, sigFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("signature file '%s' for driver is missing", sigFile)
		}
		return fmt.Errorf("failed to open signature file: %w", err)
	}
	defer sig.Close()

	if err := dbc.SignedByColumnar(lib, sig); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

type writeDriverManifestMsg struct {
	DriverInfo config.DriverInfo
}

type localInstallMsg struct{}

// alreadyInstalledChecksumMsg carries the checksum computed for an already-installed driver.
type alreadyInstalledChecksumMsg string

type installState int

const (
	stSearching installState = iota
	stDownloading
	stInstalling
	stVerifying
	stDone
)

func (s installState) String() string {
	switch s {
	case stSearching:
		return "searching"
	case stDownloading:
		return "downloading"
	case stVerifying:
		return "verifying signature"
	case stInstalling:
		return "installing"
	default:
		return "done"
	}
}

// installDoneMsg is sent by a progressiveInstallModel once its driver is
// installed, or found to be already installed.
type installDoneMsg struct{}

func installDone() tea.Msg { return installDoneMsg{} }

func (m progressiveInstallModel) emitJSON(kind string, payload any) {
	out := m.jsonOut
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintln(out, marshalEnvelope(kind, payload))
}

func (m progressiveInstallModel) addEvent(event string, extra ...func(*jsonschema.InstallProgressEvent)) progressiveInstallModel {
	if !m.jsonStreamProgress {
		return m
	}
	evt := jsonschema.InstallProgressEvent{
		Event:  event,
		Driver: m.Driver,
	}
	for _, fn := range extra {
		fn(&evt)
	}
	m.emitJSON("install.progress", evt)
	return m
}

// progressiveInstallModel installs a single driver whose package has already
// been resolved. It is driven by installModel, which sends it either the
// resolved dbc.PkgInfo or a localInstallMsg to start, and it reports
// completion with installDoneMsg. Errors are returned as messages and handled
// by installModel, which also renders its progress.
type progressiveInstallModel struct {
	baseModel

	Driver             string
	NoVerify           bool
	jsonOutput         bool
	jsonStreamProgress bool
	Pre                bool
	cfg                config.Config

	insecureNoChecksum  bool
	installedDriverInfo config.DriverInfo

	DriverPackage      dbc.PkgInfo
	conflictingInfo    config.DriverInfo
	postInstallMessage string

	state   installState
	spinner spinner.Model
	p       FileProgressModel

	width, height    int
	isLocal          bool
	localPackagePath string

	alreadyInstalledChecksum string
	jsonOut                  io.Writer
}

func (m progressiveInstallModel) hasConflict() bool {
	return m.conflictingInfo.ID != "" && m.conflictingInfo.Version != nil
}

func (m progressiveInstallModel) isAlreadyInstalled() bool {
	return m.conflictingInfo.ID != "" && m.conflictingInfo.Version != nil &&
		m.conflictingInfo.Version.Equal(m.DriverPackage.Version)
}

// result reports the outcome of a finished install. An error is only
// returned in JSON mode, when the checksum of the installed driver can't be
// computed.
func (m progressiveInstallModel) result() (jsonschema.InstallStatus, error) {
	location := filepath.SplitList(m.cfg.Location)[0]
	if m.isAlreadyInstalled() {
		return jsonschema.InstallStatus{
			Status:   "already installed",
			Driver:   m.conflictingInfo.ID,
			Version:  m.conflictingInfo.Version.String(),
			Location: location,
			Checksum: m.alreadyInstalledChecksum,
		}, nil
	}

	installStatus := jsonschema.InstallStatus{
		Status:   "installed",
		Driver:   m.Driver,
		Version:  m.DriverPackage.Version.String(),
		Location: location,
		Message:  m.postInstallMessage,
	}
	if m.hasConflict() {
		installStatus.Conflict = fmt.Sprintf("%s (version: %s)", m.conflictingInfo.ID, m.conflictingInfo.Version)
	}

	if m.jsonOutput && !m.insecureNoChecksum && m.installedDriverInfo.Driver.Shared.Get(config.PlatformTuple()) != "" {
		chksum, err := checksum(m.installedDriverInfo.Driver.Shared.Get(config.PlatformTuple()))
		if err != nil {
			return installStatus, err
		}
		installStatus.Checksum = chksum
	}
	return installStatus, nil
}

func (m progressiveInstallModel) startDownloading() (progressiveInstallModel, tea.Cmd) {
	m.state = stDownloading
	if m.isAlreadyInstalled() {
		m.state = stDone
		if m.jsonOutput && !m.insecureNoChecksum && m.conflictingInfo.Driver.Shared.Get(config.PlatformTuple()) != "" {
			driverPath := m.conflictingInfo.Driver.Shared.Get(config.PlatformTuple())
			return m, func() tea.Msg {
				chksum, err := checksum(driverPath)
				if err != nil {
					return fmt.Errorf("checksum_failed: %w", err)
				}
				return alreadyInstalledChecksumMsg(chksum)
			}
		}
		return m, installDone
	}

	m = m.addEvent("download.start")
	return m, func() tea.Msg {
		output, err := m.downloadPkg(m.DriverPackage)
		if err != nil {
			return err
		}
		return output
	}
}

func (m progressiveInstallModel) startInstalling(downloaded *os.File) (progressiveInstallModel, tea.Cmd) {
	m.state = stInstalling
	return m, func() tea.Msg {
		if m.conflictingInfo.ID != "" {
			if err := config.UninstallDriver(m.cfg, m.conflictingInfo); err != nil {
				return err
			}
		}

		manifest, err := config.InstallDriver(m.cfg, m.Driver, downloaded)
		if err != nil {
			return err
		}
		return manifest
	}
}

func (m progressiveInstallModel) Update(msg tea.Msg) (progressiveInstallModel, tea.Cmd) {
	switch msg := msg.(type) {
	case alreadyInstalledChecksumMsg:
		m.alreadyInstalledChecksum = string(msg)
		return m, installDone
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case progressMsg:
		if m.jsonOutput {
			m = m.addEvent("download.progress", func(e *jsonschema.InstallProgressEvent) {
				e.Bytes = msg.written
				e.Total = msg.total
			})
		}
		progressCmd := m.p.SetPercent(msg.written, msg.total)
		return m, progressCmd
	case progress.FrameMsg:
		var cmd tea.Cmd
		m.p, cmd = m.p.Update(msg)
		return m, cmd
	case localInstallMsg:
		return m, func() tea.Msg {
			localDrv, err := os.Open(m.localPackagePath)
			if err != nil {
				return err
			}
			return localDrv
		}
	case dbc.PkgInfo:
		m.DriverPackage = msg
		di, err := config.GetDriver(m.cfg, m.Driver)
		if err == nil {
			m.conflictingInfo = di
		}

		return m.startDownloading()
	case *os.File:
		m = m.addEvent("download.complete")
		m = m.addEvent("extract.start")
		return m.startInstalling(msg)
	case config.Manifest:
		if m.DriverPackage.Version == nil {
			m.DriverPackage = manifestToPackageInfo(msg)
		}

		m.state = stVerifying
		m.postInstallMessage = strings.Join(msg.PostInstall.Messages, "\n")
		m = m.addEvent("extract.complete")
		m = m.addEvent("verify.start")
		return m, func() tea.Msg {
			if err := verifySignature(msg, m.NoVerify); err != nil {
				path := filepath.Dir(msg.Driver.Shared.Get(config.PlatformTuple()))
				_ = os.RemoveAll(path)
				return err
			}
			return writeDriverManifestMsg{DriverInfo: msg.DriverInfo}
		}
	case writeDriverManifestMsg:
		m.state = stDone
		m.installedDriverInfo = msg.DriverInfo
		m = m.addEvent("verify.complete")
		m = m.addEvent("manifest.create")
		return m, tea.Sequence(func() tea.Msg {
			return config.CreateManifest(m.cfg, msg.DriverInfo)
		}, installDone)
	}

	return m, nil
}

// installSpec is one DRIVER argument to `dbc install`.
type installSpec struct {
	Name string
	// Constraints is nil when no version constraint was given.
	Constraints *semver.Constraints
	// LocalPath is set when installing from a local package.
	LocalPath string
}

func isLocalPackage(driver string) bool {
	return strings.HasSuffix(driver, ".tar.gz") || strings.HasSuffix(driver, ".tgz")
}

// localPackageDriverName derives the driver name from a local package file
// name of the form drivername_platform_arch_version.tar.gz.
func localPackageDriverName(path string) string {
	driverName := strings.TrimSuffix(
		strings.TrimSuffix(filepath.Base(path), ".tar.gz"), ".tgz")
	parts := strings.Split(driverName, "_"+config.PlatformTuple()+"_")
	if len(parts) < 2 {
		return driverName
	}
	return parts[0]
}

func parseInstallSpecs(drivers []string) ([]installSpec, error) {
	specs := make([]installSpec, 0, len(drivers))
	seen := make(map[string]bool, len(drivers))
	for _, d := range drivers {
		var spec installSpec
		if isLocalPackage(d) {
			spec = installSpec{Name: localPackageDriverName(d), LocalPath: d}
		} else {
			name, vers, err := parseDriverConstraint(d)
			if err != nil {
				return nil, err
			}
			spec = installSpec{Name: name, Constraints: vers}
		}

		if seen[spec.Name] {
			return nil, fmt.Errorf("driver `%s` specified more than once", spec.Name)
		}
		seen[spec.Name] = true
		specs = append(specs, spec)
	}
	return specs, nil
}

// resolvedPackagesMsg holds the resolved package for each installSpec, in
// order. Entries for local packages are left empty.
type resolvedPackagesMsg []dbc.PkgInfo

type rowState int

const (
	rowWaiting rowState = iota
	rowActive
	rowInstalled
	rowAlreadyInstalled
	rowFailed
	rowNotInstalled
)

// installRow is one driver's line in the progress tree.
type installRow struct {
	// Label is the DRIVER argument as typed.
	Label string
	// Name and Version are filled in as the driver is resolved and installed.
	Name    string
	Version string
	State   rowState
	// Replaced is the version of a conflicting driver that was removed.
	Replaced string
	Message  string
}

const treeDetailIndent = 8

var (
	failMark = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).SetString("✗")
	waitMark = lipgloss.NewStyle().Faint(true).SetString("○")
)

// renderTree draws the progress tree. The text in the tree is shown both live
// (via View) and as the final summary (via FinalOutput), so it doesn't depend
// on tea.Println, whose output is dropped when running without a renderer.
func (m installModel) renderTree() string {
	t := tree.New()
	for _, row := range m.rows {
		var title string
		var detail string
		switch row.State {
		case rowWaiting, rowActive:
			mark := waitMark.String()
			if row.State == rowActive {
				mark = m.spinner.View()
				detail = m.current.state.String()
				if m.current.state == stDownloading {
					detail += " " + m.current.p.View()
				}
			} else {
				detail = "waiting"
			}
			title = mark + " " + row.Label
			if row.Version != "" {
				title += " → " + row.Version
			}
		case rowInstalled, rowAlreadyInstalled:
			title = checkMark.String() + " " + row.Name + " " + row.Version
			if row.State == rowAlreadyInstalled {
				title += " (already installed)"
			} else if row.Replaced != "" {
				title += " (replaced " + row.Replaced + ")"
			}
			if row.Message != "" {
				detail = postMsgStyle.Render(row.Message)
			}
		case rowFailed:
			title = failMark.String() + " " + row.Label
			if row.Version != "" {
				title += " → " + row.Version
			}
			detail = formatErr(m.err)
		case rowNotInstalled:
			title = skipMark.String() + " " + row.Label + " (not installed)"
		}

		if detail == "" {
			t.Child(title)
		} else {
			// Wrap long details (e.g. license hints) so they keep their
			// indentation instead of wrapping to column 0. treeDetailIndent
			// is the width of "│   └── ".
			if m.width > treeDetailIndent {
				detail = lipgloss.NewStyle().Width(m.width - treeDetailIndent).Render(detail)
			}
			t.Child(tree.Root(title).Child(detail))
		}
	}

	// lipgloss pads multi-line entries to a common width; trim that so piped
	// output doesn't end in trailing spaces.
	lines := strings.Split(t.String(), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

// installModel implements `dbc install`. It resolves every requested driver
// up front, then installs them one at a time in the order given, stopping at
// the first error. This matches the behavior of `dbc sync`.
type installModel struct {
	baseModel

	Drivers            []string
	NoVerify           bool
	jsonOutput         bool
	jsonStreamProgress bool
	Pre                bool
	insecureNoChecksum bool
	cfg                config.Config

	specs    []installSpec
	packages []dbc.PkgInfo
	// index of the driver currently being installed in specs
	index   int
	started bool
	current progressiveInstallModel

	spinner       spinner.Model
	width, height int

	registryErrors error

	installed []jsonschema.InstallStatus
	skipped   []jsonschema.InstallStatus
	// rows holds the per-driver progress shown in the tree, in argument order
	rows []installRow
	// finished is set once the last driver is done or a driver has failed
	finished bool
	// compact is set once the full view no longer fits in the terminal. The
	// view then shows only the header, and the tree is printed after exit.
	compact bool

	jsonOut         io.Writer
	jsonErrorOutput string // JSON error envelope to emit via FinalOutput
}

func (installModel) NeedsRenderer() {}

func (m installModel) IsJSONMode() bool { return m.jsonOutput }

func (m installModel) WithJSONWriter(w io.Writer) tea.Model {
	m.jsonOut = w
	return m
}

func (m installModel) Init() tea.Cmd {
	specs, err := parseInstallSpecs(m.Drivers)
	if err != nil {
		return errCmd("%w", err)
	}

	needsRegistry := false
	for _, spec := range specs {
		if spec.LocalPath == "" {
			needsRegistry = true
		}
	}

	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		if !needsRegistry {
			return specs
		}

		installDir := "."
		if locs := filepath.SplitList(m.cfg.Location); len(locs) > 0 && locs[0] != "" {
			installDir = locs[0]
		}
		lockDir := installDir
		for {
			if _, err := os.Stat(lockDir); err == nil {
				break
			}
			parent := filepath.Dir(lockDir)
			if parent == lockDir {
				lockDir = os.TempDir()
				break
			}
			lockDir = parent
		}
		lockPath := filepath.Join(lockDir, ".dbc.install.lock")
		lock, err := acquireLock(lockPath, 10*time.Second)
		if err != nil {
			return err
		}
		defer lock.Release()

		drivers, err := m.getDriverRegistry()
		return specsWithRegistry{
			specs:   specs,
			drivers: drivers,
			err:     err,
		}
	})
}

type driversWithRegistryError struct {
	drivers []dbc.Driver
	err     error
}

type specsWithRegistry struct {
	specs   []installSpec
	drivers []dbc.Driver
	err     error
}

// resolvePackage finds the package to install for a registry driver.
func (m installModel) resolvePackage(spec installSpec, list []dbc.Driver) (dbc.PkgInfo, error) {
	d, err := findDriver(spec.Name, list)
	if err != nil {
		// If we have registry errors, enhance the error message
		if m.registryErrors != nil {
			return dbc.PkgInfo{}, fmt.Errorf("could not find driver: %w\n\nNote: Some driver registries were unavailable:\n%s", err, m.registryErrors.Error())
		}
		return dbc.PkgInfo{}, fmt.Errorf("could not find driver: %w", err)
	}

	if spec.Constraints != nil {
		spec.Constraints.IncludePrerelease = m.Pre
		return d.GetWithConstraint(spec.Constraints, config.PlatformTuple())
	}

	pkg, err := d.GetPackage(nil, config.PlatformTuple(), m.Pre)
	if err != nil {
		if !m.Pre && !d.HasNonPrerelease() {
			for _, cfg := range config.Get() {
				if di, ok := cfg.Drivers[spec.Name]; ok && di.Version != nil && di.Version.Prerelease() != "" {
					return dbc.PkgInfo{}, fmt.Errorf("driver `%s` is already installed (version %s); only pre-release versions are available for this driver; to update, use: dbc install --pre %s", spec.Name, di.Version, spec.Name)
				}
			}
		}
		return dbc.PkgInfo{}, err
	}
	return pkg, nil
}

func (m installModel) resolveAll(list []dbc.Driver) tea.Cmd {
	return func() tea.Msg {
		packages := make(resolvedPackagesMsg, len(m.specs))
		for i, spec := range m.specs {
			if spec.LocalPath != "" {
				// Only check that the archive can be opened here; it's read
				// and verified when its turn comes.
				f, err := os.Open(spec.LocalPath)
				if err != nil {
					return err
				}
				f.Close()
				continue
			}
			pkg, err := m.resolvePackage(spec, list)
			if err != nil {
				return err
			}
			packages[i] = pkg
		}
		return packages
	}
}

func (m installModel) newDriverModel(spec installSpec) progressiveInstallModel {
	return progressiveInstallModel{
		baseModel:          m.baseModel,
		Driver:             spec.Name,
		NoVerify:           m.NoVerify,
		jsonOutput:         m.jsonOutput,
		jsonStreamProgress: m.jsonStreamProgress,
		Pre:                m.Pre,
		insecureNoChecksum: m.insecureNoChecksum,
		cfg:                m.cfg,
		spinner:            m.spinner,
		width:              m.width,
		height:             m.height,
		isLocal:            spec.LocalPath != "",
		localPackagePath:   spec.LocalPath,
		jsonOut:            m.jsonOut,
		p: NewFileProgress(
			progress.WithDefaultBlend(),
			progress.WithWidth(20),
			progress.WithoutPercentage(),
		),
	}
}

// startCurrent begins installing the driver at m.index.
func (m installModel) startCurrent() (installModel, tea.Cmd) {
	m.started = true
	m.current = m.newDriverModel(m.specs[m.index])

	var startMsg tea.Msg = m.packages[m.index]
	if m.current.isLocal {
		m.current.state = stInstalling
		startMsg = localInstallMsg{}
	}
	m.rows[m.index].State = rowActive
	var cmd tea.Cmd
	m.current, cmd = m.current.Update(startMsg)
	return m, cmd
}

func (m installModel) fail(code string, err error) (tea.Model, tea.Cmd) {
	m.status = 1
	m.err = err
	m.finished = true
	if m.started {
		m.rows[m.index].State = rowFailed
		for i := m.index + 1; i < len(m.rows); i++ {
			m.rows[i].State = rowNotInstalled
		}
	}
	if m.jsonOutput {
		m.jsonErrorOutput = marshalEnvelope("error", jsonschema.ErrorResponse{
			Code:    code,
			Message: err.Error(),
		})
	}
	return m, tea.Quit
}

func (m installModel) driverDone() (tea.Model, tea.Cmd) {
	installStatus, err := m.current.result()
	if err != nil {
		return m.fail("checksum_failed", err)
	}

	row := &m.rows[m.index]
	row.Name, row.Version = installStatus.Driver, installStatus.Version
	if m.current.isAlreadyInstalled() {
		row.State = rowAlreadyInstalled
		m.skipped = append(m.skipped, installStatus)
	} else {
		row.State = rowInstalled
		row.Message = installStatus.Message
		if m.current.hasConflict() {
			row.Replaced = m.current.conflictingInfo.Version.String()
		}
		m.installed = append(m.installed, installStatus)
		if installStatus.Checksum != "" {
			m.current.addEvent("verify.checksum.ok", func(e *jsonschema.InstallProgressEvent) {
				e.Checksum = installStatus.Checksum
			})
		}
		m.current.addEvent("install.complete")
	}
	if m.jsonOutput {
		m.current.emitJSON("install.status", installStatus)
	}

	if m.index == len(m.specs)-1 {
		m.finished = true
		return m, tea.Quit
	}

	m.index++
	return m.startCurrent()
}

func (m installModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	if im, ok := next.(installModel); ok && !im.compact && im.tooTall() {
		im.compact = true
		next = im
	}
	return next, cmd
}

// tooTall reports whether the full view is taller than the terminal, in which
// case bubbletea would crop it from the top.
func (m installModel) tooTall() bool {
	if m.height <= 0 || m.rows == nil || m.jsonOutput {
		return false
	}
	// +1 for the trailing newline View adds.
	return strings.Count(m.render(), "\n")+2 > m.height
}

func (m installModel) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Once a driver has failed, ignore anything still in flight so that no
	// further drivers are started before the program quits.
	if m.status != 0 {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.started {
			m.current, _ = m.current.Update(msg)
		}
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.current.spinner = m.spinner
		return m, cmd
	case []installSpec:
		m.specs = msg
		return m, m.resolveAll(nil)
	case specsWithRegistry:
		m.specs = msg.specs
		m.registryErrors = msg.err
		if len(msg.drivers) == 0 && msg.err != nil {
			return m, errCmd("%w", msg.err)
		}
		return m, m.resolveAll(msg.drivers)
	case resolvedPackagesMsg:
		m.packages = msg
		m.rows = make([]installRow, len(m.specs))
		for i, spec := range m.specs {
			m.rows[i] = installRow{Label: m.Drivers[i], Name: spec.Name}
			if msg[i].Version != nil {
				m.rows[i].Version = msg[i].Version.String()
			}
		}
		return m.startCurrent()
	case installDoneMsg:
		return m.driverDone()
	case error:
		return m.fail("install_failed", msg)
	case tea.KeyPressMsg:
		base, cmd := m.baseModel.Update(msg)
		m.baseModel = base.(baseModel)
		return m, cmd
	}

	if !m.started {
		return m, nil
	}
	var cmd tea.Cmd
	m.current, cmd = m.current.Update(msg)
	return m, cmd
}

func (m installModel) FinalOutput() string {
	if m.jsonOutput {
		if m.status != 0 {
			return m.jsonErrorOutput
		}

		installed, skipped := m.installed, m.skipped
		if installed == nil {
			installed = []jsonschema.InstallStatus{}
		}
		if skipped == nil {
			skipped = []jsonschema.InstallStatus{}
		}
		return marshalEnvelope("install.result", jsonschema.InstallResult{
			Installed: installed,
			Skipped:   skipped,
			Errors:    []jsonschema.InstallError{},
		})
	}

	// The final frame is the summary. On failure it shows which drivers were
	// installed before the error, and the error itself.
	if len(m.rows) == 0 {
		return ""
	}
	if m.compact {
		// The header is already on screen as the last frame.
		return m.renderTree()
	}
	return m.render()
}

// ErrorDisplayed reports whether the error is already shown in the output
// (under the failed driver), so main shouldn't print it again. Errors from
// before any driver started, such as a driver not being found, aren't.
func (m installModel) ErrorDisplayed() bool {
	return !m.jsonOutput && m.started
}

// render draws the header and progress tree.
func (m installModel) render() string {
	return m.header() + "\n" + m.renderTree()
}

// header draws the "Resolved ..." and "Installing/Installed ..." lines.
func (m installModel) header() string {
	noun := "drivers"
	if len(m.rows) == 1 {
		noun = "driver"
	}

	installed := 0
	for _, row := range m.rows {
		if row.State == rowInstalled || row.State == rowAlreadyInstalled {
			installed++
		}
	}

	status := "Installing " + noun
	switch {
	case m.finished && m.status != 0:
		status = fmt.Sprintf("Installed %d of %d %s", installed, len(m.rows), noun)
	case m.finished:
		status = fmt.Sprintf("Installed %d %s", installed, noun)
	}
	return fmt.Sprintf("Resolved %d %s\n%s", len(m.rows), noun, status)
}

// FinalOutputInView reports whether the last frame shows the whole summary.
// In compact mode it only shows the header, so main prints the tree after it.
func (m installModel) FinalOutputInView() bool { return !m.compact }

func (m installModel) View() tea.View {
	if m.jsonOutput {
		return tea.NewView("")
	}

	if m.rows == nil {
		if m.status != 0 {
			return tea.NewView("")
		}
		return tea.NewView(m.spinner.View() + " Resolving drivers...\n")
	}
	// After finishing, this last frame stays on screen as the summary.
	if m.compact {
		return tea.NewView(m.header() + "\n")
	}
	return tea.NewView(m.render() + "\n")
}

var postMsgStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
