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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/columnar-tech/dbc/internal/resolution"
)

type SyncCmd struct {
	Path               string             `arg:"-p" placeholder:"FILE" default:"./dbc.toml" help:"Driver list to sync from"`
	Level              config.ConfigLevel `arg:"-l" help:"Config level to install to (user, system)"`
	NoVerify           bool               `arg:"--no-verify" help:"Allow installation of drivers without a signature file"`
	Json               bool               `arg:"--json" help:"Print output as JSON instead of plaintext"`
	JsonStreamProgress bool               `arg:"--json-stream-progress" help:"Stream progress events as JSON lines (implies --json)"`
}

func (c SyncCmd) newModel(base baseModel) syncModel {
	ctx, cancel := context.WithCancel(context.Background())
	projectLock := &syncProjectLock{}
	// Cancellation requests release, but an active worker owns the lock until
	// it has finished all writes and cleanup. This also covers idle handoffs.
	context.AfterFunc(ctx, projectLock.release)
	return syncModel{
		baseModel:          base,
		Path:               c.Path,
		cfg:                getConfig(c.Level),
		NoVerify:           c.NoVerify,
		jsonOutput:         c.Json || c.JsonStreamProgress,
		jsonStreamProgress: c.JsonStreamProgress,
		ctx:                ctx,
		cancel:             cancel,
		projectLock:        projectLock,
		installPackage:     config.InstallPackage,
	}
}

func (c SyncCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return c.newModel(baseModel)
}

func (c SyncCmd) GetModel() tea.Model {
	return c.newModel(defaultBaseModel())
}

func (syncModel) NeedsRenderer() {}

func (s syncModel) IsJSONMode() bool { return s.jsonOutput }

func (s syncModel) WithJSONWriter(w io.Writer) tea.Model {
	s.jsonOut = w
	return s
}

func (s syncModel) emitJSON(kind string, payload any) {
	out := s.jsonOut
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintln(out, marshalEnvelope(kind, payload))
}

func (s syncModel) FinalOutput() string {
	if s.status != 0 {
		return s.jsonErrorOutput // empty string for non-JSON errors
	}
	if !s.jsonOutput {
		return ""
	}
	installed := s.newlyInstalled
	if installed == nil {
		installed = []jsonschema.SyncedDriver{}
	}
	skipped := s.skippedDrivers
	if skipped == nil {
		skipped = []jsonschema.SyncedDriver{}
	}
	return marshalEnvelope("sync.status", jsonschema.SyncStatus{
		Installed: installed,
		Skipped:   skipped,
		Errors:    []jsonschema.SyncError{},
	})
}

type syncModel struct {
	baseModel

	// path to driver list
	Path     string
	NoVerify bool
	cfg      config.Config

	jsonOutput         bool
	jsonStreamProgress bool

	// the drivers to install or skip, in installation order
	installItems []syncItem
	// the index of the next driver to install in installItems
	index int

	spinner       spinner.Model
	progress      progress.Model
	width, height int

	done bool

	// ctx is canceled when the user quits. Workers retain their temporary files
	// and the project lock until they return; cancellation does not interrupt
	// a blocking call or release a lock while that call may still write.
	ctx            context.Context
	cancel         context.CancelFunc
	projectLock    *syncProjectLock
	installPackage func(context.Context, config.Config, string, *os.File, config.InstallPackageOptions) (config.Manifest, error)

	// jsonErrorOutput is the JSON error envelope to emit via FinalOutput
	jsonErrorOutput string

	// skippedDrivers tracks already-installed drivers for JSON output
	skippedDrivers []jsonschema.SyncedDriver
	// newlyInstalled tracks freshly installed drivers for JSON output
	newlyInstalled []jsonschema.SyncedDriver

	jsonOut io.Writer
}

// syncProjectLock is shared by model copies and workers. A release request
// prevents new work, but defers unlocking until every active worker returns.
type syncProjectLock struct {
	mu      sync.Mutex
	lock    *fslock.Lock
	closed  bool
	workers int
}

func (p *syncProjectLock) begin() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.workers++
	return true
}

func (p *syncProjectLock) end() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.workers--
	p.releaseIfIdle()
}

