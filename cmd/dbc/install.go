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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
)

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
	Driver             string             `arg:"positional,required" help:"Driver to install, a version constraint (mysql>=1,<2), a local .tar.gz/.tgz package, or a Packslip source (github.com/owner/repo=1.2.3)"`
	Level              config.ConfigLevel `arg:"-l" help:"Config level to install to (user, system)"`
	Json               bool               `arg:"--json" help:"Print output as JSON instead of plaintext"`
	JsonStreamProgress bool               `arg:"--json-stream-progress" help:"Stream progress events as JSON lines (implies --json)"`
	NoVerify           bool               `arg:"--no-verify" help:"Allow installation of drivers without a signature file"`
	Pre                bool               `arg:"--pre" help:"Allow implicit installation of pre-release versions"`
	InsecureNoChecksum bool               `arg:"--insecure-no-checksum" help:"Skip sha256 checksum recording (not recommended)"`
}

func (InstallCmd) Description() string {
	return "Install a driver.\n\n" +
		"`DRIVER` may be a registry driver with a version constraint (for example `dbc install mysql`, `dbc install \"mysql=0.1.0\"`, or `dbc install \"mysql>=1,<2\"`), a local `.tar.gz`/`.tgz` package, or an exact Packslip project release such as `dbc install github.com/owner/repo=1.2.3`.\n" +
		"See https://docs.columnar.tech/dbc/guides/installing/#version-constraints for more on version constraint syntax."
}

func (c InstallCmd) GetModelCustom(baseModel baseModel) tea.Model {
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	isLocal := (strings.HasSuffix(c.Driver, ".tar.gz") || strings.HasSuffix(c.Driver, ".tgz")) && !strings.Contains(c.Driver, "://")
	localPackagePath := ""
	if isLocal {
		localPackagePath = c.Driver
	}
	var packslipProject, packslipVersion string
	var isPackslip bool
	var packslipErr error
	if !isLocal {
		packslipProject, packslipVersion, isPackslip, packslipErr = parsePackslipInstallArgument(c.Driver)
	}
	return progressiveInstallModel{
		Driver:             c.Driver,
		NoVerify:           c.NoVerify,
		jsonOutput:         c.Json || c.JsonStreamProgress,
		jsonStreamProgress: c.JsonStreamProgress,
		Pre:                c.Pre,
		insecureNoChecksum: c.InsecureNoChecksum,
		spinner:            s,
		cfg:                getConfig(c.Level),
		baseModel:          baseModel,
		isLocal:            isLocal,
		localPackagePath:   localPackagePath,
		isPackslip:         isPackslip,
		packslipProject:    packslipProject,
		packslipVersion:    packslipVersion,
		directInputErr:     packslipErr,
		p: NewFileProgress(
			progress.WithDefaultBlend(),
			progress.WithWidth(20),
			progress.WithoutPercentage(),
		),
	}
}

func (c InstallCmd) GetModel() tea.Model {
	return c.GetModelCustom(defaultBaseModel())
}

type directInstallResolvedMsg struct{ item installItem }

type directInstallPreparedMsg struct {
	item     installItem
	executor *packageExecutor
}

type directInstallFinishedMsg struct {
	item   installItem
	result config.EnsurePackageResult
	err    error
}

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

func (progressiveInstallModel) NeedsRenderer() {}

func (m progressiveInstallModel) IsJSONMode() bool { return m.jsonOutput }

func (m progressiveInstallModel) WithJSONWriter(w io.Writer) tea.Model {
	m.jsonOut = w
	return m
}

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

type progressiveInstallModel struct {
	baseModel

	Driver             string
	VersionInput       *semver.Version
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
	isPackslip       bool
	packslipProject  string
	packslipVersion  string
	directInputErr   error
	installSource    *jsonschema.InstallSource

	registryErrors           error
	alreadyInstalledChecksum string
	jsonOut                  io.Writer
	jsonErrorOutput          string // JSON error envelope to emit via FinalOutput
}

type driversWithRegistryError struct {
	drivers []dbc.Driver
	err     error
}

func (m progressiveInstallModel) Init() tea.Cmd {
	if m.directInputErr != nil {
		return errCmd("%w", m.directInputErr)
	}
	if m.isPackslip && m.Pre {
		return errCmd("--pre does not apply to exact Packslip releases")
	}
	if m.isLocal || m.isPackslip {
		return tea.Batch(m.spinner.Tick, func() tea.Msg {
			item, err := m.resolveDirectInstall(context.Background())
			if err != nil {
				return err
			}
			return directInstallResolvedMsg{item: item}
		})
	}

	return tea.Batch(m.spinner.Tick, func() tea.Msg {
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
		return driversWithRegistryError{
			drivers: drivers,
			err:     err,
		}
	})
}

