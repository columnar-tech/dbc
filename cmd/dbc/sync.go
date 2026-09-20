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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
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
	}
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
	locked LockFile
	cfg    config.Config

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

	jsonOut io.Writer
}

type driversListMsg struct {
	path string
	list DriversList
}

func (s syncModel) Init() tea.Cmd {
	return func() tea.Msg {
		p, err := filepath.Abs(s.Path)
		if err != nil {
			return err
		}

		if filepath.Ext(p) == "" {
			p = filepath.Join(p, "dbc.toml")
		}

		lockPath := filepath.Join(filepath.Dir(p), ".dbc.project.lock")
		lock, err := acquireLock(lockPath, 10*time.Second)
		if err != nil {
			return err
		}
		defer lock.Release()

		drivers, err := loadDriverList(p)
		if err != nil {
			return err
		}
		return driversListMsg{
			path: p,
			list: drivers,
		}
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

type installItem struct {
	Driver               dbc.Driver
	Package              dbc.PkgInfo
	Checksum             string
	ArchiveHash          string
	ArchiveSize          int64
	InstalledLibraryHash string
	LockEntry            *lockInfo
}

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
	if artifact.Path != "" || artifact.URL == "" {
		return installItem{}, fmt.Errorf("locked registry artifact for %s is not a remote URL artifact", name)
	}
	if artifact.Format != "" && artifact.Format != "tar.gz" {
		return installItem{}, fmt.Errorf("locked artifact format %q for %s is not supported by sync yet", artifact.Format, name)
	}
	packageURL, err := url.Parse(artifact.URL)
	if err != nil || !packageURL.IsAbs() || packageURL.Hostname() == "" {
		return installItem{}, fmt.Errorf("invalid locked artifact URL for %s: %q", name, artifact.URL)
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

func (s syncModel) installDriver(cfg config.Config, item installItem) tea.Cmd {
	return func() tea.Msg {
		var removedDriver *config.DriverInfo
		if cfg.Exists {
			// is driver installed already?
			if drv, ok := cfg.Drivers[item.Driver.Path]; ok {
				if item.Package.Version.Equal(drv.Version) {
					chksum, err := checksum(drv.Driver.Shared.Get(config.PlatformTuple()))
					if err != nil {
						return fmt.Errorf("failed to compute checksum: %w", err)
					}

					if item.Checksum != "" {
						// A lockfile checksum proves only the installed library.
						if chksum != item.Checksum {
							return fmt.Errorf("checksum mismatch for driver %s: %s != %s",
								item.Driver.Path, chksum, item.Checksum)
						}
					} else {
						item.Checksum = chksum
					}
					item.InstalledLibraryHash = chksum

					if !canReuseLockedEntry(item) {
						if err := ensureArchiveSnapshot(s, &item); err != nil {
							return fmt.Errorf("failed to snapshot driver archive: %w", err)
						}
					}

					return alreadyInstalledDrvMsg{info: drv, item: item}
				} else {
					removedDriver = &drv
				}
			}
		}

		// avoid deadlock by doing this in a goroutine rather than during processing the tea.Msg
		go func() {
			output, err := s.downloadPkg(item.Package)
			if err != nil {
				prog.Send(fmt.Errorf("failed to download driver: %w", err))
				return
			}
			defer output.Close()
			if err := snapshotDownloadedArchive(&item, output); err != nil {
				prog.Send(fmt.Errorf("failed to snapshot downloaded driver archive: %w", err))
				return
			}

			expected, hasMetadata, err := expectedRegistryPackageMetadata(item.Package)
			if err != nil {
				prog.Send(err)
				return
			}
			if !hasMetadata {
				packageManifest, inspectErr := config.InspectPackageMetadata(output)
				if inspectErr != nil {
					prog.Send(inspectErr)
					return
				}
				if packageManifest.PackageVersion == 2 {
					prog.Send(errors.New("registry package v2 requires archive hash and size metadata"))
					return
				}
			}
			var verify func(string, config.Manifest) error
			if !s.NoVerify {
				verify = func(stagingDir string, manifest config.Manifest) error {
					return dbc.VerifyPackageSignature(stagingDir, manifest)
				}
			}
			manifest, err := config.InstallPackage(cfg, item.Driver.Path, output, expected, config.InstallOptions{
				Verify: verify,
			})
			if err != nil {
				if isPackageVerificationFailure(err) {
					prog.Send(fmt.Errorf("failed to verify signature: %w", packageVerificationError(err)))
				} else {
					prog.Send(fmt.Errorf("failed to install driver: %w", err))
				}
				return
			}

			prog.Send(installedDrvMsg{
				removed:     removedDriver,
				info:        manifest.DriverInfo,
				postInstall: manifest.PostInstall.Messages,
				item:        item,
			})
		}()
		return nil
	}
}

func (s syncModel) writeLockFile() error {
	s.locked.Version = lockFileVersion
	return writeLockFileAtomic(s.LockFilePath, s.locked)
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
	if err != nil || artifact.URL == "" || item.Package.Path == nil || item.Package.ArtifactSize == nil {
		return false
	}
	return item.Package.Path.String() == artifact.URL &&
		item.Package.ArtifactHash == artifact.Hash && *item.Package.ArtifactSize == *artifact.Size
}

func packageLockSource(item installItem) (lockSource, error) {
	if item.Driver.Registry == nil || item.Driver.Registry.BaseURL == nil {
		return lockSource{}, fmt.Errorf("driver %q has no registry identity", item.Driver.Path)
	}
	return lockSource{Type: "registry", URL: item.Driver.Registry.BaseURL.String()}, nil
}

func ensureArchiveSnapshot(s syncModel, item *installItem) error {
	if item.Package.ArtifactHash != "" && item.Package.ArtifactSize != nil {
		if err := resolution.ValidateArtifactMetadata(item.Package.ArtifactHash, item.Package.ArtifactSize); err != nil {
			return err
		}
		item.ArchiveHash = item.Package.ArtifactHash
		item.ArchiveSize = *item.Package.ArtifactSize
		return nil
	}
	output, err := s.downloadPkg(item.Package)
	if err != nil {
		return fmt.Errorf("failed to download archive for lock metadata: %w", err)
	}
	defer output.Close()
	return snapshotDownloadedArchive(item, output)
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
			Target: target,
			Format: "tar.gz",
			URL:    item.Package.Path.String(),
			Hash:   hash,
			Size:   &size,
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
				items, err := s.createInstallList(s.list)
				if err != nil {
					return err
				}
				return items
			}
		}
		return s, func() tea.Msg {
			drivers, err := s.getDriverRegistry()
			// Return both drivers and error - we'll decide how to handle based on whether
			// all requested drivers can be found
			return driversWithRegistryError{
				drivers: drivers,
				err:     err,
			}
		}
	case driversWithRegistryError:
		s.registryErrors = msg.err
		// If we have no drivers and there's an error, fail immediately
		if len(msg.drivers) == 0 && msg.err != nil {
			return s, errCmd("error getting driver list: %w", msg.err)
		}
		s.driverIndex = msg.drivers
		return s, func() tea.Msg {
			items, err := s.createInstallList(s.list)
			if err != nil {
				return err
			}
			return items
		}
	case []dbc.Driver:
		// For backwards compatibility, still handle plain driver list
		s.driverIndex = msg
		return s, func() tea.Msg {
			items, err := s.createInstallList(s.list)
			if err != nil {
				return err
			}
			return items
		}
	case []installItem:
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
					Driver: item.Driver.Path,
				})
			}
		}

		return s, tea.Batch(s.installDriver(s.cfg, s.installItems[s.index]), s.spinner.Tick)
	case alreadyInstalledDrvMsg:
		entry, err := lockEntryForItem(msg.item)
		if err != nil {
			return s, errCmd("failed to update lock entry: %w", err)
		}
		s.locked.Drivers = append(s.locked.Drivers, entry)
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
			s.done = true
			if s.jsonOutput {
				return s, tea.Sequence(
					func() tea.Msg { return s.writeLockFile() },
					tea.Quit)
			}
			return s, tea.Sequence(
				tea.Printf("%s %s-%s already installed", checkMark, msg.info.ID, msg.info.Version),
				func() tea.Msg { return s.writeLockFile() },
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
		chksum, err := checksum(msg.info.Driver.Shared.Get(config.PlatformTuple()))
		if err != nil {
			s.status = 1
			if s.jsonOutput {
				return s, tea.Sequence(tea.Println(marshalEnvelope("error", jsonschema.ErrorResponse{
					Code:    "checksum_failed",
					Message: err.Error(),
				})), tea.Quit)
			}
			return s, tea.Sequence(tea.Println("Error: ", err), tea.Quit)
		}
		msg.item.InstalledLibraryHash = chksum
		entry, err := lockEntryForItem(msg.item)
		if err != nil {
			s.status = 1
			return s, tea.Sequence(tea.Println("Error: ", err), tea.Quit)
		}
		s.locked.Drivers = append(s.locked.Drivers, entry)
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

		if s.index >= len(s.installItems)-1 {
			s.done = true
			if s.jsonOutput {
				return s, tea.Sequence(
					func() tea.Msg { return s.writeLockFile() },
					tea.Quit)
			}
			return s, tea.Sequence(
				printCmd,
				func() tea.Msg { return s.writeLockFile() },
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
			printCmd,
			s.installDriver(s.cfg, s.installItems[s.index]),
		)
	case error:
		s.status = 1
		s.err = msg
		if s.jsonOutput {
			return s, tea.Sequence(tea.Println(marshalEnvelope("error", jsonschema.ErrorResponse{
				Code:    "sync_failed",
				Message: msg.Error(),
			})), tea.Quit)
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

	driverName := s.installItems[s.index].Driver.Path
	info := lipgloss.NewStyle().MaxWidth(cellsAvail).Render("Installing " + driverName)

	cellsRemaining := max(0, s.width-lipgloss.Width(spin+info+prog+driverCount))
	gap := strings.Repeat(" ", max(0, cellsRemaining))

	return tea.NewView(spin + info + gap + prog + driverCount)
}
