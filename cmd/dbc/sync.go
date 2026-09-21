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
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/columnar-tech/dbc/internal/sourceidentity"
	"github.com/columnar-tech/dbc/internal/sourceresolution"
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
	ensurePackage       func(context.Context, config.Config, string, config.ExpectedPackageMetadata, config.InstallOptions, config.EnsurePackageCallbacks) (config.EnsurePackageResult, error)
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
	Release              resolution.ResolvedRelease
	ArtifactIndex        int
	Platform             string
	InstalledLibraryHash string
	ValidatedLibraryHash string
	LockEntry            *lockInfo
	Archive              *sourceresolution.OpenedArtifact
	Expected             config.ExpectedPackageMetadata
	Validation           *config.PackageValidation
	AlreadyInstalled     *config.DriverInfo
}

type plannedSyncItem struct {
	Name        string
	Requirement sourceresolution.Requirement
	Plan        sourceresolution.Plan
	LockEntry   *lockInfo
	LegacyLock  *lockInfo
}

func (item *installItem) selectedArtifact() (*resolution.Artifact, error) {
	if item == nil || item.ArtifactIndex < 0 || item.ArtifactIndex >= len(item.Release.Artifacts) {
		return nil, errors.New("install item has no selected artifact")
	}
	return &item.Release.Artifacts[item.ArtifactIndex], nil
}

func newInstallItem(release resolution.ResolvedRelease, artifactIndex int, platform string, lockEntry *lockInfo) (installItem, error) {
	item := installItem{Release: cloneResolvedReleaseForSync(release), ArtifactIndex: artifactIndex, Platform: platform}
	if artifactIndex < 0 || artifactIndex >= len(release.Artifacts) {
		return installItem{}, errors.New("resolved release has no selected artifact")
	}
	if release.DriverID == "" || release.Version == "" || release.Source.Type == "" || release.Source.Reference == "" {
		return installItem{}, errors.New("resolved release is missing driver, version, or source identity")
	}
	if platform == "" {
		return installItem{}, errors.New("install item is missing its target platform")
	}
	target, err := resolution.TargetFromPlatformTuple(platform)
	if err != nil {
		return installItem{}, fmt.Errorf("invalid install platform %q: %w", platform, err)
	}
	if release.Artifacts[artifactIndex].Target != target {
		return installItem{}, fmt.Errorf("selected artifact target %v does not match platform %q", release.Artifacts[artifactIndex].Target, platform)
	}
	if lockEntry != nil {
		copy := cloneLockInfo(*lockEntry)
		item.LockEntry = &copy
	}
	return item, nil
}

func cloneResolvedReleaseForSync(release resolution.ResolvedRelease) resolution.ResolvedRelease {
	clone := release
	clone.Evidence = append([]resolution.Evidence(nil), release.Evidence...)
	clone.Artifacts = make([]resolution.Artifact, len(release.Artifacts))
	for i, artifact := range release.Artifacts {
		clone.Artifacts[i] = artifact
		clone.Artifacts[i].Size = cloneInt64(artifact.Size)
		clone.Artifacts[i].HostRequirements = cloneHostRequirements(artifact.HostRequirements)
	}
	return clone
}

type preparedSyncMsg struct {
	items []installItem
	lock  LockFile
}

type candidateLockSavedMsg struct{ lock LockFile }

func (s syncModel) planSyncItems(list DriversList) ([]plannedSyncItem, bool, error) {
	lf, err := loadLockFile(s.LockFilePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}

	planned := make([]plannedSyncItem, 0, len(list.Drivers))
	needsRegistry := false
	for name, spec := range list.Drivers {
		requirement, err := requirementForDriverSpec(name, spec)
		if err != nil {
			return nil, false, err
		}

		entry, hasEntry := lf.lockinfo[name]
		var plan sourceresolution.Plan
		var existing *lockInfo
		var legacy *lockInfo
		if lf.Version == lockFileVersion && hasEntry {
			release := entry.resolvedRelease()
			plan = requirement.Plan(&release, false)
			if plan.Outcome() == sourceresolution.PlanReplay || plan.Outcome() == sourceresolution.PlanRefreshRequired {
				copy := cloneLockInfo(entry)
				existing = &copy
			}
		} else {
			plan = requirement.Plan(nil, false)
			// v1 locks prove only a version and (optionally) installed library
			// bytes. They are migration inputs, never replayable release snapshots.
			// Without source identity, retain that proof only for the historical
			// default-registry selection and verify the selected result later.
			if lf.Version == lockFileVersionV1 && hasEntry &&
				requirement.Source().Mode() == sourceresolution.DefaultRegistry {
				copy := cloneLockInfo(entry)
				legacy = &copy
			}
		}

		switch plan.Outcome() {
		case sourceresolution.PlanReplay:
		case sourceresolution.PlanResolve, sourceresolution.PlanRefreshRequired:
			needsRegistry = true
		case sourceresolution.PlanLockedArtifactMissing:
			return nil, false, &LockedModeArtifactMissingError{DriverID: name, Platform: config.PlatformTuple()}
		case sourceresolution.PlanReject:
			return nil, false, fmt.Errorf("cannot plan sync for driver %q: %w", name, plan.Err())
		default:
			return nil, false, fmt.Errorf("cannot plan sync for driver %q: invalid plan outcome %d", name, plan.Outcome())
		}
		planned = append(planned, plannedSyncItem{
			Name: name, Requirement: requirement, Plan: plan,
			LockEntry: existing, LegacyLock: legacy,
		})
	}
	return planned, needsRegistry, nil
}