func (m progressiveInstallModel) startDirectInstall(item installItem) (tea.Model, tea.Cmd) {
	m.Driver = item.Release.DriverID
	version, err := semver.NewVersion(item.Release.Version)
	if err != nil {
		return m, errCmd("invalid resolved driver version %q: %w", item.Release.Version, err)
	}
	m.DriverPackage = dbc.PkgInfo{Driver: dbc.Driver{Path: m.Driver, Title: m.Driver}, Version: version, PlatformTuple: item.Platform}
	m.installSource = &jsonschema.InstallSource{Type: item.Release.Source.Type, Reference: item.Release.Source.Reference}
	if m.isLocal {
		m.state = stInstalling
		m = m.addEvent("download.complete")
		m = m.addEvent("extract.start")
	} else {
		m.state = stDownloading
		m = m.addEvent("download.start")
	}
	return m, func() tea.Msg {
		executor, err := m.newDirectInstallExecutor(item)
		if err != nil {
			return err
		}
		if err := executor.prepareItem(context.Background(), &item); err != nil {
			if closeErr := closeDirectInstallArchive(&item); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close prepared package archive: %w", closeErr))
			}
			return err
		}
		return directInstallPreparedMsg{item: item, executor: executor}
	}
}

func (m progressiveInstallModel) startDirectEnsure(item installItem, executor *packageExecutor) (tea.Model, tea.Cmd) {
	if !m.isLocal {
		m = m.addEvent("download.complete")
		m = m.addEvent("extract.start")
	}
	m.state = stInstalling
	return m, func() tea.Msg {
		installCfg, err := installConfigForEnsure(executor.cfg)
		if err != nil {
			if closeErr := closeDirectInstallArchive(&item); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close prepared package archive: %w", closeErr))
			}
			return directInstallFinishedMsg{item: item, err: err}
		}
		executor.cfg = installCfg
		result, err := executor.ensurePreparedPackage(context.Background(), &item)
		if closeErr := closeDirectInstallArchive(&item); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close prepared package archive: %w", closeErr))
		}
		return directInstallFinishedMsg{item: item, result: result, err: err}
	}
}

func (m progressiveInstallModel) Preamble() string {
	if m.isLocal {
		return "Installing from local package: " + m.localPackagePath + "\n\n"
	}
	return ""
}

func (m progressiveInstallModel) hasConflict() bool {
	return m.conflictingInfo.ID != "" && m.conflictingInfo.Version != nil
}

func (m progressiveInstallModel) isAlreadyInstalled() bool {
	return m.conflictingInfo.ID != "" && m.conflictingInfo.Version != nil &&
		m.conflictingInfo.Version.Equal(m.DriverPackage.Version)
}