func (p *syncProjectLock) hold(lock fslock.Lock) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		_ = lock.Release()
		return false
	}
	p.lock = &lock
	return true
}

func (p *syncProjectLock) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.releaseIfIdle()
}

// releaseIfIdle is called with mu held. In particular, cancellation cannot
// release the lock while an uninterruptible install is still publishing.
func (p *syncProjectLock) releaseIfIdle() {
	if p.closed && p.workers == 0 && p.lock != nil {
		_ = p.lock.Release()
		p.lock = nil
	}
}

func (s syncModel) Init() tea.Cmd {
	return func() tea.Msg {
		items, warnings, err := s.prepareWithWarnings()
		return syncPreparedMsg{items: items, warnings: warnings, err: err}
	}
}

type syncPreparedMsg struct {
	items    []syncItem
	warnings []string
	err      error
}

func loadDriverList(path string) (DriversList, error) {
	list, err := openAndDecodeDriverList(path)
	if err != nil {
		return DriversList{}, err
	}
	if len(list.Drivers) == 0 {
		return DriversList{}, fmt.Errorf("no drivers found in driver list `%s`", path)
	}
	return list, nil
}

// syncItem is a driver of the driver list that is ready to be installed or
// that is already installed as locked.
type syncItem struct {
	Name    string
	Package dbc.PkgInfo
	// LibrarySHA256 is the library digest a v1 lock file requires, or empty.
	LibrarySHA256 string
	// Installed is set when the driver is already installed as locked.
	Installed *config.DriverInfo
	// File is the downloaded archive; nil for installed drivers.
	File *os.File
}

// libraryChecksumError reports an archive whose driver library differs from
// the checksum recorded by a v1 lock file.
type libraryChecksumError struct {
	Driver, Expected, Actual string
}

func (e *libraryChecksumError) Error() string {
	return fmt.Sprintf("checksum mismatch for driver %s: the lock file records %s but the package contains %s",
		e.Driver, e.Expected, e.Actual)
}

// archiveSizeMismatchError reports an archive whose bytes do not match the
// optional size recorded in the lockfile.
type archiveSizeMismatchError struct {
	Driver           string
	Expected, Actual int64
}

func (e *archiveSizeMismatchError) Error() string {
	return fmt.Sprintf("driver %s: archive size mismatch: expected %d bytes, got %d", e.Driver, e.Expected, e.Actual)
}

// removeDownload closes a downloaded archive and removes its private directory.
func removeDownload(f *os.File) {
	if f == nil {
		return
	}
	_ = f.Close()
	_ = os.RemoveAll(filepath.Dir(f.Name()))
}

// prepare plans every driver, downloads and verifies what is not installed
// yet and writes the lock file, all while holding the project lock and before
// anything is installed. The model retains the project lock until sync exits.
// Downloads are removed again when preparation fails.
func (s syncModel) prepare() ([]syncItem, error) {
	items, _, err := s.prepareWithWarnings()
	return items, err
}

func (s syncModel) prepareWithWarnings() (items []syncItem, warnings []string, err error) {
	return driverListResolution{
		path:        s.Path,
		ctx:         s.ctx,
		projectLock: s.projectLock,
		base:        s.baseModel,
	}.resolve(s.fetch)
}

// driverListPaths returns the absolute driver list path and the path of its
// lock file for a user-supplied driver list path or directory.
func driverListPaths(path string) (listPath, lockPath string, err error) {
	listPath, err = filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	if filepath.Ext(listPath) == "" {
		listPath = filepath.Join(listPath, "dbc.toml")
	}
	lockPath = strings.TrimSuffix(listPath, filepath.Ext(listPath)) + ".lock"
	return listPath, lockPath, nil
}

// driverListResolution resolves a driver list into its lock file. dbc sync and
// dbc lock share it and differ only in the per-driver step they pass to resolve.
type driverListResolution struct {
	// path is the driver list path or directory as given by the user.
	path string
	ctx  context.Context
	// projectLock holds the project lock for the lifetime of the command.
	projectLock *syncProjectLock
	// base provides the registry and download functions.
	base baseModel
}

