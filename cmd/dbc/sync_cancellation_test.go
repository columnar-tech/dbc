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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/columnar-tech/dbc/internal/jsonschema"
)

type syncProgramRun struct {
	program *tea.Program
	done    chan struct{}
	model   tea.Model
	err     error
	output  bytes.Buffer
}

func startSyncProgram(model syncModel) *syncProgramRun {
	return startSyncProgramWithContext(model, nil)
}

func startSyncProgramWithContext(model syncModel, ctx context.Context) *syncProgramRun {
	run := &syncProgramRun{done: make(chan struct{})}
	model.jsonOut = &run.output
	options := []tea.ProgramOption{tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithFilter(filterProgramMessage)}
	if ctx != nil {
		options = append(options, tea.WithContext(ctx))
	}
	run.program = tea.NewProgram(model, options...)
	go func() {
		run.model, run.err = run.program.Run()
		notifyProgramExited(model)
		run.program.Wait()
		close(run.done)
	}()
	return run
}

func waitSyncTestSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for sync worker test signal")
	}
}

func waitSyncProgram(t *testing.T, run *syncProgramRun) syncModel {
	t.Helper()
	select {
	case <-run.done:
	case <-time.After(5 * time.Second):
		run.program.Send(tea.InterruptMsg{})
		t.Fatal("timed out waiting for sync program to finish")
	}
	if run.err != nil {
		t.Fatalf("sync program returned an error: %v", run.err)
	}
	model, ok := run.model.(syncModel)
	if !ok {
		t.Fatalf("unexpected sync model result %T", run.model)
	}
	return model
}