func requirementForDriverSpec(name string, spec driverSpec) (sourceresolution.Requirement, error) {
	var selection sourceresolution.SourceSelection
	if spec.Source == nil {
		selection = sourceresolution.DefaultRegistrySelection()
	} else {
		if spec.Source.Type != dbc.DriverSourceRegistry {
			return sourceresolution.Requirement{}, fmt.Errorf("source type %q for driver %q is not supported by sync yet; source integration will follow", spec.Source.Type, name)
		}
		key, err := driverSourceIdentity(spec.Source)
		if err != nil {
			return sourceresolution.Requirement{}, fmt.Errorf("driver %q has invalid declared registry source: %w", name, err)
		}
		selection, err = sourceresolution.ExplicitSourceSelection(key)
		if err != nil {
			return sourceresolution.Requirement{}, fmt.Errorf("driver %q has invalid declared registry source: %w", name, err)
		}
	}

	constraint := ""
	if spec.Version != nil {
		constraint = spec.Version.String()
	}
	policy := sourceresolution.PrereleaseForbidden
	if spec.Prerelease == "allow" || (spec.Version != nil && spec.Version.IncludePrerelease) {
		policy = sourceresolution.PrereleaseAllowed
	}
	version, err := sourceresolution.RegistryVersionRequirement(constraint, policy)
	if err != nil {
		return sourceresolution.Requirement{}, fmt.Errorf("driver %q has invalid version requirement: %w", name, err)
	}
	target, err := resolution.TargetFromPlatformTuple(config.PlatformTuple())
	if err != nil {
		return sourceresolution.Requirement{}, fmt.Errorf("unsupported sync platform: %w", err)
	}
	return sourceresolution.NewRequirement(name, selection, version, target)
}

func (s syncModel) createInstallList(planned []plannedSyncItem) ([]installItem, error) {
	items := make([]installItem, 0, len(planned))
	for _, entry := range planned {
		switch entry.Plan.Outcome() {
		case sourceresolution.PlanReplay:
			item, err := installItemFromPlan(entry)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		case sourceresolution.PlanResolve, sourceresolution.PlanRefreshRequired:
			item, err := s.resolveRegistryPlan(entry)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		case sourceresolution.PlanReject:
			return nil, fmt.Errorf("cannot plan sync for driver %q: %w", entry.Name, entry.Plan.Err())
		case sourceresolution.PlanLockedArtifactMissing:
			return nil, &LockedModeArtifactMissingError{DriverID: entry.Name, Platform: config.PlatformTuple()}
		default:
			return nil, fmt.Errorf("cannot resolve sync plan for driver %q: invalid outcome %d", entry.Name, entry.Plan.Outcome())
		}
	}
	return items, nil
}

func installItemFromPlan(planned plannedSyncItem) (installItem, error) {
	release, ok := planned.Plan.Release()
	if !ok {
		return installItem{}, fmt.Errorf("replay plan for %q has no release", planned.Name)
	}
	selected, ok := planned.Plan.Artifact()
	if !ok {
		return installItem{}, fmt.Errorf("replay plan for %q has no selected artifact", planned.Name)
	}
	for i := range release.Artifacts {
		if release.Artifacts[i].Target == selected.Target {
			return newInstallItem(release, i, config.PlatformTuple(), planned.LockEntry)
		}
	}
	return installItem{}, fmt.Errorf("replay plan for %q selected an artifact outside its release", planned.Name)
}

func (s syncModel) resolveRegistryPlan(planned plannedSyncItem) (installItem, error) {
	allowPrerelease := false
	if policy, ok := planned.Requirement.Version().PrereleasePolicy(); ok {
		allowPrerelease = policy == sourceresolution.PrereleaseAllowed
	}
	var exactVersion string
	var priorLock *lockInfo
	refreshingSnapshot := false
	var refreshSource *sourceidentity.Key
	var expectedRefreshSource *sourceidentity.Key
	if planned.Plan.Outcome() == sourceresolution.PlanRefreshRequired {
		release, ok := planned.Plan.Release()
		if !ok {
			return installItem{}, fmt.Errorf("refresh plan for %q has no prior release", planned.Name)
		}
		key, err := sourceidentity.Parse(sourceidentity.Kind(release.Source.Type), release.Source.Reference)
		if err != nil || key.Kind != sourceidentity.Registry {
			return installItem{}, fmt.Errorf("refresh plan for %q has an invalid registry source identity", planned.Name)
		}
		keyCopy := key
		expectedRefreshSource = &keyCopy
		if planned.Requirement.Source().Mode() == sourceresolution.DefaultRegistry {
			// A default-registry requirement normally respects configured registry
			// order. Refreshing a locked release is different: complete that same
			// source snapshot instead of selecting a different registry.
			refreshSource = &key
		}
		exactVersion = release.Version
		refreshingSnapshot = true
		priorLock = planned.LockEntry
	} else if planned.LegacyLock != nil && planned.LegacyLock.Version != nil &&
		planned.Requirement.Source().Mode() == sourceresolution.DefaultRegistry &&
		planned.Requirement.AcceptsVersion(planned.LegacyLock.Version.String()) {
		// A v1 lock has no source identity, so its version may guide registry
		// selection only under the historical default-registry policy.
		exactVersion = planned.LegacyLock.Version.String()
	}
	driver, err := registryDriverForRequirement(planned.Requirement, s.driverIndex, refreshSource)
	if err != nil {
		return installItem{}, wrapWithRegistryContext(err, s.registryErrors)
	}

	var pkg dbc.PkgInfo
	if exactVersion != "" {
		version, err := semver.NewVersion(exactVersion)
		if err != nil {
			return installItem{}, fmt.Errorf("invalid locked version %q for %s: %w", exactVersion, planned.Name, err)
		}
		// Exact versions reach this path only after Requirement accepts them.
		// Ask the legacy registry adapter for that exact prerelease as well;
		// otherwise GetPackage's latest-selection filter would reject a version
		// explicitly selected by the constraint.
		pkg, err = driver.GetPackage(version, config.PlatformTuple(), true)
	} else if constraint, ok := planned.Requirement.Version().Constraint(); ok && constraint != "" {
		parsed, err := semver.NewConstraint(constraint)
		if err != nil {
			return installItem{}, fmt.Errorf("invalid registry constraint %q for %s: %w", constraint, planned.Name, err)
		}
		parsed.IncludePrerelease = allowPrerelease
		pkg, err = driver.GetWithConstraint(parsed, config.PlatformTuple())
	} else {
		pkg, err = driver.GetPackage(nil, config.PlatformTuple(), allowPrerelease)
	}
	if err != nil {
		return installItem{}, err
	}

	release, err := resolvedReleaseFromRegistryPackage(driver, pkg)
	if err != nil {
		return installItem{}, err
	}
	if refreshingSnapshot {
		resolvedSource, sourceErr := sourceidentity.Parse(sourceidentity.Kind(release.Source.Type), release.Source.Reference)
		if sourceErr != nil || resolvedSource.Kind != sourceidentity.Registry {
			return installItem{}, fmt.Errorf("registry refresh for %q returned an invalid source identity", planned.Name)
		}
		if expectedRefreshSource == nil || resolvedSource != *expectedRefreshSource {
			expected := "unknown"
			if expectedRefreshSource != nil {
				expected = expectedRefreshSource.Reference
			}
			return installItem{}, fmt.Errorf("registry refresh for %q returned source %q instead of locked source %q", planned.Name, resolvedSource.Reference, expected)
		}
		lockedVersion, lockErr := semver.NewVersion(exactVersion)
		resolvedVersion, resolveErr := semver.NewVersion(release.Version)
		if lockErr != nil || resolveErr != nil ||
			!sourceresolution.SameReleaseVersion(sourceidentity.Registry, lockedVersion, resolvedVersion) {
			return installItem{}, fmt.Errorf("registry refresh for %q resolved version %q instead of locked version %q", planned.Name, release.Version, exactVersion)
		}
	}
	item, err := installItemFromResolverResult(planned.Requirement, release, priorLock)
	if err != nil {
		return installItem{}, err
	}
	if planned.LegacyLock != nil && planned.LegacyLock.Version != nil && priorLock == nil &&
		planned.Requirement.Source().Mode() == sourceresolution.DefaultRegistry {
		resolvedVersion, versionErr := semver.NewVersion(release.Version)
		if versionErr == nil && sourceresolution.SameReleaseVersion(sourceidentity.Registry, planned.LegacyLock.Version, resolvedVersion) {
			copy := cloneLockInfo(*planned.LegacyLock)
			item.LockEntry = &copy
		}
	}
	return item, nil
}