// resolve plans every driver, runs fetch for each planned driver and writes
// the lock file once all of them succeeded, all while holding the project lock.
// fetch may record a measured archive digest in the planned lock entry. The
// items returned by fetch are released with removeDownload when resolving
// fails. On success the project lock stays held; callers release it.
func (r driverListResolution) resolve(fetch func(*plannedDriver) (syncItem, error)) (items []syncItem, warnings []string, err error) {
	// Preserve URL stripping for every diagnostic, including failures before
	// the project lock is acquired. Secret values are handled at source boundaries.
	defer func() { err = redactURLSecrets(err, nil) }()
	if !r.projectLock.begin() {
		return nil, warnings, context.Canceled
	}
	defer r.projectLock.end()
	listPath, lockPath, err := driverListPaths(r.path)
	if err != nil {
		return nil, warnings, err
	}

	lock, err := acquireLock(filepath.Join(filepath.Dir(listPath), ".dbc.project.lock"), 10*time.Second)
	if err != nil {
		return nil, warnings, err
	}
	if !r.projectLock.hold(lock) {
		return nil, warnings, context.Canceled
	}
	defer func() {
		if err != nil {
			r.projectLock.release()
			for _, item := range items {
				removeDownload(item.File)
			}
			items = nil
		}
	}()

	list, err := loadDriverList(listPath)
	if err != nil {
		return nil, warnings, err
	}
	existing, err := readLockFile(lockPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, warnings, err
	}
	if err := applyProjectRegistries(list); err != nil {
		var urls []*url.URL
		for _, entry := range list.Registries {
			if u, parseErr := url.Parse(entry.URL); parseErr == nil {
				urls = append(urls, u)
			}
		}
		return nil, warnings, redactRegistryError(err, r.base.getRegistries, urls...)
	}

	planner := syncPlanner{
		target:        config.PlatformTuple(),
		registries:    r.base.getRegistries,
		index:         r.base.getDriverRegistry,
		registryIndex: r.base.getRegistryIndex,
	}
	planned, warnings, err := planner.plan(list, existing)
	if err != nil {
		return nil, warnings, err
	}
	if err := r.ctx.Err(); err != nil {
		return nil, warnings, err
	}

	entries := make([]lockedDriver, 0, len(planned))
	for i := range planned {
		if err := r.ctx.Err(); err != nil {
			return items, warnings, err
		}
		item, err := fetch(&planned[i])
		if err != nil {
			return items, warnings, err
		}
		items = append(items, item)
		entries = append(entries, planned[i].Entry)
	}
	if err := r.ctx.Err(); err != nil {
		return items, warnings, err
	}
	if err := writeLockFileV2(lockPath, lockFileV2{Version: lockFileVersionV2, Drivers: entries}); err != nil {
		return items, warnings, err
	}
	return items, warnings, nil
}

// fetch reuses the installed package of a planned driver when it matches the
// lock, and otherwise downloads and verifies the archive. A digest measured
// for an unpublished hash is recorded in the planned lock entry.
func (s syncModel) fetch(pd *plannedDriver) (syncItem, error) {
	item := syncItem{Name: pd.Name, LibrarySHA256: pd.LibrarySHA256}
	target, err := resolution.TargetFromPlatformTuple(pd.Package.PlatformTuple)
	if err != nil {
		return item, err
	}
	// Matching archive digests identify the same archive bytes; size is checked
	// only when a download is needed.
	if want := pd.Package.ArchiveSHA256; want != "" {
		var signatureVerifier config.PackageVerifier
		if !s.NoVerify {
			signatureVerifier = dbc.VerifyPackageSignature
		}
		info, ok, err := config.CheckInstalledPackage(s.ctx, s.cfg, pd.Name, config.InstalledPackageExpectation{
			Version:           pd.Package.Version,
			ArchiveSHA256:     want,
			LibrarySHA256:     pd.LibrarySHA256,
			SignatureVerifier: signatureVerifier,
		})
		if err != nil {
			return item, fmt.Errorf("failed to check installed driver %s: %w", pd.Name, err)
		}
		if ok {
			item.Package, item.Installed = pd.Package, &info
			return item, nil
		}
	}

	f, err := s.downloadPkg(pd.Package)
	if err != nil {
		return item, fmt.Errorf("failed to download driver: %w", redactRegistryError(err, s.getRegistries, pd.Package.Path))
	}
	if err := checkDownloadedArchive(f, pd, target, nil); err != nil {
		removeDownload(f)
		return item, err
	}
	item.Package, item.File = pd.Package, f
	return item, nil
}

