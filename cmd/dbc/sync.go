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
	"os"
	"path/filepath"
	"reflect"
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
	"github.com/columnar-tech/dbc/internal/packslip"
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
	Spec        driverSpec
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
			if requirement.Source().Mode() == sourceresolution.DefaultRegistry {
				needsRegistry = true
			} else if key, ok := requirement.Source().Key(); ok && key.Kind == sourceidentity.Registry {
				needsRegistry = true
			}
		case sourceresolution.PlanLockedArtifactMissing:
			return nil, false, &LockedModeArtifactMissingError{DriverID: name, Platform: config.PlatformTuple()}
		case sourceresolution.PlanReject:
			return nil, false, fmt.Errorf("cannot plan sync for driver %q: %w", name, plan.Err())
		default:
			return nil, false, fmt.Errorf("cannot plan sync for driver %q: invalid plan outcome %d", name, plan.Outcome())
		}
		planned = append(planned, plannedSyncItem{
			Name: name, Spec: spec, Requirement: requirement, Plan: plan,
			LockEntry: existing, LegacyLock: legacy,
		})
	}
	return planned, needsRegistry, nil
}

func requirementForDriverSpec(name string, spec driverSpec) (sourceresolution.Requirement, error) {
	var selection sourceresolution.SourceSelection
	sourceKind := sourceidentity.Registry
	if spec.Source == nil {
		selection = sourceresolution.DefaultRegistrySelection()
	} else {
		key, err := driverSourceIdentity(spec.Source)
		if err != nil {
			return sourceresolution.Requirement{}, fmt.Errorf("driver %q has invalid declared source: %w", name, err)
		}
		selection, err = sourceresolution.ExplicitSourceSelection(key)
		if err != nil {
			return sourceresolution.Requirement{}, fmt.Errorf("driver %q has invalid declared source: %w", name, err)
		}
		sourceKind = key.Kind
	}

	var version sourceresolution.VersionRequirement
	var err error
	switch sourceKind {
	case sourceidentity.Registry:
		constraint := ""
		if spec.Version != nil {
			constraint = spec.Version.String()
		}
		policy := sourceresolution.PrereleaseForbidden
		if spec.Prerelease == "allow" || (spec.Version != nil && spec.Version.IncludePrerelease) {
			policy = sourceresolution.PrereleaseAllowed
		}
		version, err = sourceresolution.RegistryVersionRequirement(constraint, policy)
	case sourceidentity.Packslip:
		if spec.Prerelease != "" {
			return sourceresolution.Requirement{}, fmt.Errorf("driver %q packslip source does not support prerelease policy", name)
		}
		if spec.Version == nil {
			// TODO: Exact versions are required only for the current Packslip PoC.
			// Before Packslip support is generally available, allow omitted versions
			// for GitHub-hosted projects by discovering eligible releases and resolving
			// the latest one to an exact ResolvedRelease. Requirements may then be
			// moving, but ResolvedRelease and dbc.lock must remain exact. Non-GitHub
			// projects can use signed Packslip release lists for version discovery.
			return sourceresolution.Requirement{}, fmt.Errorf("driver %q packslip source requires an exact SemVer 2.0.0 version", name)
		}
		version, err = sourceresolution.PackslipVersionRequirement(spec.Version.String())
	case sourceidentity.Path:
		if spec.Prerelease != "" {
			return sourceresolution.Requirement{}, fmt.Errorf("driver %q path source does not support prerelease policy", name)
		}
		if spec.Version == nil {
			version = sourceresolution.PathMetadataVersionRequirement()
		} else {
			version, err = sourceresolution.PathVersionRequirement(spec.Version.String())
		}
	default:
		return sourceresolution.Requirement{}, fmt.Errorf("source type %q for driver %q is not supported by sync", sourceKind, name)
	}
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
	return s.createInstallListContext(context.Background(), planned)
}

