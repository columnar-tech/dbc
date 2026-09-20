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

func (c SyncCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return syncModel{
		baseModel:          baseModel,
		Path:               c.Path,
		cfg:                getConfig(c.Level),
		NoVerify:           c.NoVerify,
		jsonOutput:         c.Json || c.JsonStreamProgress,
		jsonStreamProgress: c.JsonStreamProgress,
		worker:             newSyncWorker(),
	}
}

func (c SyncCmd) GetModel() tea.Model {
	return syncModel{
		Path:               c.Path,
		cfg:                getConfig(c.Level),
		NoVerify:           c.NoVerify,
		jsonOutput:         c.Json || c.JsonStreamProgress,
		jsonStreamProgress: c.JsonStreamProgress,
		baseModel:          defaultBaseModel(),
		worker:             newSyncWorker(),
	}
}

func (syncModel) NeedsRenderer() {}

func (s syncModel) IsJSONMode() bool { return s.jsonOutput }

// FilterProgramMessage keeps Bubble Tea quit and interrupt messages from
// stopping sync while its worker owns the project lock or prepared archives.
// Other commands do not implement this optional interface and retain their
// existing quit behavior.
func (s syncModel) FilterProgramMessage(msg tea.Msg) tea.Msg {
	if s.terminal || s.worker == nil {
		return msg
	}
	cancel := false
	switch msg.(type) {
	case tea.QuitMsg, tea.InterruptMsg:
		cancel = true
	case tea.KeyPressMsg:
		key := msg.(tea.KeyPressMsg).String()
		cancel = key == "ctrl+c" || key == "ctrl+d" || key == "esc"
	}
	if cancel {
		s.worker.cancel()
		return nil
	}
	return msg
}

// ProgramExited is called by the command runner after Bubble Tea exits through
// a path not handled by FilterProgramMessage. It abandons UI events and joins
// the worker after cancellation and cleanup.
func (s syncModel) ProgramExited() {
	if s.worker != nil {
		s.worker.abandonProgram()
	}
}

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
	if s.status != 0 || !s.jsonOutput {
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
	Path         string
	NoVerify     bool
	LockFilePath string
	// information to write the new lockfile
	locked             LockFile
	writeCandidateLock func(string, LockFile) error
	cfg                config.Config

	jsonOutput         bool
	jsonStreamProgress bool

	// the list of drivers in the driver list
	list DriversList
	// cdn driver registry index
	driverIndex []dbc.Driver
	// the list of package+version to install
	installItems []installItem
	// the index of the next driver to install in installItems
	index int

	spinner       spinner.Model
	progress      progress.Model
	width, height int

	done           bool
	registryErrors error // Store registry errors for better error messages

	// skippedDrivers tracks already-installed drivers for JSON output
	skippedDrivers []jsonschema.SyncedDriver
	// newlyInstalled tracks freshly installed drivers for JSON output
	newlyInstalled []jsonschema.SyncedDriver

	jsonOut  io.Writer
	worker   *syncWorker
	terminal bool
}

type syncWorkerHooks struct {
	duringPrepare       func(context.Context, int, installItem) error
	beforeCandidateSave func(context.Context) error
	installPackage      func(context.Context, config.Config, string, *os.File, config.ExpectedPackageMetadata, config.InstallOptions) (config.Manifest, error)
}

type syncWorker struct {
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	events    chan tea.Msg
	abandon   chan struct{}
	stateMu   sync.Mutex
	started   bool
	abandoned bool
	finish    sync.Once
	hooks     syncWorkerHooks
}

func newSyncWorker() *syncWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &syncWorker{
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
		events: make(chan tea.Msg, 1), abandon: make(chan struct{}),
	}
}

type syncWorkerResultMsg struct {
	err  error
	code string
	lock LockFile
}

type syncResolvingMsg struct{ driver string }

type syncItemsMsg struct{ items []installItem }

// driversListMsg is retained for focused lock-resolution tests. The production
// sync path reads the driver list inside the project-lock-owning worker.
type driversListMsg struct {
	path string
	list DriversList
}

func (s syncModel) Init() tea.Cmd {
	return func() tea.Msg {
		if s.worker == nil {
			s.worker = newSyncWorker()
		}
		return s.worker.start(s)
	}
}

func (w *syncWorker) start(s syncModel) tea.Msg {
	w.stateMu.Lock()
	if w.abandoned {
		w.stateMu.Unlock()
		return nil
	}
	if !w.started {
		w.started = true
		go w.run(s)
	}
	w.stateMu.Unlock()
	return w.receive()
}

