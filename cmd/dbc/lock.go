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
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/columnar-tech/dbc/internal/resolution"
)

type LockCmd struct {
	Path     string `arg:"-p" placeholder:"FILE" default:"./dbc.toml" help:"Driver list to lock"`
	NoVerify bool   `arg:"--no-verify" help:"Allow locking drivers without a signature file"`
	Json     bool   `arg:"--json" help:"Print output as JSON instead of plaintext"`
}

func (c LockCmd) newModel(base baseModel) lockModel {
	ctx, cancel := context.WithCancel(context.Background())
	projectLock := &syncProjectLock{}
	// Cancellation requests release; an active worker keeps the lock until it
	// has finished all writes and cleanup.
	context.AfterFunc(ctx, projectLock.release)
	return lockModel{
		baseModel:   base,
		Path:        c.Path,
		NoVerify:    c.NoVerify,
		jsonOutput:  c.Json,
		ctx:         ctx,
		cancel:      cancel,
		projectLock: projectLock,
	}
}

func (c LockCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return c.newModel(baseModel)
}

func (c LockCmd) GetModel() tea.Model {
	return c.newModel(defaultBaseModel())
}

// lockModel resolves a driver list into its lock file without installing
// anything. It shares the resolution with syncModel.
type lockModel struct {
	baseModel

	// Path is the driver list path or directory.
	Path     string
	NoVerify bool

	jsonOutput bool

	ctx         context.Context
	cancel      context.CancelFunc
	projectLock *syncProjectLock

	locked          []jsonschema.LockedDriver
	lockPath        string
	jsonErrorOutput string
}

func (m lockModel) IsJSONMode() bool { return m.jsonOutput }

type lockDoneMsg struct {
	items    []syncItem
	lockPath string
}

func (m lockModel) Init() tea.Cmd {
	return func() tea.Msg {
		// The project lock is only needed while resolving.
		defer m.projectLock.release()
		items, warnings, err := driverListResolution{
			path:        m.Path,
			ctx:         m.ctx,
			projectLock: m.projectLock,
			base:        m.baseModel,
		}.resolve(m.fetch)
		for _, warning := range warnings {
			fmt.Fprintln(os.Stderr, warning)
		}
		if err != nil {
			return err
		}
		_, lockPath, err := driverListPaths(m.Path)
		if err != nil {
			return err
		}
		return lockDoneMsg{items: items, lockPath: lockPath}
	}
}

// fetch records the archive evidence of a planned driver without installing
// it. An archive is downloaded only when the registry published no hash for
// the current target or when a v1 library proof has to be checked. The
// download is verified like the one of dbc sync, and unless NoVerify is set
// its signature is checked with the key the lock pins before its hash is
// recorded. The download is always removed again.
func (m lockModel) fetch(pd *plannedDriver) (syncItem, error) {
	item := syncItem{Name: pd.Name, Package: pd.Package, LibrarySHA256: pd.LibrarySHA256, Signer: pd.Signer}
	if pd.Package.ArchiveSHA256 != "" && pd.LibrarySHA256 == "" {
		return item, nil
	}
	target, err := resolution.TargetFromPlatformTuple(pd.Package.PlatformTuple)
	if err != nil {
		return item, err
	}
	// Always extract the archive with the install-time safety checks, so an
	// archive that could not be installed is never pinned, even with NoVerify.
	f, err := downloadArchive(m.baseModel, pd, target, archiveChecks{
		extract:   true,
		signature: signatureVerifier(pd.Signer, m.NoVerify),
	})
	if err != nil {
		return item, err
	}
	removeDownload(f)
	item.Package = pd.Package
	return item, nil
}

func (m lockModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case lockDoneMsg:
		m.lockPath = msg.lockPath
		m.locked = make([]jsonschema.LockedDriver, 0, len(msg.items))
		for _, item := range msg.items {
			m.locked = append(m.locked, jsonschema.LockedDriver{Name: item.Name, Version: item.Package.Version.String()})
		}
		return m, tea.Quit
	case tea.KeyPressMsg:
		if isQuitKey(msg) {
			m.cancel()
		}
	case error:
		msg = redactURLSecrets(msg, nil)
		m.projectLock.release()
		m.status = 1
		m.err = msg
		if m.jsonOutput {
			code := "lock_failed"
			var archiveErr *config.ArchiveHashMismatchError
			var libraryErr *libraryChecksumError
			var sizeErr *archiveSizeMismatchError
			if errors.As(msg, &archiveErr) || errors.As(msg, &libraryErr) || errors.As(msg, &sizeErr) {
				code = "checksum_failed"
			}
			m.jsonErrorOutput = marshalEnvelope("error", jsonschema.ErrorResponse{
				Code:    code,
				Message: msg.Error(),
			})
			return m, tea.Quit
		}
	}

	bm, cmd := m.baseModel.Update(msg)
	m.baseModel = bm.(baseModel)
	return m, cmd
}

func (m lockModel) FinalOutput() string {
	if m.status != 0 {
		return m.jsonErrorOutput // empty string for non-JSON errors
	}
	if m.lockPath == "" {
		return ""
	}
	if m.jsonOutput {
		return marshalEnvelope("lock.status", jsonschema.LockStatus{
			LockFilePath: m.lockPath,
			Drivers:      m.locked,
		})
	}
	noun := "drivers"
	if len(m.locked) == 1 {
		noun = "driver"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Locked %d %s to %s", len(m.locked), noun, displayPath(m.lockPath))
	for _, d := range m.locked {
		fmt.Fprintf(&b, "\n%s %s-%s", checkMark, d.Name, d.Version)
	}
	return b.String()
}

// displayPath shows path relative to the working directory when it lies below it.
func displayPath(path string) string {
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return path
}