func registryDriverForRequirement(requirement sourceresolution.Requirement, drivers []dbc.Driver, refreshSource *sourceidentity.Key) (dbc.Driver, error) {
	if requirement.Source().Mode() == sourceresolution.DefaultRegistry && refreshSource == nil {
		return findDriver(requirement.DriverID(), drivers)
	}
	var key sourceidentity.Key
	if requirement.Source().Mode() == sourceresolution.DefaultRegistry {
		if refreshSource == nil || refreshSource.Kind != sourceidentity.Registry {
			return dbc.Driver{}, fmt.Errorf("driver %q has an invalid pinned registry source", requirement.DriverID())
		}
		key = *refreshSource
	} else {
		var ok bool
		key, ok = requirement.Source().Key()
		if !ok || key.Kind != sourceidentity.Registry {
			return dbc.Driver{}, fmt.Errorf("driver %q has an unsupported registry source selection", requirement.DriverID())
		}
	}
	var matches []dbc.Driver
	for _, driver := range drivers {
		if driver.Registry == nil || driver.Registry.BaseURL == nil {
			continue
		}
		candidate, err := sourceidentity.Parse(sourceidentity.Registry, driver.Registry.BaseURL.String())
		if err == nil && candidate == key {
			matches = append(matches, driver)
		}
	}
	driver, err := findDriver(requirement.DriverID(), matches)
	if err != nil {
		if refreshSource != nil {
			return dbc.Driver{}, fmt.Errorf("driver %q was not found in locked registry source %q", requirement.DriverID(), key.Reference)
		}
		return dbc.Driver{}, fmt.Errorf("driver %q was not found in declared registry %q", requirement.DriverID(), key.Reference)
	}
	return driver, nil
}

func resolvedReleaseFromRegistryPackage(driver dbc.Driver, pkg dbc.PkgInfo) (resolution.ResolvedRelease, error) {
	if driver.Path == "" || pkg.Version == nil || strings.TrimSpace(pkg.PlatformTuple) == "" {
		return resolution.ResolvedRelease{}, errors.New("registry package metadata is incomplete")
	}
	if driver.Registry == nil || driver.Registry.BaseURL == nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("driver %q has no registry identity", driver.Path)
	}
	registryKey, err := sourceidentity.Parse(sourceidentity.Registry, driver.Registry.BaseURL.String())
	if err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("driver %q has invalid registry identity: %w", driver.Path, err)
	}
	if pkg.Driver.Path != "" && pkg.Driver.Path != driver.Path {
		return resolution.ResolvedRelease{}, fmt.Errorf("registry resolver returned package for driver %q while resolving %q", pkg.Driver.Path, driver.Path)
	}
	if pkg.Driver.Registry != nil {
		if pkg.Driver.Registry.BaseURL == nil {
			return resolution.ResolvedRelease{}, fmt.Errorf("registry resolver returned package without registry identity for %q", driver.Path)
		}
		packageRegistry, err := sourceidentity.Parse(sourceidentity.Registry, pkg.Driver.Registry.BaseURL.String())
		if err != nil || packageRegistry != registryKey {
			return resolution.ResolvedRelease{}, fmt.Errorf("registry resolver returned package from a different source for %q", driver.Path)
		}
	}
	if pkg.Path == nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("registry package metadata for %q has no archive URL", driver.Path)
	}
	target, err := resolution.TargetFromPlatformTuple(pkg.PlatformTuple)
	if err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("invalid registry package platform %q: %w", pkg.PlatformTuple, err)
	}
	size := cloneInt64(pkg.ArtifactSize)
	release := resolution.ResolvedRelease{
		DriverID: driver.Path,
		Version:  pkg.Version.String(),
		Source:   resolution.SourceSpec{Type: "registry", Reference: registryKey.Reference},
		Artifacts: []resolution.Artifact{{
			Target: target, Format: "tar.gz", Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: pkg.Path.String()},
			Hash: pkg.ArtifactHash, Size: size,
		}},
	}
	if err := resolution.ValidateResolvedReleaseCandidate(release); err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("invalid registry package metadata: %w", err)
	}
	return release, nil
}