func (w *syncWorker) hasStarted() bool {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	return w.started
}

func (w *syncWorker) nextEvent() tea.Cmd {
	return func() tea.Msg { return w.receive() }
}

func (w *syncWorker) send(ctx context.Context, msg tea.Msg) bool {
	select {
	case w.events <- msg:
		return true
	case <-ctx.Done():
		return false
	case <-w.abandon:
		return false
	}
}

func (w *syncWorker) receive() tea.Msg {
	select {
	case <-w.abandon:
		return nil
	default:
	}
	select {
	case msg := <-w.events:
		return msg
	case <-w.abandon:
		return nil
	}
}

func (w *syncWorker) run(s syncModel) {
	defer w.finish.Do(func() { close(w.done) })
	defer w.cancel()
	result := s.runSyncWorker(w)
	select {
	case w.events <- result:
	case <-w.abandon:
	}
}

func (w *syncWorker) abandonProgram() {
	w.stateMu.Lock()
	if !w.abandoned {
		w.abandoned = true
		w.cancel()
		close(w.abandon)
	}
	started := w.started
	if !started {
		w.finish.Do(func() { close(w.done) })
	}
	w.stateMu.Unlock()
	if started {
		<-w.done
	}
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

func reloadConfigTarget(selected config.Config) config.Config {
	refreshed, ok := config.Get()[selected.Level]
	if !ok {
		return selected
	}
	// Keep the exact target resolved when the sync model was constructed while
	// refreshing its runtime manifest state after acquiring the project lock.
	refreshed.Level = selected.Level
	refreshed.Location = selected.Location
	return refreshed
}

type installItem struct {
	Driver               dbc.Driver
	Package              dbc.PkgInfo
	Checksum             string
	ArchiveHash          string
	ArchiveSize          int64
	InstalledLibraryHash string
	LockEntry            *lockInfo
	Archive              *os.File
	Expected             config.ExpectedPackageMetadata
	AlreadyInstalled     *config.DriverInfo
	RemovedDriver        *config.DriverInfo
}

type preparedSyncMsg struct {
	items []installItem
	lock  LockFile
}

type candidateLockSavedMsg struct{ lock LockFile }

func (s syncModel) createInstallList(list DriversList) ([]installItem, error) {
	// Load the lock file if it exists
	lf, err := loadLockFile(s.LockFilePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// construct our list of driver+version to install
	var items []installItem
	for name, spec := range list.Drivers {
		var info lockInfo
		if lf.lockinfo != nil {
			info = lf.lockinfo[name]
		}
		if lf.Version == lockFileVersion && info.Version != nil {
			if info.Source.Type != "registry" {
				return nil, fmt.Errorf("locked source type %q for driver %q is not supported by sync yet; source integration will follow", info.Source.Type, name)
			}
			if lockVersionSatisfiesSpec(info, spec) {
				artifact, err := selectLockedArtifact(info, config.PlatformTuple(), false)
				if err == nil {
					item, err := installItemFromLockedArtifact(name, info, artifact)
					if err != nil {
						return nil, err
					}
					items = append(items, item)
					continue
				}
				var refreshErr *LockRefreshRequiredError
				if !errors.As(err, &refreshErr) {
					return nil, err
				}
			}
		}

		// locate the driver info in the CDN driver registry index
		drv, err := findDriver(name, s.driverIndex)
		if err != nil {
			return nil, wrapWithRegistryContext(err, s.registryErrors)
		}

		var pkg dbc.PkgInfo
		// if the lockfile specified a version and either the driver list doesn't
		// specify a version constraint or the version in the locked file is valid
		// for that constraint, then we want to install the version in the lockfile
		if info.Version != nil && (spec.Version == nil || spec.Version.Check(info.Version)) {
			// install the locked version and verify checksum
			pkg, err = drv.GetPackage(info.Version, config.PlatformTuple(), spec.Prerelease == "allow")
		} else {
			// no locked version or driver list version doesn't match locked file
			if spec.Version != nil {
				if spec.Prerelease == "allow" {
					spec.Version.IncludePrerelease = true
				}
				pkg, err = drv.GetWithConstraint(spec.Version, config.PlatformTuple())
			} else {
				pkg, err = drv.GetPackage(nil, config.PlatformTuple(), spec.Prerelease == "allow")
			}
		}

		if err != nil {
			return nil, err
		}

		items = append(items, installItem{
			Driver:   drv,
			Package:  pkg,
			Checksum: info.legacyChecksumFor(config.PlatformTuple()),
			LockEntry: func() *lockInfo {
				if info.Version == nil {
					return nil
				}
				copy := info
				return &copy
			}(),
		})
	}
	return items, nil
}

func (s syncModel) registryDiscoveryNeeded(list DriversList) (bool, error) {
	lf, err := loadLockFile(s.LockFilePath)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for name, spec := range list.Drivers {
		entry, ok := lf.lockinfo[name]
		if lf.Version != lockFileVersion || !ok || entry.Version == nil {
			return true, nil
		}
		// Non-registry source adapters are outside this sync slice. Their error
		// is reported by createInstallList without contacting the registry.
		if entry.Source.Type != "registry" {
			continue
		}
		if !lockVersionSatisfiesSpec(entry, spec) {
			return true, nil
		}
		if _, err := selectLockedArtifact(entry, config.PlatformTuple(), false); err != nil {
			var refreshErr *LockRefreshRequiredError
			if errors.As(err, &refreshErr) {
				return true, nil
			}
			return false, err
		}
	}
	return false, nil
}

func lockVersionSatisfiesSpec(entry lockInfo, spec driverSpec) bool {
	if entry.Version == nil {
		return false
	}
	if spec.Version != nil {
		// An explicit constraint can name a prerelease directly. Let semver's
		// constraint evaluation decide whether that locked version is allowed.
		return spec.Version.Check(entry.Version)
	}
	return entry.Version.Prerelease() == "" || spec.Prerelease == "allow"
}

func installItemFromLockedArtifact(name string, entry lockInfo, artifact lockArtifact) (installItem, error) {
	if artifact.Location.Kind != resolution.ArtifactLocationURL || artifact.Location.Value == "" {
		return installItem{}, fmt.Errorf("locked registry artifact for %s is not a remote URL artifact", name)
	}
	if artifact.Format != "" && artifact.Format != "tar.gz" {
		return installItem{}, fmt.Errorf("locked artifact format %q for %s is not supported by sync yet", artifact.Format, name)
	}
	packageURL, err := url.Parse(artifact.Location.Value)
	if err != nil || !packageURL.IsAbs() || packageURL.Hostname() == "" {
		return installItem{}, fmt.Errorf("invalid locked artifact URL for %s: %q", name, artifact.Location.Value)
	}
	registryURL, err := url.Parse(entry.Source.URL)
	if err != nil || !registryURL.IsAbs() || registryURL.Hostname() == "" {
		return installItem{}, fmt.Errorf("invalid locked registry identity for %s: %q", name, entry.Source.URL)
	}
	driver := dbc.Driver{Path: name, Title: name, Registry: &dbc.Registry{BaseURL: registryURL}}
	pkg := dbc.PkgInfo{
		Driver:        driver,
		Version:       entry.Version,
		PlatformTuple: config.PlatformTuple(),
		Path:          packageURL,
		ArtifactHash:  artifact.Hash,
		ArtifactSize:  cloneInt64(artifact.Size),
	}
	copy := entry
	return installItem{
		Driver:    driver,
		Package:   pkg,
		Checksum:  entry.legacyChecksumFor(config.PlatformTuple()),
		LockEntry: &copy,
	}, nil
}

type installedDrvMsg struct {
	removed     *config.DriverInfo
	info        config.DriverInfo
	postInstall []string
	item        installItem
}

type alreadyInstalledDrvMsg struct {
	info config.DriverInfo
	item installItem
}

func (s syncModel) installPreparedPackage(ctx context.Context, item installItem) (installedDrvMsg, error) {
	if err := ctx.Err(); err != nil {
		return installedDrvMsg{}, err
	}
	if item.Archive == nil {
		return installedDrvMsg{}, errors.New("prepared package archive is missing")
	}
	if _, err := item.Archive.Seek(0, io.SeekStart); err != nil {
		return installedDrvMsg{}, fmt.Errorf("failed to rewind prepared driver archive: %w", err)
	}
	var verify func(string, config.Manifest) error
	if !s.NoVerify {
		verify = func(stagingDir string, manifest config.Manifest) error {
			return dbc.VerifyPackageSignature(stagingDir, manifest)
		}
	}
	install := s.worker.hooks.installPackage
	if install == nil {
		// config.InstallPackage is contextless. Keep this call synchronous so
		// cancellation cannot release the project lock or archive prematurely.
		install = func(_ context.Context, cfg config.Config, driver string, archive *os.File, expected config.ExpectedPackageMetadata, options config.InstallOptions) (config.Manifest, error) {
			return config.InstallPackage(cfg, driver, archive, expected, options)
		}
	}
	manifest, err := install(ctx, s.cfg, item.Driver.Path, item.Archive, item.Expected, config.InstallOptions{Verify: verify})
	if err := ctx.Err(); err != nil {
		return installedDrvMsg{}, err
	}
	if err != nil {
		if isPackageVerificationFailure(err) {
			return installedDrvMsg{}, fmt.Errorf("failed to verify signature: %w", packageVerificationError(err))
		}
		return installedDrvMsg{}, fmt.Errorf("failed to install driver: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return installedDrvMsg{}, err
	}
	checksumHash, err := checksum(manifest.DriverInfo.Driver.Shared.Get(config.PlatformTuple()))
	if err != nil {
		return installedDrvMsg{}, syncChecksumError{err: err}
	}
	if item.InstalledLibraryHash != "" && item.InstalledLibraryHash != checksumHash {
		return installedDrvMsg{}, syncChecksumError{err: errors.New("installed library checksum does not match validated package")}
	}
	return installedDrvMsg{
		removed:     item.RemovedDriver,
		info:        manifest.DriverInfo,
		postInstall: manifest.PostInstall.Messages,
		item:        item,
	}, nil
}

func (s syncModel) writeLockFile() error {
	s.locked.Version = lockFileVersion
	return s.persistCandidateLock(s.locked)
}

func (s syncModel) persistCandidateLock(lock LockFile) error {
	lock.Version = lockFileVersion
	if s.writeCandidateLock != nil {
		return s.writeCandidateLock(s.LockFilePath, lock)
	}
	return writeLockFileAtomic(s.LockFilePath, lock)
}

func canReuseLockedEntry(item installItem) bool {
	if item.LockEntry == nil || item.LockEntry.Version == nil || item.Package.Version == nil ||
		!item.LockEntry.Version.Equal(item.Package.Version) {
		return false
	}
	source, err := packageLockSource(item)
	if err != nil || item.LockEntry.Source != source {
		return false
	}
	artifact, err := selectLockedArtifact(*item.LockEntry, config.PlatformTuple(), false)
	if err != nil || artifact.Location.Kind != resolution.ArtifactLocationURL || artifact.Location.Value == "" || item.Package.Path == nil || item.Package.ArtifactSize == nil {
		return false
	}
	lockedURL, err := url.Parse(artifact.Location.Value)
	if err != nil || lockedURL == nil {
		return false
	}
	return item.Package.Path.String() == lockedURL.String() &&
		item.Package.ArtifactHash == artifact.Hash && *item.Package.ArtifactSize == *artifact.Size
}

func packageLockSource(item installItem) (lockSource, error) {
	if item.Driver.Registry == nil || item.Driver.Registry.BaseURL == nil {
		return lockSource{}, fmt.Errorf("driver %q has no registry identity", item.Driver.Path)
	}
	return lockSource{Type: "registry", URL: item.Driver.Registry.BaseURL.String()}, nil
}

func snapshotDownloadedArchive(item *installItem, archive *os.File) error {
	if archive == nil {
		return fmt.Errorf("download returned no archive")
	}
	info, err := archive.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat downloaded archive: %w", err)
	}
	actualSize := info.Size()
	actualHash, err := checksumFile(archive, archive.Name())
	if err != nil {
		return err
	}
	actualHash = "sha256:" + actualHash
	if err := resolution.ValidateArtifactMetadata(actualHash, &actualSize); err != nil {
		return err
	}
	if item.Package.ArtifactHash != "" && item.Package.ArtifactHash != actualHash {
		return fmt.Errorf("downloaded archive hash %s does not match expected hash %s", actualHash, item.Package.ArtifactHash)
	}
	if item.Package.ArtifactSize != nil && *item.Package.ArtifactSize != actualSize {
		return fmt.Errorf("downloaded archive size %d does not match expected size %d", actualSize, *item.Package.ArtifactSize)
	}
	item.ArchiveHash = actualHash
	item.ArchiveSize = actualSize
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to rewind downloaded archive: %w", err)
	}
	return nil
}

func (s syncModel) prepareInstallItems(ctx context.Context, items []installItem) (preparedSyncMsg, error) {
	prepared := preparedSyncMsg{items: items, lock: LockFile{Version: lockFileVersion}}
	for i := range prepared.items {
		if err := ctx.Err(); err != nil {
			return prepared, err
		}
		item := &prepared.items[i]
		if s.cfg.Exists {
			if installed, ok := s.cfg.Drivers[item.Driver.Path]; ok {
				if item.Package.Version.Equal(installed.Version) {
					libraryHash, err := checksum(installed.Driver.Shared.Get(config.PlatformTuple()))
					if err != nil {
						return prepared, fmt.Errorf("failed to compute checksum: %w", err)
					}
					if item.Checksum != "" && libraryHash != item.Checksum {
						return prepared, fmt.Errorf("checksum mismatch for driver %s: %s != %s", item.Driver.Path, libraryHash, item.Checksum)
					}
					if item.Checksum == "" {
						item.Checksum = libraryHash
					}
					item.InstalledLibraryHash = libraryHash
					installedCopy := installed
					item.AlreadyInstalled = &installedCopy
				} else {
					installedCopy := installed
					item.RemovedDriver = &installedCopy
				}
			}
		}

		needsArchive := item.AlreadyInstalled == nil || !canReuseLockedEntry(*item)
		if needsArchive {
			// Determine whether the registry supplied source metadata before
			// measured values are copied into Package below.
			expected, hasMetadata, err := expectedRegistryPackageMetadata(item.Package)
			if err != nil {
				return prepared, err
			}
			// downloadPkg is contextless; wait for it to return while retaining
			// the project lock, then observe cancellation and clean up its archive.
			archive, err := s.downloadPkg(item.Package)
			if archive != nil {
				item.Archive = archive
			}
			if ctx.Err() != nil {
				return prepared, ctx.Err()
			}
			if err != nil {
				return prepared, fmt.Errorf("failed to download driver: %w", err)
			}
			if err := snapshotDownloadedArchive(item, archive); err != nil {
				return prepared, fmt.Errorf("failed to snapshot downloaded driver archive: %w", err)
			}
			if !hasMetadata {
				if _, err := archive.Seek(0, io.SeekStart); err != nil {
					return prepared, fmt.Errorf("failed to rewind downloaded driver archive: %w", err)
				}
				packageManifest, inspectErr := config.InspectPackageMetadata(archive)
				if inspectErr != nil {
					return prepared, inspectErr
				}
				if packageManifest.PackageVersion == 2 {
					return prepared, errors.New("registry package v2 requires archive hash and size metadata")
				}
			}
			item.Package.ArtifactHash = item.ArchiveHash
			item.Package.ArtifactSize = cloneInt64(&item.ArchiveSize)
			expected.ArchiveHash = item.ArchiveHash
			expected.ArchiveSize = item.ArchiveSize
			item.Expected = expected
			if _, err := archive.Seek(0, io.SeekStart); err != nil {
				return prepared, fmt.Errorf("failed to rewind downloaded driver archive: %w", err)
			}
			var verify func(string, config.Manifest) error
			if !s.NoVerify {
				verify = func(stagingDir string, manifest config.Manifest) error {
					return dbc.VerifyPackageSignature(stagingDir, manifest)
				}
			}
			validation, err := config.ValidatePackage(item.Driver.Path, archive, expected, config.InstallOptions{Verify: verify})
			if err != nil {
				if isPackageVerificationFailure(err) {
					return prepared, fmt.Errorf("failed to verify signature: %w", packageVerificationError(err))
				}
				return prepared, fmt.Errorf("failed to validate driver package: %w", err)
			}
			if item.AlreadyInstalled == nil {
				item.InstalledLibraryHash = strings.TrimPrefix(validation.VerifiedLibraryHash, "sha256:")
			}
		} else {
			// The exact locked artifact is already installed, so it can be reused
			// without another download or package validation.
			expected, _, err := expectedRegistryPackageMetadata(item.Package)
			if err != nil {
				return prepared, err
			}
			item.Expected = expected
		}

		entry, err := lockEntryForItem(*item)
		if err != nil {
			return prepared, fmt.Errorf("failed to update lock entry: %w", err)
		}
		prepared.lock.Drivers = append(prepared.lock.Drivers, entry)
		if s.worker != nil && s.worker.hooks.duringPrepare != nil {
			if err := s.worker.hooks.duringPrepare(ctx, i, *item); err != nil {
				return prepared, err
			}
		}
	}
	return prepared, nil
}

type syncChecksumError struct{ err error }

func (e syncChecksumError) Error() string { return e.err.Error() }
func (e syncChecksumError) Unwrap() error { return e.err }

func acquireSyncProjectLock(ctx context.Context, lockPath string) (fslock.Lock, error) {
	lock, err := fslock.AcquireContext(ctx, lockPath)
	if err == nil {
		return lock, nil
	}
	if errors.Is(err, fslock.ErrLockContended) {
		return fslock.Lock{}, fmt.Errorf("another dbc operation is in progress: %w", err)
	}
	if errors.Is(err, os.ErrPermission) {
		return fslock.Lock{}, fmt.Errorf(
			"cannot write to %s: permission denied.\nThis command requires elevated privileges; try %s.",
			filepath.Dir(lockPath), elevationHint())
	}
	return fslock.Lock{}, fmt.Errorf("could not acquire lock in %s: %w", filepath.Dir(lockPath), err)
}

func (s syncModel) runSyncWorker(worker *syncWorker) syncWorkerResultMsg {
	result := syncWorkerResultMsg{code: "sync_failed"}
	fail := func(code string, err error) syncWorkerResultMsg {
		result.code = code
		result.err = err
		return result
	}
	ctx := worker.ctx
	path, err := filepath.Abs(s.Path)
	if err != nil {
		return fail("sync_failed", err)
	}
	if filepath.Ext(path) == "" {
		path = filepath.Join(path, "dbc.toml")
	}
	lockPath := filepath.Join(filepath.Dir(path), ".dbc.project.lock")
	lockCtx, cancelLockWait := context.WithTimeout(ctx, 10*time.Second)
	projectLock, err := acquireSyncProjectLock(lockCtx, lockPath)
	cancelLockWait()
	if err != nil {
		if ctx.Err() != nil {
			return fail("sync_failed", ctx.Err())
		}
		return fail("sync_failed", err)
	}
	defer projectLock.Release()
	if err := ctx.Err(); err != nil {
		return fail("sync_failed", err)
	}
	s.cfg = reloadConfigTarget(s.cfg)

	list, err := loadDriverList(path)
	if err != nil {
		return fail("sync_failed", err)
	}
	s.Path = path
	s.LockFilePath = strings.TrimSuffix(path, filepath.Ext(path)) + ".lock"
	s.list = list
	if err := applyProjectRegistries(s.list); err != nil {
		return fail("sync_failed", err)
	}
	if err := ctx.Err(); err != nil {
		return fail("sync_failed", err)
	}

	needsRegistry, err := s.registryDiscoveryNeeded(s.list)
	if err != nil {
		return fail("sync_failed", fmt.Errorf("failed to inspect lock file: %w", err))
	}
	if needsRegistry {
		// Registry clients are currently contextless. The project lock remains
		// held until this call returns, after which cancellation is observed.
		drivers, registryErr := s.getDriverRegistry()
		if err := ctx.Err(); err != nil {
			return fail("sync_failed", err)
		}
		s.registryErrors = registryErr
		if len(drivers) == 0 && registryErr != nil {
			return fail("sync_failed", fmt.Errorf("error getting driver list: %w", registryErr))
		}
		s.driverIndex = drivers
	}

	items, err := s.createInstallList(s.list)
	if err != nil {
		return fail("sync_failed", err)
	}
	if err := ctx.Err(); err != nil {
		return fail("sync_failed", err)
	}
	if !worker.send(ctx, syncItemsMsg{items: items}) {
		return fail("sync_failed", ctx.Err())
	}
	for _, item := range items {
		if !worker.send(ctx, syncResolvingMsg{driver: item.Driver.Path}) {
			return fail("sync_failed", ctx.Err())
		}
	}

	prepared, err := s.prepareInstallItems(ctx, items)
	defer closePreparedArchives(prepared.items)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fail("sync_failed", err)
		}
		return fail("sync_failed", err)
	}
	if err := ctx.Err(); err != nil {
		return fail("sync_failed", err)
	}
	if worker.hooks.beforeCandidateSave != nil {
		if err := worker.hooks.beforeCandidateSave(ctx); err != nil {
			return fail("sync_failed", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return fail("sync_failed", err)
	}
	if err := s.persistCandidateLock(prepared.lock); err != nil {
		return fail("sync_failed", fmt.Errorf("failed to write candidate lock file: %w", err))
	}
	result.lock = prepared.lock

	for _, item := range prepared.items {
		if err := ctx.Err(); err != nil {
			return fail("sync_failed", err)
		}
		if item.AlreadyInstalled != nil {
			progress := alreadyInstalledDrvMsg{info: *item.AlreadyInstalled, item: item}
			progress.item.Archive = nil
			if !worker.send(ctx, progress) {
				return fail("sync_failed", ctx.Err())
			}
			continue
		}
		installed, err := s.installPreparedPackage(ctx, item)
		if err != nil {
			var checksumErr syncChecksumError
			if errors.As(err, &checksumErr) {
				return fail("checksum_failed", checksumErr)
			}
			return fail("sync_failed", err)
		}
		installed.item.Archive = nil
		if !worker.send(ctx, installed) {
			return fail("sync_failed", ctx.Err())
		}
	}
	if err := ctx.Err(); err != nil {
		return fail("sync_failed", err)
	}
	result.err = nil
	result.code = ""
	return result
}

func closePreparedArchives(items []installItem) {
	for i := range items {
		if items[i].Archive != nil {
			_ = items[i].Archive.Close()
			items[i].Archive = nil
		}
	}
}

func syncProgressPercent(index, total int) float64 {
	if total <= 0 {
		return 0
	}
	completed := min(max(index+1, 0), total)
	return float64(completed) / float64(total)
}

func (s syncModel) fail(code string, err error) (syncModel, tea.Cmd) {
	s.status = 1
	s.err = err
	s.terminal = true
	if s.jsonOutput {
		s.emitJSON("error", jsonschema.ErrorResponse{
			Code:    code,
			Message: err.Error(),
		})
		return s, tea.Quit
	}
	return s, tea.Quit
}

func lockEntryForItem(item installItem) (lockInfo, error) {
	if item.LockEntry != nil && item.LockEntry.Legacy != nil && samePlatformTarget(item.LockEntry.Legacy.Platform, config.PlatformTuple()) {
		if err := verifyLegacyLibraryProof(*item.LockEntry, config.PlatformTuple(), item.InstalledLibraryHash); err != nil {
			return lockInfo{}, err
		}
	}
	if canReuseLockedEntry(item) {
		return *item.LockEntry, nil
	}
	if item.Package.Version == nil || item.Package.Path == nil {
		return lockInfo{}, fmt.Errorf("driver %q has incomplete resolved package metadata", item.Driver.Path)
	}
	source, err := packageLockSource(item)
	if err != nil {
		return lockInfo{}, err
	}
	target, err := resolution.TargetFromPlatformTuple(item.Package.PlatformTuple)
	if err != nil {
		return lockInfo{}, fmt.Errorf("invalid resolved package target %q: %w", item.Package.PlatformTuple, err)
	}
	hash := item.ArchiveHash
	if hash == "" {
		hash = item.Package.ArtifactHash
	}
	size := item.ArchiveSize
	if item.ArchiveHash == "" && item.Package.ArtifactSize != nil {
		size = *item.Package.ArtifactSize
	}
	release := resolution.ResolvedRelease{
		DriverID: item.Driver.Path,
		Version:  item.Package.Version.String(),
		Source:   resolution.SourceSpec{Type: source.Type, Reference: source.URL},
		Artifacts: []resolution.Artifact{{
			Target:   target,
			Format:   "tar.gz",
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: item.Package.Path.String()},
			Hash:     hash,
			Size:     &size,
		}},
	}
	candidate, err := lockInfoFromResolvedRelease(item.Driver.Path, release)
	if err != nil {
		return lockInfo{}, err
	}
	if item.LockEntry == nil || item.LockEntry.Version == nil || !item.LockEntry.Version.Equal(item.Package.Version) {
		return candidate, nil
	}
	if item.LockEntry.Version == nil {
		return candidate, nil
	}
	if len(item.LockEntry.Artifacts) == 0 {
		if item.LockEntry.Version == nil {
			return candidate, nil
		}
		if item.LockEntry.Legacy != nil {
			var verified *VerifiedLegacyLibrary
			if samePlatformTarget(item.LockEntry.Legacy.Platform, config.PlatformTuple()) {
				verified = &VerifiedLegacyLibrary{Platform: config.PlatformTuple(), LibraryHash: item.InstalledLibraryHash}
			}
			return migrateV1Entry(*item.LockEntry, release, config.PlatformTuple(), verified)
		}
		return candidate, nil
	}
	return refreshLockEntry(*item.LockEntry, candidate)
}

