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
	"fmt"
	"io"
	"os"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
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
		Migration: s.migration,
	})
}

func syncMigrationMessage(migration jsonschema.SyncMigration) string {
	return fmt.Sprintf("Migrated dbc.lock from v%d to v%d. v2 records package archive hashes; verified v1 installed-library checksums are retained as legacy proofs where applicable.", migration.FromVersion, migration.ToVersion)
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
	// migration is set only after the worker has persisted a v2 candidate lock.
	migration *jsonschema.SyncMigration

	jsonOut  io.Writer
	worker   *syncWorker
	terminal bool
}