func (suite *SubcommandTestSuite) TestSyncCancellationWaitsForWorkerCleanup() {
	for _, stage := range []string{"registry", "download", "prepare", "candidate-save", "install"} {
		suite.Run(stage, func() {
			tmp := suite.T().TempDir()
			path := filepath.Join(tmp, "dbc.toml")
			lockPath := filepath.Join(tmp, "dbc.lock")
			projectLockPath := filepath.Join(tmp, ".dbc.project.lock")
			suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0600))
			suite.Require().NoError(writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion}))
			oldLock, err := os.ReadFile(lockPath)
			suite.Require().NoError(err)

			entered := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			var downloadedArchive *os.File
			var preparedPayload *config.PreparedPackage
			model := SyncCmd{Path: path, NoVerify: true, Json: stage != "prepare", JsonStreamProgress: stage == "prepare"}.GetModelCustom(baseModel{
				getDriverRegistry: func() ([]dbc.Driver, error) {
					if stage == "registry" {
						close(entered)
						<-release
					}
					return getTestDriverRegistry()
				},
				downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
					downloadedArchive, err = downloadTestPkg(pkg)
					if stage == "download" {
						close(entered)
						<-release
					}
					return downloadedArchive, err
				},
			}).(syncModel)
			model.worker.hooks.duringPrepare = func(_ context.Context, _ int, item installItem) error {
				if item.Validation != nil {
					preparedPayload = item.Validation.Prepared
				}
				return nil
			}
			block := func(context.Context) error {
				close(entered)
				<-release
				return nil
			}
			switch stage {
			case "prepare":
				model.worker.hooks.duringPrepare = func(ctx context.Context, _ int, item installItem) error {
					if item.Validation != nil {
						preparedPayload = item.Validation.Prepared
					}
					return block(ctx)
				}
			case "candidate-save":
				model.worker.hooks.beforeCandidateSave = block
			case "install":
				model.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
					preparedPayload, err = callbacks.Prepare(ctx)
					if err != nil {
						return config.EnsurePackageResult{}, err
					}
					close(entered)
					<-release
					return config.EnsurePackageResult{}, ctx.Err()
				}
			}

			run := startSyncProgram(model)
			waitSyncTestSignal(suite.T(), entered)
			run.program.Send(tea.InterruptMsg{})
			waitSyncTestSignal(suite.T(), model.worker.ctx.Done())
			select {
			case <-run.done:
				suite.FailNow("program exited before the worker completed")
			default:
			}
			select {
			case <-model.worker.done:
				suite.FailNow("worker exited while its test hook was blocked")
			default:
			}
			var statErr error
			switch stage {
			case "registry":
				suite.Nil(downloadedArchive)
			case "download":
				_, statErr = downloadedArchive.Stat()
				suite.NoError(statErr, "source response remains open while download-stage cancellation is blocked")
			case "prepare", "candidate-save", "install":
				suite.NotNil(preparedPayload, "package staging completed before the cancellation hook")
			}
			lockCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			_, lockErr := fslock.AcquireContext(lockCtx, projectLockPath)
			cancel()
			suite.ErrorIs(lockErr, context.DeadlineExceeded, "project lock must stay held until the worker exits")

			unblock()
			result := waitSyncProgram(suite.T(), run)
			suite.Equal(1, result.Status())
			suite.ErrorIs(result.Err(), context.Canceled)
			suite.Empty(result.FinalOutput())
			var lines []string
			for _, line := range strings.Split(strings.TrimSpace(run.output.String()), "\n") {
				if line != "" {
					lines = append(lines, line)
				}
			}
			errorEnvelopes := 0
			for _, line := range lines {
				var envelope jsonschema.Envelope
				suite.NoError(json.Unmarshal([]byte(line), &envelope), "each JSON progress line must be valid")
				if envelope.Kind != "error" {
					suite.NotEqual("sync.status", envelope.Kind, "cancel must not produce successful final output")
					continue
				}
				errorEnvelopes++
				var response jsonschema.ErrorResponse
				suite.NoError(json.Unmarshal(envelope.Payload, &response))
				suite.Equal("sync_failed", response.Code)
				suite.Equal(context.Canceled.Error(), response.Message)
			}
			suite.Equal(1, errorEnvelopes, "cancel should emit one terminal JSON error envelope")
			waitSyncTestSignal(suite.T(), model.worker.done)
			if stage == "download" {
				assertFileClosed(suite.T(), downloadedArchive)
			}
			if preparedPayload != nil {
				suite.NoError(preparedPayload.Close(), "prepared payload should already have been closed by worker cleanup")
			}
			assertNoPreparedWorkspaces(suite.T(), suite.Dir())
			projectLock, lockErr := fslock.Acquire(projectLockPath, time.Second)
			suite.NoError(lockErr)
			if lockErr == nil {
				suite.NoError(projectLock.Release())
			}
			newLock, err := os.ReadFile(lockPath)
			suite.Require().NoError(err)
			if stage == "install" {
				suite.NotEqual(oldLock, newLock, "candidate lock remains after execution begins")
			} else {
				suite.Equal(oldLock, newLock, "cancellation before candidate save preserves old lock")
			}
			_, err = config.GetDriver(config.Config{Level: config.ConfigEnv, Location: suite.Dir()}, "test-driver-1")
			suite.Error(err, "test install hook must prevent runtime installation")
		})
	}
}

func (suite *SubcommandTestSuite) TestSyncProgramContextCancellationJoinsWorker() {
	tmp := suite.T().TempDir()
	path := filepath.Join(tmp, "dbc.toml")
	projectLockPath := filepath.Join(tmp, ".dbc.project.lock")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0600))
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var archive *os.File
	model := SyncCmd{Path: path, NoVerify: true, Json: true}.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			var err error
			archive, err = downloadTestPkg(pkg)
			close(entered)
			<-release // Deliberately contextless, matching the downloader contract.
			return archive, err
		},
	}).(syncModel)
	programCtx, cancelProgram := context.WithCancel(context.Background())
	run := startSyncProgramWithContext(model, programCtx)
	waitSyncTestSignal(suite.T(), entered)
	cancelProgram()
	waitSyncTestSignal(suite.T(), model.worker.ctx.Done())
	select {
	case <-run.done:
		suite.FailNow("runner must join the worker before returning")
	default:
	}
	suite.NotNil(archive)
	_, err := archive.Stat()
	suite.NoError(err, "archive must remain owned until the blocked downloader returns")
	lockCtx, cancelLock := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, lockErr := fslock.AcquireContext(lockCtx, projectLockPath)
	cancelLock()
	suite.ErrorIs(lockErr, context.DeadlineExceeded, "project lock must remain held during cancellation cleanup")

	unblock()
	waitSyncTestSignal(suite.T(), run.done)
	waitSyncTestSignal(suite.T(), model.worker.done)
	suite.ErrorIs(run.err, context.Canceled)
	assertFileClosed(suite.T(), archive)
	projectLock, err := fslock.Acquire(projectLockPath, time.Second)
	suite.NoError(err)
	if err == nil {
		suite.NoError(projectLock.Release())
	}
}