// checkDownloadedArchive checks a downloaded archive against the plan: the
// locked size, the archive hash and the v1 library proof. When the registry
// published no hash, the measured digest is recorded in the planned package
// and lock entry. A non-nil verify runs on the extracted package before any
// digest is recorded. It leaves f positioned at its start and does not close it.
func checkDownloadedArchive(f *os.File, pd *plannedDriver, target resolution.Target, verify config.PackageVerifier) error {
	if pd.ArchiveSize != nil {
		info, err := f.Stat()
		if err != nil {
			return fmt.Errorf("failed to stat downloaded driver %s: %w", pd.Name, err)
		}
		if info.Size() != *pd.ArchiveSize {
			return &archiveSizeMismatchError{Driver: pd.Name, Expected: *pd.ArchiveSize, Actual: info.Size()}
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek downloaded driver archive: %w", err)
	}
	digest, library, err := archiveDigests(f, pd.LibrarySHA256 != "", verify)
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		var sigErr *signatureVerificationError
		if errors.As(err, &sigErr) {
			return fmt.Errorf("failed to verify signature: %w", sigErr.err)
		}
		return fmt.Errorf("failed to inspect downloaded driver %s: %w", pd.Name, err)
	}
	switch want := pd.Package.ArchiveSHA256; {
	case want == "":
		pd.Package.ArchiveSHA256 = digest
		for i := range pd.Entry.Artifacts {
			if a := &pd.Entry.Artifacts[i]; a.Target == target {
				a.Hash = lockHashPrefix + digest
			}
		}
	case want != digest:
		return fmt.Errorf("driver %s: %w", pd.Name, &config.ArchiveHashMismatchError{Expected: want, Actual: digest})
	}
	if pd.LibrarySHA256 != "" && pd.LibrarySHA256 != library {
		return &libraryChecksumError{Driver: pd.Name, Expected: pd.LibrarySHA256, Actual: library}
	}
	return nil
}

// signatureVerificationError marks a failed package signature check so that it
// is not reported as a failure to inspect the archive.
type signatureVerificationError struct{ err error }

func (e *signatureVerificationError) Error() string { return e.err.Error() }
func (e *signatureVerificationError) Unwrap() error { return e.err }

// archiveDigests returns the SHA-256 of the archive and, when withLibrary is
// set, of the driver library inside it. A non-nil verify checks the extracted
// package in the same pass and fails before any digest is returned.
func archiveDigests(f *os.File, withLibrary bool, verify config.PackageVerifier) (archive, library string, err error) {
	if withLibrary || verify != nil {
		var opts []config.InspectOption
		if verify != nil {
			opts = append(opts, config.WithInspectSignatureVerifier(verify))
		}
		info, err := config.InspectPackageArchive(f, opts...)
		return info.ArchiveSHA256, info.LibrarySHA256, err
	}
	archive, err = checksum(f.Name())
	return archive, "", err
}

type installedDrvMsg struct {
	info        config.DriverInfo
	postInstall []string
}

type alreadyInstalledDrvMsg struct {
	info config.DriverInfo
}