func installItemFromResolverResult(requirement sourceresolution.Requirement, release resolution.ResolvedRelease, priorLock *lockInfo) (installItem, error) {
	selected, err := requirement.ValidateResolverResult(release)
	if err != nil {
		return installItem{}, fmt.Errorf("registry resolver returned an invalid release: %w", err)
	}
	for i := range release.Artifacts {
		if release.Artifacts[i].Target == selected.Target {
			return newInstallItem(release, i, config.PlatformTuple(), priorLock)
		}
	}
	return installItem{}, fmt.Errorf("validated resolver artifact for %q is not part of its release", requirement.DriverID())
}

func installItemFromLockedArtifact(name string, entry lockInfo, artifact lockArtifact) (installItem, error) {
	if err := validateInstallableArtifactFormat(artifact.Format); err != nil {
		return installItem{}, fmt.Errorf("locked artifact for %s: %w", name, err)
	}
	requirements := resolutionHostRequirementsFromLock(artifact.HostRequirements)
	if err := validateHostRequirements(name, requirements); err != nil {
		return installItem{}, err
	}
	if err := resolution.ValidateArtifactLocation(artifact.Location); err != nil {
		return installItem{}, fmt.Errorf("invalid locked artifact location for %s: %w", name, err)
	}
	release := entry.resolvedRelease()
	artifactIndex := -1
	for index := range release.Artifacts {
		if release.Artifacts[index].Target == artifact.Target {
			artifactIndex = index
			break
		}
	}
	if artifactIndex < 0 {
		return installItem{}, fmt.Errorf("locked artifact for %s is not part of its release snapshot", name)
	}
	if !reflect.DeepEqual(lockArtifactFromResolved(release.Artifacts[artifactIndex]), artifact) {
		return installItem{}, fmt.Errorf("locked artifact for %s does not match its release snapshot", name)
	}
	if release.DriverID != name {
		return installItem{}, fmt.Errorf("locked release driver ID %q does not match %q", release.DriverID, name)
	}
	return newInstallItem(release, artifactIndex, config.PlatformTuple(), &entry)
}

func validateInstallableArtifactFormat(format string) error {
	switch format {
	case "", "tar.gz", "tgz":
		return nil
	default:
		return fmt.Errorf("unsupported package format %q (supported formats: tar.gz, tgz)", format)
	}
}

func cloneHostRequirements(requirements resolution.HostRequirements) resolution.HostRequirements {
	return resolution.HostRequirements{
		OSMin:    requirements.OSMin,
		GLibCMin: requirements.GLibCMin,
		Libs:     append([]string(nil), requirements.Libs...),
		Bins:     append([]resolution.NamedRequirement(nil), requirements.Bins...),
	}
}

func validateHostRequirements(driverID string, requirements resolution.HostRequirements) error {
	if requirements.OSMin == "" && requirements.GLibCMin == "" && len(requirements.Libs) == 0 && len(requirements.Bins) == 0 {
		return nil
	}
	var declared []string
	if requirements.OSMin != "" {
		declared = append(declared, fmt.Sprintf("os_min=%q", requirements.OSMin))
	}
	if requirements.GLibCMin != "" {
		declared = append(declared, fmt.Sprintf("glibc_min=%q", requirements.GLibCMin))
	}
	if len(requirements.Libs) != 0 {
		libs := append([]string(nil), requirements.Libs...)
		sort.Strings(libs)
		quoted := make([]string, len(libs))
		for i, lib := range libs {
			quoted[i] = fmt.Sprintf("%q", lib)
		}
		declared = append(declared, "libs=["+strings.Join(quoted, ", ")+"]")
	}
	if len(requirements.Bins) != 0 {
		bins := append([]resolution.NamedRequirement(nil), requirements.Bins...)
		sort.Slice(bins, func(i, j int) bool {
			if bins[i].Name != bins[j].Name {
				return bins[i].Name < bins[j].Name
			}
			return bins[i].Min < bins[j].Min
		})
		formatted := make([]string, len(bins))
		for i, bin := range bins {
			formatted[i] = fmt.Sprintf("%q", bin.Name)
			if bin.Min != "" {
				formatted[i] += fmt.Sprintf(" (min %q)", bin.Min)
			}
		}
		declared = append(declared, "bins=["+strings.Join(formatted, ", ")+"]")
	}
	return fmt.Errorf("unsupported host requirements for %s: %s", driverID, strings.Join(declared, "; "))
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
	selected, err := item.selectedArtifact()
	if err != nil || item.LockEntry == nil || item.LockEntry.Version == nil {
		return false
	}
	version, err := semver.NewVersion(item.Release.Version)
	if err != nil || !packageVersionsMatch(item.LockEntry.Source.Type, item.LockEntry.Version, version) {
		return false
	}
	source, err := lockSourceForResolvedRelease(item.Release)
	if err != nil || !sameLockSourceIdentity(item.LockEntry.Source, source) {
		return false
	}
	locked, err := selectLockedArtifact(*item.LockEntry, item.Platform, false)
	if err != nil {
		return false
	}
	return locked.Target == selected.Target && locked.Location == selected.Location &&
		locked.Hash == selected.Hash && sameLockSize(locked.Size, selected.Size) &&
		locked.Format == selected.Format && locked.PackageVersion == selected.PackageVersion &&
		reflect.DeepEqual(canonicalHostRequirements(locked.HostRequirements), canonicalHostRequirements(lockHostRequirementsFromResolution(selected.HostRequirements)))
}

func packageVersionsMatch(sourceType string, locked, resolved *semver.Version) bool {
	return sourceresolution.SameReleaseVersion(sourceidentity.Kind(sourceType), locked, resolved)
}

func lockSourceForResolvedRelease(release resolution.ResolvedRelease) (lockSource, error) {
	source := lockSource{Type: release.Source.Type}
	switch release.Source.Type {
	case "registry":
		source.URL = release.Source.Reference
	case "packslip":
		source.Project = release.Source.Reference
	case "path":
		source.Path = release.Source.Reference
	default:
		return lockSource{}, fmt.Errorf("unsupported resolved source type %q", release.Source.Type)
	}
	if _, err := lockSourceIdentity(source); err != nil {
		return lockSource{}, err
	}
	return source, nil
}