func (m progressiveInstallModel) FinalOutput() string {
	if m.status != 0 {
		return m.jsonErrorOutput // empty string for non-JSON errors; structured envelope for JSON mode
	}
	if m.isAlreadyInstalled() {
		if m.jsonOutput {
			payload := jsonschema.InstallStatus{
				Status:   "already installed",
				Driver:   m.conflictingInfo.ID,
				Version:  m.conflictingInfo.Version.String(),
				Location: filepath.SplitList(m.cfg.Location)[0],
				Source:   m.installSource,
			}
			if m.alreadyInstalledChecksum != "" {
				payload.Checksum = m.alreadyInstalledChecksum
			}
			payloadBytes, err := json.Marshal(payload)
			if err != nil {
				return fmt.Sprintf(`{"schema_version":1,"kind":"error","payload":{"code":"marshal_error","message":"%s"}}`, err.Error())
			}
			env := jsonschema.Envelope{
				SchemaVersion: jsonschema.SchemaVersion,
				Kind:          "install.status",
				Payload:       json.RawMessage(payloadBytes),
			}
			jsonOutput, err := json.Marshal(env)
			if err != nil {
				return fmt.Sprintf(`{"schema_version":1,"kind":"error","payload":{"code":"marshal_error","message":"%s"}}`, err.Error())
			}
			return string(jsonOutput)
		}
		return fmt.Sprintf("\nDriver %s %s already installed at %s",
			m.conflictingInfo.ID, m.conflictingInfo.Version, filepath.SplitList(m.cfg.Location)[0])
	}

	var b strings.Builder
	if m.state == stDone {
		installStatus := jsonschema.InstallStatus{
			Status:   "installed",
			Driver:   m.Driver,
			Version:  m.DriverPackage.Version.String(),
			Location: filepath.SplitList(m.cfg.Location)[0],
			Source:   m.installSource,
		}
		if m.hasConflict() {
			installStatus.Conflict = fmt.Sprintf("%s (version: %s)", m.conflictingInfo.ID, m.conflictingInfo.Version)
		}

		if m.postInstallMessage != "" {
			installStatus.Message = m.postInstallMessage
		}

		if !m.insecureNoChecksum && m.installedDriverInfo.Driver.Shared.Get(config.PlatformTuple()) != "" {
			driverPath := m.installedDriverInfo.Driver.Shared.Get(config.PlatformTuple())
			chksum, err := checksum(driverPath)
			if err != nil && m.jsonOutput {
				return marshalEnvelope("error", jsonschema.ErrorResponse{
					Code:    "checksum_failed",
					Message: err.Error(),
				})
			}
			if err == nil {
				installStatus.Checksum = chksum
			}
		}

		if m.jsonOutput {
			if installStatus.Checksum != "" {
				m.addEvent("verify.checksum.ok", func(e *jsonschema.InstallProgressEvent) {
					e.Checksum = installStatus.Checksum
				})
			}
			if m.jsonStreamProgress {
				m.emitJSON("install.progress", jsonschema.InstallProgressEvent{
					Event:  "install.complete",
					Driver: m.Driver,
				})
			}
			return marshalEnvelope("install.status", installStatus)
		}

		if installStatus.Conflict != "" {
			fmt.Fprintf(&b, "\nRemoved conflicting driver: %s", installStatus.Conflict)
		}

		fmt.Fprintf(&b, "\nInstalled %s %s to %s",
			installStatus.Driver, installStatus.Version, installStatus.Location)

		if installStatus.Message != "" {
			b.WriteString("\n\n" + postMsgStyle.Render(installStatus.Message))
		}
	}
	return b.String()
}

func (m progressiveInstallModel) searchForDriver(list []dbc.Driver) (tea.Model, tea.Cmd) {
	driverName, vers, err := parseDriverConstraint(m.Driver)
	if err != nil {
		return m, errCmd("%w", err)
	}

	m.Driver = driverName
	d, err := findDriver(m.Driver, list)
	if err != nil {
		// If we have registry errors, enhance the error message
		if m.registryErrors != nil {
			return m, errCmd("could not find driver: %w\n\nNote: Some driver registries were unavailable:\n%s", err, m.registryErrors.Error())
		}
		return m, errCmd("could not find driver: %w", err)
	}

	return m, func() tea.Msg {
		if vers != nil {
			vers.IncludePrerelease = m.Pre
			pkg, err := d.GetWithConstraint(vers, config.PlatformTuple())
			if err != nil {
				return err
			}
			return pkg
		}

		pkg, err := d.GetPackage(nil, config.PlatformTuple(), m.Pre)
		if err != nil {
			if !m.Pre && !d.HasNonPrerelease() {
				for _, cfg := range config.Get() {
					if di, ok := cfg.Drivers[driverName]; ok && di.Version != nil && di.Version.Prerelease() != "" {
						return fmt.Errorf("driver `%s` is already installed (version %s); only pre-release versions are available for this driver; to update, use: dbc install --pre %s", driverName, di.Version, driverName)
					}
				}
			}
			return err
		}

		return pkg
	}
}

func packageVerificationError(err error) error {
	const prefix = "package verification failed: "
	if strings.HasPrefix(err.Error(), prefix) {
		return errors.New(strings.TrimPrefix(err.Error(), prefix))
	}
	return err
}

func isPackageVerificationFailure(err error) bool {
	return strings.HasPrefix(err.Error(), "package verification failed: ")
}

func (m progressiveInstallModel) fail(err error) (tea.Model, tea.Cmd) {
	m.status = 1
	m.err = err
	if m.jsonOutput {
		m.jsonErrorOutput = marshalEnvelope("error", jsonschema.ErrorResponse{
			Code:    "install_failed",
			Message: err.Error(),
		})
		return m, tea.Quit
	}
	return m, tea.Quit
}