func (s syncModel) installDriver(cfg config.Config, item syncItem) tea.Cmd {
	return func() tea.Msg {
		if !s.projectLock.begin() {
			removeDownload(item.File)
			return nil
		}
		if item.Installed != nil {
			defer s.projectLock.end()
			return alreadyInstalledDrvMsg{info: *item.Installed}
		}

		program := prog
		// avoid deadlock by doing this in a goroutine rather than during processing the tea.Msg
		go func() {
			defer s.projectLock.end()
			verificationFailed := false
			options := config.InstallPackageOptions{
				ExpectedArchiveSHA256: item.Package.ArchiveSHA256,
			}
			if item.LibrarySHA256 != "" {
				options.Verifier = func(stagingDir string, manifest config.Manifest) error {
					actual, err := checksum(filepath.Join(stagingDir, manifest.Files.Driver))
					if err != nil {
						return err
					}
					if actual != item.LibrarySHA256 {
						return &libraryChecksumError{Driver: item.Name, Expected: item.LibrarySHA256, Actual: actual}
					}
					return nil
				}
			}
			if !s.NoVerify {
				// VerifyPackageSignature checks the driver library's Columnar signature.
				options.SignatureVerifier = func(stagingDir string, manifest config.Manifest) error {
					err := dbc.VerifyPackageSignature(stagingDir, manifest)
					verificationFailed = err != nil
					return err
				}
			}
			manifest, err := s.installPackage(s.ctx, cfg, item.Name, item.File, options)
			// Remove the archive before reporting: the program may exit as soon
			// as the last result arrives.
			removeDownload(item.File)
			if s.ctx.Err() != nil {
				s.projectLock.release()
				return
			}
			if err != nil {
				s.projectLock.release()
				if verificationFailed {
					program.Send(fmt.Errorf("failed to verify signature: %w", err))
					return
				}
				program.Send(fmt.Errorf("failed to install driver package: %w", err))
				return
			}

			program.Send(installedDrvMsg{
				info:        manifest.DriverInfo,
				postInstall: manifest.PostInstall.Messages,
			})
		}()
		return nil
	}
}

// removeDownloads removes the archives of the drivers after the current one.
// The current driver's archive belongs to its install worker, which removes it
// when it stops.
func (s syncModel) removeDownloads() {
	for _, item := range s.installItems[min(s.index+1, len(s.installItems)):] {
		removeDownload(item.File)
	}
}