func snapshotDownloadedArchive(item *installItem, archive *os.File) error {
	if archive == nil {
		return fmt.Errorf("download returned no archive")
	}
	selected, err := item.selectedArtifact()
	if err != nil {
		return err
	}
	if (selected.Hash == "") != (selected.Size == nil) {
		return errors.New("artifact hash and size must either both be present or both be absent")
	}
	if err := resolution.ValidateArtifactMetadata(selected.Hash, selected.Size); err != nil {
		return err
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
	if selected.Hash != "" && selected.Hash != actualHash {
		return fmt.Errorf("downloaded archive hash %s does not match expected hash %s", actualHash, selected.Hash)
	}
	if selected.Size != nil && *selected.Size != actualSize {
		return fmt.Errorf("downloaded archive size %d does not match expected size %d", actualSize, *selected.Size)
	}
	selected.Hash = actualHash
	selected.Size = cloneInt64(&actualSize)
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to rewind downloaded archive: %w", err)
	}
	return nil
}

func (s syncModel) openResolvedArtifact(ctx context.Context, item installItem) (*sourceresolution.OpenedArtifact, error) {
	selected, err := item.selectedArtifact()
	if err != nil {
		return nil, err
	}
	basePath := s.LockFilePath
	if basePath == "" {
		basePath = s.Path
	}
	if basePath == "" {
		return nil, errors.New("sync project path is not set")
	}
	baseDir, err := filepath.Abs(filepath.Dir(basePath))
	if err != nil {
		return nil, fmt.Errorf("resolve project directory: %w", err)
	}
	fetchURL := func(ctx context.Context, artifactURL *url.URL) (io.ReadCloser, error) {
		version, err := semver.NewVersion(item.Release.Version)
		if err != nil {
			return nil, fmt.Errorf("invalid resolved package version %q: %w", item.Release.Version, err)
		}
		pkg := dbc.PkgInfo{
			Driver:        dbc.Driver{Path: item.Release.DriverID, Title: item.Release.DriverID},
			Version:       version,
			PlatformTuple: item.Platform,
			Path:          artifactURL,
			ArtifactHash:  selected.Hash,
			ArtifactSize:  cloneInt64(selected.Size),
		}
		if s.downloadArtifact != nil {
			return s.downloadArtifact(ctx, pkg)
		}
		if s.downloadPkg == nil {
			return nil, errors.New("no artifact downloader is configured")
		}
		// Existing test adapters and custom models expose the legacy file-based
		// hook. Production sync uses downloadArtifact, which calls Client.Download.
		return s.downloadPkg(pkg)
	}
	return sourceresolution.OpenArtifact(ctx, fetchURL, selected.Location, baseDir)
}

func (s syncModel) prepareInstallItems(ctx context.Context, items []installItem) (preparedSyncMsg, error) {
	prepared := preparedSyncMsg{items: items, lock: LockFile{Version: lockFileVersion}}
	for i := range prepared.items {
		if err := ctx.Err(); err != nil {
			return prepared, err
		}
		item := &prepared.items[i]
		selected, err := item.selectedArtifact()
		if err != nil {
			return prepared, err
		}
		if err := validateInstallableArtifactFormat(selected.Format); err != nil {
			return prepared, fmt.Errorf("driver %s: %w", item.Release.DriverID, err)
		}
		if err := validateHostRequirements(item.Release.DriverID, selected.HostRequirements); err != nil {
			return prepared, err
		}
		var sameVersionInstalled *config.DriverInfo
		if s.cfg.Exists {
			if installed, ok := s.cfg.Drivers[item.Release.DriverID]; ok {
				if installed.Version != nil && item.Release.Version == installed.Version.String() {
					installedCopy := installed
					sameVersionInstalled = &installedCopy
					expected, _, err := expectedSyncPackageMetadata(*item)
					if err != nil {
						return prepared, err
					}
					item.Expected = expected
					matches, err := s.itemCurrentMatches(item, &installedCopy)
					if err != nil {
						return prepared, err
					}
					if matches {
						libraryHash, hashErr := checksum(installed.Driver.Shared.Get(item.Platform))
						if hashErr != nil {
							return prepared, fmt.Errorf("failed to checksum installed driver: %w", hashErr)
						}
						markAlreadyInstalled(item, installed, libraryHash)
					}
				}
			}
		}

		needsArchive := item.AlreadyInstalled == nil || !canReuseLockedEntry(*item)
		if needsArchive {
			if err := s.downloadAndValidateItem(ctx, item); err != nil {
				return prepared, err
			}
			if sameVersionInstalled != nil {
				matches, err := s.itemCurrentMatches(item, sameVersionInstalled)
				if err != nil {
					return prepared, err
				}
				if matches {
					libraryHash, hashErr := checksum(sameVersionInstalled.Driver.Shared.Get(item.Platform))
					if hashErr != nil {
						return prepared, fmt.Errorf("failed to checksum installed driver: %w", hashErr)
					}
					markAlreadyInstalled(item, *sameVersionInstalled, libraryHash)
				}
			}
			if item.AlreadyInstalled == nil {
				item.InstalledLibraryHash = item.ValidatedLibraryHash
				if item.Validation != nil && item.Validation.VerifiedLibraryHash == "" &&
					item.LockEntry != nil && item.LockEntry.Legacy != nil && samePlatformTarget(item.LockEntry.Legacy.Platform, item.Platform) {
					item.InstalledLibraryHash = item.LockEntry.Legacy.LibraryHash
				}
			}
		} else {
			// The exact locked artifact is already proven by its managed receipt.
			expected, _, err := expectedSyncPackageMetadata(*item)
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

func (s syncModel) downloadAndValidateItem(ctx context.Context, item *installItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	selected, err := item.selectedArtifact()
	if err != nil {
		return err
	}
	if err := validateInstallableArtifactFormat(selected.Format); err != nil {
		return fmt.Errorf("driver %s: %w", item.Release.DriverID, err)
	}
	if err := validateHostRequirements(item.Release.DriverID, selected.HostRequirements); err != nil {
		return err
	}
	expected, hasMetadata, err := expectedSyncPackageMetadata(*item)
	if err != nil {
		return err
	}
	if item.Archive == nil {
		archive, err := s.openResolvedArtifact(ctx, *item)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("failed to open driver artifact: %w", err)
		}
		item.Archive = archive
	}
	archive := item.Archive.File
	if err := snapshotDownloadedArchive(item, archive); err != nil {
		return fmt.Errorf("failed to snapshot downloaded driver archive: %w", err)
	}
	selected, err = item.selectedArtifact()
	if err != nil {
		return err
	}
	if !hasMetadata {
		if _, err := archive.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("failed to rewind downloaded driver archive: %w", err)
		}
		packageManifest, inspectErr := config.InspectPackageMetadata(archive)
		if inspectErr != nil {
			return inspectErr
		}
		selected.PackageVersion = packageManifest.PackageVersion
		expected.PackageVersion = selected.PackageVersion
		if packageManifest.PackageVersion == 2 {
			return errors.New("package v2 requires archive hash and size metadata")
		}
	}
	expected.ArchiveHash = selected.Hash
	expected.ArchiveSize = *selected.Size
	item.Expected = expected
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to rewind downloaded driver archive: %w", err)
	}
	var verify func(string, config.Manifest) error
	if !s.NoVerify {
		verify = func(stagingDir string, manifest config.Manifest) error {
			return dbc.VerifyPackageSignature(stagingDir, manifest)
		}
	}
	validation, err := config.ValidatePackage(item.Release.DriverID, archive, expected, config.InstallOptions{Verify: verify})
	if err != nil {
		if isPackageVerificationFailure(err) {
			return fmt.Errorf("failed to verify signature: %w", packageVerificationError(err))
		}
		return fmt.Errorf("failed to validate driver package: %w", err)
	}
	selected.PackageVersion = validation.PackageVersion
	expected.PackageVersion = validation.PackageVersion
	item.Expected = expected
	item.Validation = &validation
	item.ValidatedLibraryHash = strings.TrimPrefix(validation.VerifiedLibraryHash, "sha256:")
	if item.ValidatedLibraryHash == "" {
		candidateLibrary := validation.Registration.Driver.Shared.Get(item.Platform)
		if item.LockEntry != nil && item.LockEntry.Legacy != nil && samePlatformTarget(item.LockEntry.Legacy.Platform, item.Platform) {
			if err := verifyLegacyExternalLibrary(candidateLibrary, item.LockEntry.Legacy.LibraryHash); err != nil {
				return fmt.Errorf("candidate package external library does not match the legacy lock proof: %w", err)
			}
		}
	}
	return nil
}