func (suite *SubcommandTestSuite) TestSyncCancellationWhileWaitingForProjectLock() {
	tmp := suite.T().TempDir()
	path := filepath.Join(tmp, "dbc.toml")
	projectLockPath := filepath.Join(tmp, ".dbc.project.lock")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0600))
	holder, err := fslock.Acquire(projectLockPath, time.Second)
	suite.Require().NoError(err)
	var holderReleaseOnce sync.Once
	releaseHolder := func() { holderReleaseOnce.Do(func() { _ = holder.Release() }) }
	defer releaseHolder()
	registryCalls, downloadCalls := atomic.Int32{}, atomic.Int32{}
	model := SyncCmd{Path: path, NoVerify: true, Json: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			registryCalls.Add(1)
			return getTestDriverRegistry()
		},
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloadCalls.Add(1)
			return downloadTestPkg(pkg)
		},
	}).(syncModel)
	run := startSyncProgram(model)
	deadline := time.After(2 * time.Second)
	for !model.worker.hasStarted() {
		select {
		case <-deadline:
			suite.FailNow("sync worker did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	time.Sleep(100 * time.Millisecond)
	run.program.Send(tea.InterruptMsg{})
	result := waitSyncProgram(suite.T(), run)
	suite.Equal(1, result.Status())
	suite.ErrorIs(result.Err(), context.Canceled)
	suite.Zero(registryCalls.Load(), "registry discovery must wait for project lock acquisition")
	suite.Zero(downloadCalls.Load())
	waitSyncTestSignal(suite.T(), model.worker.done)
	releaseHolder()
	projectLock, err := fslock.Acquire(projectLockPath, time.Second)
	suite.NoError(err, "canceled lock waiter must not retain the project lock")
	if err == nil {
		suite.NoError(projectLock.Release())
	}
}

func (suite *SubcommandTestSuite) TestSyncSerializesSameProjectUntilWorkerFinishes() {
	tmp := suite.T().TempDir()
	path := filepath.Join(tmp, "dbc.toml")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0600))
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var registryCalls atomic.Int32
	first := SyncCmd{Path: path, NoVerify: true, Json: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			registryCalls.Add(1)
			return getTestDriverRegistry()
		},
		downloadPkg: downloadTestPkg,
	}).(syncModel)
	first.worker.hooks.beforeCandidateSave = func(context.Context) error {
		close(entered)
		<-release
		return nil
	}
	firstRun := startSyncProgram(first)
	waitSyncTestSignal(suite.T(), entered)

	secondDownloads, secondInstalls := atomic.Int32{}, atomic.Int32{}
	second := SyncCmd{Path: path, NoVerify: true, Json: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			registryCalls.Add(1)
			return getTestDriverRegistry()
		},
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			secondDownloads.Add(1)
			return downloadTestPkg(pkg)
		},
	}).(syncModel)
	second.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
		result, err := config.EnsurePackage(ctx, cfg, driver, expected, callbacks)
		if result.Manifest != nil {
			secondInstalls.Add(1)
		}
		return result, err
	}
	secondRun := startSyncProgram(second)
	deadline := time.After(2 * time.Second)
	for !second.worker.hasStarted() {
		select {
		case <-deadline:
			suite.FailNow("second sync did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	time.Sleep(150 * time.Millisecond)
	suite.Equal(int32(1), registryCalls.Load(), "second sync must wait before registry discovery")
	select {
	case <-secondRun.done:
		suite.FailNow("second sync exited before acquiring the project lock")
	default:
	}

	unblock()
	firstResult := waitSyncProgram(suite.T(), firstRun)
	secondResult := waitSyncProgram(suite.T(), secondRun)
	suite.Equal(0, firstResult.Status())
	suite.Equal(0, secondResult.Status())
	suite.Equal(int32(1), registryCalls.Load(), "candidate lock should let the second sync avoid discovery")
	suite.Len(secondResult.skippedDrivers, 1, "runtime state must be reloaded after the second sync acquires the lock")
	suite.Empty(secondResult.newlyInstalled)
	suite.Zero(secondDownloads.Load(), "the second sync should reuse the installed exact artifact")
	suite.Zero(secondInstalls.Load(), "the second sync should skip the already-installed driver")
}

func (suite *SubcommandTestSuite) copyArchiveForSyncTest(source string) string {
	suite.T().Helper()
	input, err := os.Open(source)
	suite.Require().NoError(err)
	defer input.Close()
	output, err := os.CreateTemp(suite.tempdir, "sync-archive-*.tar.gz")
	suite.Require().NoError(err)
	_, err = io.Copy(output, input)
	suite.Require().NoError(err)
	suite.Require().NoError(output.Close())
	return output.Name()
}

type syncInjectedMessageModel struct {
	model   syncModel
	message tea.Msg
	result  *syncModel
}

func (m syncInjectedMessageModel) Init() tea.Cmd {
	return func() tea.Msg { return m.message }
}

func (m syncInjectedMessageModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.model.Update(msg)
	if next, ok := updated.(syncModel); ok {
		m.model = next
		if m.result != nil {
			*m.result = next
		}
		return m, cmd
	}
	return updated, cmd
}

func (m syncInjectedMessageModel) View() tea.View {
	return m.model.View()
}

func (m syncInjectedMessageModel) WithJSONWriter(w io.Writer) tea.Model {
	m.model.jsonOut = w
	return m
}

func (m syncInjectedMessageModel) Status() int { return m.model.Status() }
func (m syncInjectedMessageModel) Err() error  { return m.model.Err() }
func (m syncInjectedMessageModel) FinalOutput() string {
	return m.model.FinalOutput()
}
func (m syncInjectedMessageModel) IsJSONMode() bool { return m.model.IsJSONMode() }

func (suite *SubcommandTestSuite) TestSyncTerminalErrorsUseSingleOutputContract() {
	for _, mode := range []string{"plain", "json", "json-stream"} {
		suite.Run(mode, func() {
			wantErr := errors.New("injected sync failure")
			const code = "sync_failed"
			message := syncWorkerResultMsg{err: wantErr, code: code}
			jsonOutput := mode != "plain"
			var result syncModel
			model := syncInjectedMessageModel{
				model: syncModel{
					jsonOutput:         jsonOutput,
					jsonStreamProgress: mode == "json-stream",
				},
				message: message,
				result:  &result,
			}
			output := suite.runCmdErr(model)
			suite.Equal(1, result.Status())
			suite.EqualError(result.Err(), wantErr.Error())
			suite.Empty(result.FinalOutput(), "failure must not produce a success sync.status envelope")

			if !jsonOutput {
				suite.Equal("\nError: "+wantErr.Error(), output)
				suite.Equal(1, strings.Count(output, wantErr.Error()), "terminal error should be reported once")
				return
			}

			suite.NotContains(output, "Error:", "JSON output must not contain plaintext error formatting")
			lines := strings.Split(strings.TrimSpace(output), "\n")
			suite.Len(lines, 1, "failure should produce exactly one terminal JSON envelope")
			var envelope jsonschema.Envelope
			suite.NoError(json.Unmarshal([]byte(lines[0]), &envelope))
			suite.Equal("error", envelope.Kind)
			var response jsonschema.ErrorResponse
			suite.NoError(json.Unmarshal(envelope.Payload, &response))
			suite.Equal(code, response.Code)
			suite.Equal(wantErr.Error(), response.Message)
		})
	}
}