func (s syncModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case syncPreparedMsg:
		var next tea.Msg = msg.items
		if msg.err != nil {
			next = msg.err
		}
		model, cmd := s.Update(next)
		if len(msg.warnings) == 0 {
			return model, cmd
		}
		if s.jsonOutput {
			for _, warning := range msg.warnings {
				fmt.Fprintln(os.Stderr, warning)
			}
			return model, cmd
		}
		return model, tea.Sequence(tea.Println(strings.Join(msg.warnings, "\n")), cmd)
	case tea.WindowSizeMsg:
		s.width, s.height = msg.Width, msg.Height
	case spinner.TickMsg:
		var cmd tea.Cmd
		s.spinner, cmd = s.spinner.Update(msg)
		return s, cmd
	case progress.FrameMsg:
		var cmd tea.Cmd
		s.progress, cmd = s.progress.Update(msg)
		return s, cmd
	case tea.KeyPressMsg:
		// Quit at once and tell the worker to stop. Only archives that no
		// install worker can be using are removed here. Cleanup by the worker
		// is best effort: if the process exits while a download is still
		// blocked, its temporary directory is left behind.
		if isQuitKey(msg) {
			s.cancel()
			s.removeDownloads()
		}
	case []syncItem:
		s.spinner = spinner.New()
		s.progress = progress.New(
			progress.WithDefaultBlend(),
			progress.WithWidth(40),
			progress.WithoutPercentage(),
		)
		s.installItems = msg

		if s.jsonStreamProgress {
			for _, item := range msg {
				s.emitJSON("sync.progress", jsonschema.SyncProgressEvent{
					Phase:  "resolving",
					Driver: item.Name,
				})
			}
		}

		return s, tea.Batch(s.installDriver(s.cfg, s.installItems[s.index]), s.spinner.Tick)
	case alreadyInstalledDrvMsg:
		s.skippedDrivers = append(s.skippedDrivers, jsonschema.SyncedDriver{
			Name:    msg.info.ID,
			Version: msg.info.Version.String(),
		})

		if s.jsonStreamProgress {
			s.emitJSON("sync.progress", jsonschema.SyncProgressEvent{
				Phase:   "skipped",
				Driver:  msg.info.ID,
				Version: msg.info.Version.String(),
			})
		}

		if s.index >= len(s.installItems)-1 {
			s.projectLock.release()
			s.done = true
			if s.jsonOutput {
				return s, tea.Quit
			}
			return s, tea.Sequence(
				tea.Printf("%s %s-%s already installed", checkMark, msg.info.ID, msg.info.Version),
				tea.Quit)
		}

		s.index++
		progressCmd := s.progress.SetPercent(float64(s.index) / float64(len(s.installItems)))
		if s.jsonOutput {
			return s, tea.Batch(
				progressCmd,
				s.installDriver(s.cfg, s.installItems[s.index]),
			)
		}
		return s, tea.Batch(
			progressCmd,
			tea.Printf("%s %s-%s already installed", checkMark, msg.info.ID, msg.info.Version),
			s.installDriver(s.cfg, s.installItems[s.index]),
		)
	case installedDrvMsg:
		s.newlyInstalled = append(s.newlyInstalled, jsonschema.SyncedDriver{
			Name:    msg.info.ID,
			Version: msg.info.Version.String(),
		})

		if s.jsonStreamProgress {
			s.emitJSON("sync.progress", jsonschema.SyncProgressEvent{
				Phase:   "installed",
				Driver:  msg.info.ID,
				Version: msg.info.Version.String(),
			})
		}

		var printCmd tea.Cmd
		if !s.jsonOutput {
			printCmd = tea.Printf("%s %s-%s", checkMark, msg.info.ID, msg.info.Version)

			if len(msg.postInstall) > 0 {
				for _, m := range msg.postInstall {
					printCmd = tea.Sequence(
						printCmd,
						tea.Printf("%s   post-install: %s", checkMark, m),
					)
				}
			}
		}

		if s.index >= len(s.installItems)-1 {
			s.projectLock.release()
			s.done = true
			if s.jsonOutput {
				return s, tea.Quit
			}
			return s, tea.Sequence(printCmd, tea.Quit)
		}

		s.index++
		progressCmd := s.progress.SetPercent(float64(s.index) / float64(len(s.installItems)))
		if s.jsonOutput {
			return s, tea.Batch(
				progressCmd,
				s.installDriver(s.cfg, s.installItems[s.index]),
			)
		}
		return s, tea.Batch(
			progressCmd,
			printCmd,
			s.installDriver(s.cfg, s.installItems[s.index]),
		)
	case error:
		msg = redactURLSecrets(msg, nil)
		s.projectLock.release()
		s.removeDownloads()
		s.status = 1
		s.err = msg
		if s.jsonOutput {
			code := "sync_failed"
			var archiveErr *config.ArchiveHashMismatchError
			var libraryErr *libraryChecksumError
			var sizeErr *archiveSizeMismatchError
			if errors.As(msg, &archiveErr) || errors.As(msg, &libraryErr) || errors.As(msg, &sizeErr) {
				code = "checksum_failed"
			}
			s.jsonErrorOutput = marshalEnvelope("error", jsonschema.ErrorResponse{
				Code:    code,
				Message: msg.Error(),
			})
			return s, tea.Quit
		}
	}

	bm, cmd := s.baseModel.Update(msg)
	s.baseModel = bm.(baseModel)

	return s, cmd
}

func (s syncModel) View() tea.View {
	if s.status != 0 {
		return tea.NewView("")
	}

	n := len(s.installItems)
	if n == 0 {
		return tea.NewView("Determining drivers to install...")
	}
	w := lipgloss.Width(fmt.Sprintf("%d", n))

	if s.done {
		return tea.NewView("Done!\n")
	}

	driverCount := fmt.Sprintf(" %*d/%*d", w, s.index, w, n)

	spin := s.spinner.View() + " "
	prog := s.progress.View()
	cellsAvail := max(0, s.width-lipgloss.Width(spin+prog+driverCount))

	driverName := s.installItems[s.index].Name
	info := lipgloss.NewStyle().MaxWidth(cellsAvail).Render("Installing " + driverName)

	cellsRemaining := max(0, s.width-lipgloss.Width(spin+info+prog+driverCount))
	gap := strings.Repeat(" ", max(0, cellsRemaining))

	return tea.NewView(spin + info + gap + prog + driverCount)
}