func (s syncModel) itemCurrentMatches(item *installItem, current *config.DriverInfo) (bool, error) {
	if current == nil || current.Version == nil || item.Expected.ID == "" || item.Expected.Version == "" ||
		current.ID != item.Expected.ID || current.Version.String() != item.Expected.Version {
		return false, nil
	}
	receipt, managed, present, valid, err := config.InspectDriverInstallReceipt(s.cfg, *current)
	if err != nil {
		return false, fmt.Errorf("failed to resolve installed driver receipt location: %w", err)
	}
	libraryPath := current.Driver.Shared.Get(item.Platform)
	if managed && present {
		if !valid || !config.InstallReceiptMatchesExpectedPackage(receipt, item.Expected) ||
			!config.VerifyInstallReceiptLibraryIntegrity(libraryPath, receipt) ||
			!config.InstallReceiptMatchesRuntimeRegistration(receipt, *current, item.Platform) {
			return false, nil
		}
		if item.Validation != nil {
			if item.Validation.VerifiedLibraryHash == "" || item.Validation.VerifiedLibraryHash != receipt.InstalledLibraryHash ||
				!config.PackageValidationMatchesRuntimeRegistration(*current, *item.Validation, item.Platform) {
				return false, nil
			}
		}
		return true, nil
	}
	if present || item.Validation == nil || item.LockEntry == nil || item.LockEntry.Legacy == nil ||
		!samePlatformTarget(item.LockEntry.Legacy.Platform, item.Platform) ||
		!config.PackageValidationMatchesRuntimeRegistration(*current, *item.Validation, item.Platform) {
		return false, nil
	}
	proofHash := item.LockEntry.Legacy.LibraryHash
	if err := validateLegacyLibraryHash(proofHash); err != nil {
		return false, nil
	}
	currentHash, err := checksum(libraryPath)
	if err != nil || currentHash != proofHash {
		return false, nil
	}
	if item.Validation.VerifiedLibraryHash != "" {
		return item.ValidatedLibraryHash == proofHash, nil
	}
	if err := verifyLegacyExternalLibrary(item.Validation.Registration.Driver.Shared.Get(item.Platform), proofHash); err != nil {
		return false, nil
	}
	return true, nil
}

