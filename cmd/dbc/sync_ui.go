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
	"strings"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/columnar-tech/dbc/internal/jsonschema"
)

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
	case syncLockMigratedMsg:
		// The worker sends this only after the candidate v2 lock has been
		// persisted atomically. Keep it on the model for the final JSON status,
		// while streaming an explicit event only in progress mode.
		s.migration = &jsonschema.SyncMigration{
			FromVersion: msg.fromVersion,
			ToVersion:   msg.toVersion,
		}
		if s.jsonStreamProgress {
			s.emitJSON("sync.migration", *s.migration)
			return s, s.worker.nextEvent()
		}
		if s.jsonOutput {
			return s, s.worker.nextEvent()
		}
		return s, tea.Sequence(
			tea.Printf("%s", syncMigrationMessage(*s.migration)),
			s.worker.nextEvent(),
		)
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