func (m progressiveInstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case alreadyInstalledChecksumMsg:
		m.alreadyInstalledChecksum = string(msg)
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
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
	case driversWithRegistryError:
		m.registryErrors = msg.err
		return m.searchForDriver(msg.drivers)
	case []dbc.Driver:
		// For backwards compatibility, still handle plain driver list
		return m.searchForDriver(msg)
	case directInstallResolvedMsg:
		return m.startDirectInstall(msg.item)
	case directInstallPreparedMsg:
		return m.startDirectEnsure(msg.item, msg.executor)
	case directInstallFinishedMsg:
		if msg.err != nil {
			return m.fail(msg.err)
		}
		if msg.result.Installed == nil {
			return m.fail(errors.New("package ensure returned no installed driver registration"))
		}
		m.Driver = msg.item.Release.DriverID
		version, err := semver.NewVersion(msg.item.Release.Version)
		if err != nil {
			return m.fail(fmt.Errorf("invalid resolved driver version %q: %w", msg.item.Release.Version, err))
		}
		m.DriverPackage = dbc.PkgInfo{Driver: dbc.Driver{Path: m.Driver, Title: m.Driver}, Version: version, PlatformTuple: msg.item.Platform}
		m.installSource = &jsonschema.InstallSource{Type: msg.item.Release.Source.Type, Reference: msg.item.Release.Source.Reference}
		m.installedDriverInfo = *msg.result.Installed
		if msg.result.Skipped {
			m.conflictingInfo = *msg.result.Installed
			m.state = stDone
			if m.jsonOutput && !m.insecureNoChecksum && msg.result.Installed.Driver.Shared.Get(msg.item.Platform) != "" {
				libraryPath := msg.result.Installed.Driver.Shared.Get(msg.item.Platform)
				return m, func() tea.Msg {
					checksumValue, checksumErr := checksum(libraryPath)
					if checksumErr != nil {
						return fmt.Errorf("checksum_failed: %w", checksumErr)
					}
					return alreadyInstalledChecksumMsg(checksumValue)
				}
			}
			return m, tea.Quit
		}
		if msg.result.Manifest == nil {
			return m.fail(errors.New("package ensure returned no install manifest"))
		}
		m.conflictingInfo = config.DriverInfo{}
		if msg.result.Previous != nil {
			if msg.result.Previous.Version == nil || !msg.result.Previous.Version.Equal(version) {
				m.conflictingInfo = *msg.result.Previous
			}
		}
		m.postInstallMessage = strings.Join(msg.result.Manifest.PostInstall.Messages, "\n")
		m.state = stDone
		m = m.addEvent("extract.complete")
		m = m.addEvent("verify.start")
		m = m.addEvent("verify.complete")
		m = m.addEvent("manifest.create")
		return m, tea.Quit
	case dbc.PkgInfo:
		release, err := resolvedReleaseFromRegistryPackage(msg.Driver, msg)
		if err == nil {
			var artifactIndex int
			artifactIndex, err = artifactIndexForPlatform(release, msg.PlatformTuple)
			if err == nil {
				var item installItem
				item, err = newInstallItem(release, artifactIndex, msg.PlatformTuple, nil)
				if err == nil {
					return m.startDirectInstall(item)
				}
			}
		}
		return m.fail(err)
	case error:
		return m.fail(msg)
	}

	base, cmd := m.baseModel.Update(msg)
	m.baseModel = base.(baseModel)
	return m, cmd
}

func checkbox(label string, checked bool) string {
	if checked {
		return fmt.Sprintf("[%s] %s", checkMark, label)
	}
	return fmt.Sprintf("[ ] %s", label)
}

var postMsgStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))

func (m progressiveInstallModel) View() tea.View {
	if m.status != 0 || m.jsonOutput {
		return tea.NewView("")
	}

	if m.isAlreadyInstalled() {
		return tea.NewView("")
	}

	var b strings.Builder
	for s := range stDone {
		if m.isLocal && (s == stSearching || s == stDownloading) {
			continue
		}

		if s == m.state {
			fmt.Fprintf(&b, "[%s] %s...", m.spinner.View(), s.String())
			if s == stDownloading {
				b.WriteString(" " + m.p.View())
			}
		} else {
			if s == stVerifying && s < m.state && m.NoVerify {
				fmt.Fprintf(&b, "[%s] %s", skipMark, s.String())
			} else {
				b.WriteString(checkbox(s.String(), s < m.state))
			}
		}
		b.WriteByte('\n')
	}

	return tea.NewView(b.String())
}