func (s syncModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
	case driversListMsg:
		// This adapter exists for focused lock-resolution tests. Runtime syncs
		// keep this entire sequence in runSyncWorker under the project lock.
		s.Path = msg.path
		s.LockFilePath = strings.TrimSuffix(s.Path, filepath.Ext(s.Path)) + ".lock"
		s.list = msg.list
		if err := applyProjectRegistries(s.list); err != nil {
			return s, errCmd("%v", err)
		}
		needsRegistry, err := s.registryDiscoveryNeeded(s.list)
		if err != nil {
			return s, errCmd("failed to inspect lock file: %w", err)
		}
		if !needsRegistry {
			return s, func() tea.Msg {
				returnItems, err := s.createInstallList(s.list)
				if err != nil {
					return err
				}
				return returnItems
			}
		}
		return s, func() tea.Msg {
			drivers, err := s.getDriverRegistry()
			return driversWithRegistryError{drivers: drivers, err: err}
		}
	case syncItemsMsg:
		s.spinner = spinner.New()
		s.progress = progress.New(
			progress.WithDefaultBlend(),
			progress.WithWidth(40),
			progress.WithoutPercentage(),
		)
		s.installItems = msg.items
		return s, tea.Batch(s.worker.nextEvent(), s.spinner.Tick)
	case syncResolvingMsg:
		if s.jsonStreamProgress {
			s.emitJSON("sync.progress", jsonschema.SyncProgressEvent{
				Phase:  "resolving",
				Driver: msg.driver,
			})
		}
		return s, s.worker.nextEvent()
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
		percent := syncProgressPercent(s.index, len(s.installItems))
		if s.index < len(s.installItems)-1 {
			s.index++
		}
		progressCmd := s.progress.SetPercent(percent)
		if s.jsonOutput {
			return s, tea.Batch(progressCmd, s.worker.nextEvent())
		}
		return s, tea.Batch(
			progressCmd,
			tea.Sequence(tea.Printf("%s %s-%s already installed", checkMark, msg.info.ID, msg.info.Version), s.worker.nextEvent()),
		)
	case installedDrvMsg:
		// Production workers verify the post-install checksum before publishing
		// this progress event. Retain the direct-update path for isolated model
		// tests and embedders that inject the event without a worker.
		if s.worker == nil {
			chksum, err := checksum(msg.info.Driver.Shared.Get(config.PlatformTuple()))
			if err != nil {
				return s.fail("checksum_failed", err)
			}
			if msg.item.InstalledLibraryHash != "" && msg.item.InstalledLibraryHash != chksum {
				return s.fail("checksum_failed", errors.New("installed library checksum does not match validated package"))
			}
		}
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
			if msg.removed != nil {
				printCmd = tea.Sequence(
					printCmd,
					tea.Printf("%s   removed %s-%s", checkMark, msg.removed.ID, msg.removed.Version),
				)
			}

			if len(msg.postInstall) > 0 {
				for _, m := range msg.postInstall {
					printCmd = tea.Sequence(
						printCmd,
						tea.Printf("%s   post-install: %s", checkMark, m),
					)
				}
			}
		}

		percent := syncProgressPercent(s.index, len(s.installItems))
		if s.index < len(s.installItems)-1 {
			s.index++
		}
		progressCmd := s.progress.SetPercent(percent)
		if s.jsonOutput {
			return s, tea.Batch(
				progressCmd,
				s.worker.nextEvent(),
			)
		}
		return s, tea.Batch(
			progressCmd,
			tea.Sequence(printCmd, s.worker.nextEvent()),
		)
	case syncWorkerResultMsg:
		if msg.err != nil {
			return s.fail(msg.code, msg.err)
		}
		s.locked = msg.lock
		s.done = true
		s.terminal = true
		return s, tea.Quit
	case error:
		return s.fail("sync_failed", msg)
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

	driverIndex := min(s.index, len(s.installItems)-1)
	driverName := s.installItems[driverIndex].Driver.Path
	info := lipgloss.NewStyle().MaxWidth(cellsAvail).Render("Installing " + driverName)

	cellsRemaining := max(0, s.width-lipgloss.Width(spin+info+prog+driverCount))
	gap := strings.Repeat(" ", max(0, cellsRemaining))

	return tea.NewView(spin + info + gap + prog + driverCount)
}