func (s syncModel) ensurePreparedPackage(ctx context.Context, item *installItem) (config.EnsurePackageResult, error) {
	if err := ctx.Err(); err != nil {
		return config.EnsurePackageResult{}, err
	}
	selected, err := item.selectedArtifact()
	if err != nil {
		return config.EnsurePackageResult{}, err
	}
	if err := validateInstallableArtifactFormat(selected.Format); err != nil {
		return config.EnsurePackageResult{}, fmt.Errorf("driver %s: %w", item.Release.DriverID, err)
	}
	if err := validateHostRequirements(item.Release.DriverID, selected.HostRequirements); err != nil {
		return config.EnsurePackageResult{}, err
	}
	callbacks := config.EnsurePackageCallbacks{
		CurrentMatches: func(current *config.DriverInfo) (bool, error) {
			return s.itemCurrentMatches(item, current)
		},
		Archive: func(ctx context.Context) (*os.File, error) {
			if item.Archive == nil || item.Validation == nil {
				if err := s.downloadAndValidateItem(ctx, item); err != nil {
					return nil, err
				}
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if _, err := item.Archive.File.Seek(0, io.SeekStart); err != nil {
				return nil, fmt.Errorf("failed to rewind prepared driver archive: %w", err)
			}
			return item.Archive.File, nil
		},
		ValidateResult: func(result config.EnsurePackageResult) error {
			return s.validateEnsureResult(item, result)
		},
	}
	var verify func(string, config.Manifest) error
	if !s.NoVerify {
		verify = func(stagingDir string, manifest config.Manifest) error {
			return dbc.VerifyPackageSignature(stagingDir, manifest)
		}
	}
	ensure := s.worker.hooks.ensurePackage
	if ensure == nil {
		ensure = config.EnsurePackage
	}
	result, err := ensure(ctx, s.cfg, item.Release.DriverID, item.Expected, config.InstallOptions{Verify: verify}, callbacks)
	if err != nil && isPackageVerificationFailure(err) {
		return result, fmt.Errorf("failed to verify signature: %w", packageVerificationError(err))
	}
	return result, err
}

func (s syncModel) validateEnsureResult(item *installItem, result config.EnsurePackageResult) error {
	if result.Installed == nil {
		return errors.New("package ensure returned no installed driver registration")
	}
	if result.Skipped {
		matches, err := s.itemCurrentMatches(item, result.Installed)
		if err != nil {
			return err
		}
		if !matches {
			return errors.New("installed driver no longer matches the selected package proof")
		}
		return nil
	}
	if item.Validation == nil || result.Manifest == nil {
		return errors.New("installed package is missing validated candidate evidence")
	}
	if !config.PackageValidationMatchesRuntimeRegistration(*result.Installed, *item.Validation, item.Platform) ||
		!config.PackageValidationMatchesRuntimeRegistration(result.Manifest.DriverInfo, *item.Validation, item.Platform) {
		return errors.New("installed registration does not match the validated package")
	}
	if result.Installed.Version == nil || result.Installed.Version.String() != item.Expected.Version {
		return errors.New("installed driver version does not match the selected package")
	}
	if item.Validation.VerifiedLibraryHash == "" {
		path := result.Installed.Driver.Shared.Get(item.Platform)
		if item.LockEntry != nil && item.LockEntry.Legacy != nil && samePlatformTarget(item.LockEntry.Legacy.Platform, item.Platform) {
			if err := verifyLegacyExternalLibrary(path, item.LockEntry.Legacy.LibraryHash); err != nil {
				return fmt.Errorf("installed package external library does not match the legacy lock proof: %w", err)
			}
		}
		return nil
	}
	receipt, managed, present, valid, err := config.InspectDriverInstallReceipt(s.cfg, *result.Installed)
	if err != nil {
		return fmt.Errorf("failed to resolve installed driver receipt location: %w", err)
	}
	libraryPath := result.Installed.Driver.Shared.Get(item.Platform)
	if !managed || !present || !valid || !config.InstallReceiptMatchesExpectedPackage(receipt, item.Expected) ||
		receipt.InstalledLibraryHash != item.Validation.VerifiedLibraryHash ||
		!config.InstallReceiptMatchesRuntimeRegistration(receipt, *result.Installed, item.Platform) ||
		!config.VerifyInstallReceiptLibraryIntegrity(libraryPath, receipt) {
		return errors.New("installed package receipt does not match the validated package")
	}
	actualHash, err := checksum(libraryPath)
	if err != nil {
		return syncChecksumError{err: err}
	}
	if actualHash != item.ValidatedLibraryHash {
		return syncChecksumError{err: errors.New("installed library checksum does not match validated package")}
	}
	return nil
}

func expectedSyncPackageMetadata(item installItem) (config.ExpectedPackageMetadata, bool, error) {
	selected, err := item.selectedArtifact()
	if err != nil {
		return config.ExpectedPackageMetadata{}, false, err
	}
	if item.Release.DriverID == "" || item.Release.Version == "" || item.Platform == "" ||
		item.Release.Source.Type == "" || item.Release.Source.Reference == "" {
		return config.ExpectedPackageMetadata{}, false, errors.New("resolved package metadata is incomplete")
	}
	hasHash := selected.Hash != ""
	hasSize := selected.Size != nil
	if hasHash != hasSize {
		return config.ExpectedPackageMetadata{}, false, errors.New("package metadata must include both archive hash and size")
	}
	if selected.PackageVersion == 2 && !hasHash {
		return config.ExpectedPackageMetadata{}, false, errors.New("package v2 requires archive hash and size metadata")
	}
	expected := config.ExpectedPackageMetadata{
		ID: item.Release.DriverID, Version: item.Release.Version, Platform: item.Platform,
		SourceType: item.Release.Source.Type, SourceIdentity: item.Release.Source.Reference,
		PackageVersion: selected.PackageVersion,
	}
	if hasHash {
		expected.ArchiveHash = selected.Hash
		expected.ArchiveSize = *selected.Size
	}
	return expected, hasHash, nil
}

func markAlreadyInstalled(item *installItem, installed config.DriverInfo, libraryHash string) {
	item.InstalledLibraryHash = strings.TrimPrefix(libraryHash, "sha256:")
	installedCopy := installed
	item.AlreadyInstalled = &installedCopy
}

func verifyLegacyExternalLibrary(path, expectedHash string) error {
	if err := validateLegacyLibraryHash(expectedHash); err != nil {
		return fmt.Errorf("invalid legacy library checksum: %w", err)
	}
	if path == "" {
		return errors.New("candidate runtime registration has no shared library path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("could not inspect candidate external library %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("candidate external library %s is not a regular file", path)
	}
	actualHash, err := checksum(path)
	if err != nil {
		return err
	}
	if actualHash != expectedHash {
		return fmt.Errorf("candidate external library checksum mismatch: got %s, expected %s", actualHash, expectedHash)
	}
	return nil
}

type syncChecksumError struct{ err error }

func (e syncChecksumError) Error() string { return e.err.Error() }
func (e syncChecksumError) Unwrap() error { return e.err }

func acquireSyncProjectLock(ctx context.Context, lockPath string) (fslock.Lock, error) {
	lock, err := fslock.AcquireContext(ctx, lockPath)
	if err == nil {
		return lock, nil
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return fslock.Lock{}, ctx.Err()
	}
	if errors.Is(err, os.ErrPermission) {
		return fslock.Lock{}, fmt.Errorf(
			"cannot write to %s: permission denied.\nThis command requires elevated privileges; try %s.",
			filepath.Dir(lockPath), elevationHint())
	}
	if errors.Is(err, fslock.ErrLockContended) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fslock.Lock{}, fmt.Errorf("another dbc operation is in progress: %w: %v", fslock.ErrLockContended, err)
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

	planned, needsRegistry, err := s.planSyncItems(s.list)
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

	items, err := s.createInstallList(planned)
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
		if !worker.send(ctx, syncResolvingMsg{driver: item.Release.DriverID}) {
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

	for i := range prepared.items {
		if err := ctx.Err(); err != nil {
			return fail("sync_failed", err)
		}
		item := &prepared.items[i]
		ensured, err := s.ensurePreparedPackage(ctx, item)
		if err != nil {
			var checksumErr syncChecksumError
			if errors.As(err, &checksumErr) {
				return fail("checksum_failed", checksumErr)
			}
			return fail("sync_failed", err)
		}
		if ensured.Skipped {
			if ensured.Installed == nil {
				return fail("sync_failed", errors.New("package ensure skipped without an installed registration"))
			}
			progress := alreadyInstalledDrvMsg{info: *ensured.Installed, item: *item}
			progress.item.Archive = nil
			if !worker.send(ctx, progress) {
				return fail("sync_failed", ctx.Err())
			}
			continue
		}
		if ensured.Manifest == nil || ensured.Installed == nil {
			return fail("sync_failed", errors.New("package ensure completed without installation details"))
		}
		installed := installedDrvMsg{
			removed:     ensured.Previous,
			info:        *ensured.Installed,
			postInstall: ensured.Manifest.PostInstall.Messages,
			item:        *item,
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
	selected, err := item.selectedArtifact()
	if err != nil {
		return lockInfo{}, err
	}
	if err := validateInstallableArtifactFormat(selected.Format); err != nil {
		return lockInfo{}, fmt.Errorf("driver %s: %w", item.Release.DriverID, err)
	}
	if err := validateHostRequirements(item.Release.DriverID, selected.HostRequirements); err != nil {
		return lockInfo{}, err
	}
	legacyProofMatches := true
	if item.LockEntry != nil && item.LockEntry.Legacy != nil && samePlatformTarget(item.LockEntry.Legacy.Platform, item.Platform) {
		if err := verifyLegacyLibraryProof(*item.LockEntry, item.Platform, item.InstalledLibraryHash); err != nil {
			if !hasValidatedReplacementEvidence(item) {
				return lockInfo{}, err
			}
			// A mismatched legacy proof cannot be carried into a candidate that
			// will replace the old library. The validated archive becomes the new
			// v2 artifact evidence instead.
			legacyProofMatches = false
		}
	}
	if canReuseLockedEntry(item) && legacyProofMatches {
		return *item.LockEntry, nil
	}
	candidate, err := lockInfoFromResolvedRelease(item.Release.DriverID, item.Release)
	if err != nil {
		return lockInfo{}, err
	}
	if item.LockEntry == nil || item.LockEntry.Version == nil ||
		!packageVersionsMatch(candidate.Source.Type, item.LockEntry.Version, candidate.Version) {
		return candidate, nil
	}
	if len(item.LockEntry.Artifacts) == 0 {
		if item.LockEntry.Legacy != nil {
			if !legacyProofMatches {
				return candidate, nil
			}
			var verified *VerifiedLegacyLibrary
			if samePlatformTarget(item.LockEntry.Legacy.Platform, item.Platform) {
				verified = &VerifiedLegacyLibrary{Platform: item.Platform, LibraryHash: item.InstalledLibraryHash}
			}
			return migrateV1Entry(*item.LockEntry, item.Release, item.Platform, verified)
		}
		return candidate, nil
	}
	existing := *item.LockEntry
	if !legacyProofMatches {
		existing.Legacy = nil
	}
	return refreshLockEntry(existing, candidate)
}

func hasValidatedReplacementEvidence(item installItem) bool {
	selected, err := item.selectedArtifact()
	if err != nil || item.AlreadyInstalled != nil || item.Archive == nil || selected.Hash == "" || selected.Size == nil || *selected.Size <= 0 || item.ValidatedLibraryHash == "" {
		return false
	}
	if validateLegacyLibraryHash(item.ValidatedLibraryHash) != nil ||
		item.Expected.ID != item.Release.DriverID || item.Expected.Version != item.Release.Version ||
		item.Expected.Platform == "" || item.Expected.Platform != item.Platform ||
		item.Expected.SourceType != item.Release.Source.Type || item.Expected.SourceIdentity != item.Release.Source.Reference ||
		item.Expected.ArchiveHash != selected.Hash || item.Expected.ArchiveSize != *selected.Size {
		return false
	}
	archiveInfo, err := item.Archive.File.Stat()
	return err == nil && archiveInfo.Size() == *selected.Size
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
		planned, needsRegistry, err := s.planSyncItems(s.list)
		if err != nil {
			return s, errCmd("failed to inspect lock file: %w", err)
		}
		if !needsRegistry {
			return s, func() tea.Msg {
				returnItems, err := s.createInstallList(planned)
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
			chksum, err := checksum(msg.info.Driver.Shared.Get(msg.item.Platform))
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
	driverName := s.installItems[driverIndex].Release.DriverID
	info := lipgloss.NewStyle().MaxWidth(cellsAvail).Render("Installing " + driverName)

	cellsRemaining := max(0, s.width-lipgloss.Width(spin+info+prog+driverCount))
	gap := strings.Repeat(" ", max(0, cellsRemaining))

	return tea.NewView(spin + info + gap + prog + driverCount)
}
