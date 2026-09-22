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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/columnar-tech/dbc/internal/sourceidentity"
	"github.com/columnar-tech/dbc/internal/sourceresolution"
)

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
	if err != nil || !sourceresolution.SameReleaseVersion(sourceidentity.Kind(item.LockEntry.Source.Type), item.LockEntry.Version, version) {
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

func (s syncModel) runSyncWorker(worker *syncWorker) (result syncWorkerResultMsg) {
	result = syncWorkerResultMsg{code: "sync_failed"}
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

	planned, needsRegistry, inputLockVersion, err := s.planSyncItemsWithVersion(s.list)
	if err != nil {
		return fail("sync_failed", fmt.Errorf("failed to inspect lock file: %w", err))
	}
	// Keep the version from the snapshot that actually drove planning in the
	// worker. The model is copied through Bubble Tea messages, while this value
	// must remain tied to the candidate being persisted.
	worker.inputLockVersion = inputLockVersion
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
	defer func() {
		if cleanupErr := closePreparedItems(prepared.items); cleanupErr != nil {
			result.err = errors.Join(result.err, fmt.Errorf("failed to clean up prepared package workspaces: %w", cleanupErr))
			if result.code == "" {
				result.code = "sync_failed"
			}
		}
	}()
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
	if worker.inputLockVersion == lockFileVersionV1 {
		if !worker.send(ctx, syncLockMigratedMsg{
			fromVersion: lockFileVersionV1,
			toVersion:   lockFileVersion,
		}) {
			return fail("sync_failed", ctx.Err())
		}
	}
	result.lock = prepared.lock

	executor, err := s.newPackageExecutor()
	if err != nil {
		return fail("sync_failed", err)
	}
	for i := range prepared.items {
		if err := ctx.Err(); err != nil {
			return fail("sync_failed", err)
		}
		item := &prepared.items[i]
		ensured, err := executor.ensurePreparedPackage(ctx, item)
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

func closePreparedItems(items []installItem) error {
	var closeErr error
	for i := range items {
		if items[i].Archive != nil {
			if err := items[i].Archive.Close(); err != nil {
				closeErr = errors.Join(closeErr, fmt.Errorf("could not close downloaded artifact: %w", err))
			}
			items[i].Archive = nil
		}
		if items[i].Validation != nil && items[i].Validation.Prepared != nil {
			if err := items[i].Validation.Prepared.Close(); err != nil {
				closeErr = errors.Join(closeErr, fmt.Errorf("could not close prepared package: %w", err))
			}
			items[i].Validation.Prepared = nil
		}
	}
	return closeErr
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
	if _, err := item.selectedArtifact(); err != nil {
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
		!sourceresolution.SameReleaseVersion(sourceidentity.Kind(candidate.Source.Type), item.LockEntry.Version, candidate.Version) {
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
	if err != nil || item.AlreadyInstalled != nil || item.Validation == nil || item.Validation.Prepared == nil ||
		!item.Validation.Prepared.MatchesExpected(item.Expected) ||
		selected.Hash == "" || selected.Size == nil || *selected.Size <= 0 || item.ValidatedLibraryHash == "" {
		return false
	}
	if validateLegacyLibraryHash(item.ValidatedLibraryHash) != nil ||
		item.Expected.ID != item.Release.DriverID || item.Expected.Version != item.Release.Version ||
		item.Expected.Platform == "" || item.Expected.Platform != item.Platform ||
		item.Expected.SourceType != item.Release.Source.Type || item.Expected.SourceIdentity != item.Release.Source.Reference ||
		item.Expected.ArchiveHash != selected.Hash || item.Expected.ArchiveSize != *selected.Size {
		return false
	}
	return true
}