func (s syncModel) createInstallListContext(ctx context.Context, planned []plannedSyncItem) ([]installItem, error) {
	items := make([]installItem, 0, len(planned))
	var packslipResolver packslip.Resolver
	for _, entry := range planned {
		switch entry.Plan.Outcome() {
		case sourceresolution.PlanReplay:
			item, err := installItemFromPlan(entry)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		case sourceresolution.PlanResolve, sourceresolution.PlanRefreshRequired:
			var item installItem
			var err error
			kind := sourceidentity.Registry
			if entry.Requirement.Source().Mode() == sourceresolution.ExplicitSource {
				if key, ok := entry.Requirement.Source().Key(); ok {
					kind = key.Kind
				}
			}
			switch kind {
			case sourceidentity.Registry:
				item, err = s.resolveRegistryPlan(entry)
			case sourceidentity.Packslip:
				if packslipResolver == nil {
					if s.newPackslipResolver == nil {
						return nil, errors.New("no Packslip resolver is configured")
					}
					packslipResolver, err = s.newPackslipResolver()
					if err != nil {
						return nil, fmt.Errorf("create Packslip resolver: %w", err)
					}
				}
				item, err = s.resolvePackslipPlan(ctx, entry, packslipResolver)
			case sourceidentity.Path:
				item, err = s.resolvePathPlan(ctx, entry)
			default:
				err = fmt.Errorf("source type %q for driver %q is not supported by sync", kind, entry.Name)
			}
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

func (s syncModel) resolvePackslipPlan(ctx context.Context, planned plannedSyncItem, resolver packslip.Resolver) (installItem, error) {
	if planned.Spec.Source == nil || planned.Spec.Source.Type != dbc.DriverSourcePackslip {
		return installItem{}, fmt.Errorf("Packslip plan for %q has no Packslip source declaration", planned.Name)
	}
	version := ""
	if exact, ok := planned.Requirement.Version().ExactVersion(); ok {
		version = exact
	}
	var priorLock *lockInfo
	if planned.Plan.Outcome() == sourceresolution.PlanRefreshRequired {
		release, ok := planned.Plan.Release()
		if !ok {
			return installItem{}, fmt.Errorf("Packslip refresh plan for %q has no prior release", planned.Name)
		}
		version = release.Version
		priorLock = planned.LockEntry
	}
	release, err := sourceresolution.ResolvePackslip(ctx, resolver, planned.Spec.Source.Project, planned.Name, version)
	if err != nil {
		return installItem{}, err
	}
	return installItemFromResolverResult(planned.Requirement, release, priorLock)
}

func (s syncModel) resolvePathPlan(ctx context.Context, planned plannedSyncItem) (installItem, error) {
	if planned.Spec.Source == nil || planned.Spec.Source.Type != dbc.DriverSourcePath {
		return installItem{}, fmt.Errorf("path plan for %q has no path source declaration", planned.Name)
	}
	version := ""
	if exact, ok := planned.Requirement.Version().ExactVersion(); ok {
		version = exact
	}
	var priorLock *lockInfo
	var priorVersion string
	if planned.Plan.Outcome() == sourceresolution.PlanRefreshRequired {
		release, ok := planned.Plan.Release()
		if !ok {
			return installItem{}, fmt.Errorf("path refresh plan for %q has no prior release", planned.Name)
		}
		priorVersion = release.Version
		if planned.Requirement.Version().Mode() != sourceresolution.PathMetadataDerived {
			version = release.Version
		}
		priorLock = planned.LockEntry
	}
	baseDir, err := s.projectBaseDir()
	if err != nil {
		return installItem{}, err
	}
	target, err := resolution.TargetFromPlatformTuple(config.PlatformTuple())
	if err != nil {
		return installItem{}, fmt.Errorf("unsupported sync platform: %w", err)
	}
	release, err := sourceresolution.ResolvePath(ctx, planned.Spec.Source.Path, sourceresolution.Request{
		DriverID: planned.Name, Version: version, Target: target,
		Platform: config.PlatformTuple(), BaseDir: baseDir,
	})
	if err != nil {
		return installItem{}, err
	}
	if priorLock != nil && priorVersion != "" && release.Version != priorVersion {
		// An omitted path version follows the current archive metadata. Once it
		// changes, the prior snapshot (including artifacts, evidence, and any
		// legacy proof) no longer describes the candidate release.
		priorLock = nil
	}
	return installItemFromResolverResult(planned.Requirement, release, priorLock)
}

func (s syncModel) projectBaseDir() (string, error) {
	basePath := s.LockFilePath
	if basePath == "" {
		basePath = s.Path
	}
	if basePath == "" {
		return "", errors.New("sync project path is not set")
	}
	baseDir, err := filepath.Abs(filepath.Dir(basePath))
	if err != nil {
		return "", fmt.Errorf("resolve project directory: %w", err)
	}
	return baseDir, nil
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
		return installItem{}, fmt.Errorf("source resolver returned an invalid release: %w", err)
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

func cloneHostRequirements(requirements resolution.HostRequirements) resolution.HostRequirements {
	return resolution.HostRequirements{
		OSMin:    requirements.OSMin,
		GLibCMin: requirements.GLibCMin,
		Libs:     append([]string(nil), requirements.Libs...),
		Bins:     append([]resolution.NamedRequirement(nil), requirements.Bins...),
	}
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

func (s syncModel) prepareInstallItems(ctx context.Context, items []installItem) (preparedSyncMsg, error) {
	prepared := preparedSyncMsg{items: items, lock: LockFile{Version: lockFileVersion}}
	if err := ctx.Err(); err != nil {
		return prepared, err
	}
	if len(prepared.items) == 0 {
		return prepared, nil
	}
	executor, err := s.newPackageExecutor()
	if err != nil {
		return prepared, err
	}
	for i := range prepared.items {
		if err := ctx.Err(); err != nil {
			return prepared, err
		}
		item := &prepared.items[i]
		if err := executor.prepareItem(ctx, item); err != nil {
			return prepared, err
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
	executor, err := s.newPackageExecutor()
	if err != nil {
		return err
	}
	return executor.downloadAndValidateItem(ctx, item)
}

func (s syncModel) itemCurrentMatches(item *installItem, current *config.DriverInfo) (bool, error) {
	executor, err := s.newPackageExecutor()
	if err != nil {
		return false, err
	}
	return executor.itemCurrentMatches(item, current)
}

func (s syncModel) ensurePreparedPackage(ctx context.Context, item *installItem) (config.EnsurePackageResult, error) {
	executor, err := s.newPackageExecutor()
	if err != nil {
		return config.EnsurePackageResult{}, err
	}
	return executor.ensurePreparedPackage(ctx, item)
}

func (s syncModel) validateEnsureResult(item *installItem, result config.EnsurePackageResult) error {
	executor, err := s.newPackageExecutor()
	if err != nil {
		return err
	}
	return executor.validateEnsureResult(item, result)
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

	items, err := s.createInstallListContext(ctx, planned)
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
