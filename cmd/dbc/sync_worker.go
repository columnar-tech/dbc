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
	"fmt"
	"sync"

	"charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc/config"
)

type syncWorkerHooks struct {
	duringPrepare       func(context.Context, int, installItem) error
	beforeCandidateSave func(context.Context) error
	ensurePackage       func(context.Context, config.Config, string, config.ExpectedPackageMetadata, config.EnsurePackageCallbacks) (config.EnsurePackageResult, error)
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
	// inputLockVersion belongs to the worker that owns the sync transaction.
	// Keeping it here avoids relying on a copied tea model after the worker has
	// read the input lock and before it persists the v2 candidate.
	inputLockVersion int
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

type syncLockMigratedMsg struct {
	fromVersion int
	toVersion   int
}

type syncResolvingMsg struct{ driver string }

type syncItemsMsg struct{ items []installItem }

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
