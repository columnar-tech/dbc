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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/fslock"
)

const projectLockWait = 5 * time.Second

func assertProjectLockHeld(suite *SubcommandTestSuite, path string) {
	suite.T().Helper()
	lock, err := fslock.AcquireContext(context.Background(), path, 0)
	suite.ErrorIs(err, fslock.ErrLockContended, "project lock should remain held")
	if err == nil {
		_ = lock.Release()
	}
}

func acquireProjectLock(suite *SubcommandTestSuite, path string) {
	suite.T().Helper()
	ctx, cancel := context.WithTimeout(context.Background(), projectLockWait)
	defer cancel()
	lock, err := fslock.AcquireContext(ctx, path, projectLockWait)
	suite.Require().NoError(err, "project lock should be released after its worker returns")
	suite.Require().NoError(lock.Release())
}

func waitForProjectLockState(suite *SubcommandTestSuite, projectLock *syncProjectLock, workers int) {
	suite.T().Helper()
	suite.Eventually(func() bool {
		projectLock.mu.Lock()
		defer projectLock.mu.Unlock()
		return projectLock.closed && projectLock.workers == workers
	}, projectLockWait, 10*time.Millisecond, "project lock reaches the expected worker state")
}

func (suite *SubcommandTestSuite) TestSyncProjectLockWaitsForPrepareWorkerAfterQuit() {
	listPath := suite.writeDriverList("test-driver-1")
	base := testBaseModel()
	enteredDownload := make(chan struct{})
	continueDownload := make(chan struct{})
	var releaseDownload sync.Once
	allowDownloadReturn := func() { releaseDownload.Do(func() { close(continueDownload) }) }
	suite.T().Cleanup(allowDownloadReturn)
	base.downloadPkg = func(dbc.PkgInfo) (*os.File, error) {
		close(enteredDownload)
		<-continueDownload
		return nil, context.Canceled
	}

	model := SyncCmd{Path: listPath, Level: suite.configLevel}.newModel(base)
	suite.T().Cleanup(model.cancel)
	lockPath := filepath.Join(suite.tempdir, ".dbc.project.lock")
	type prepareResult struct {
		err error
	}
	finished := make(chan prepareResult, 1)
	go func() {
		_, err := model.prepare()
		finished <- prepareResult{err: err}
	}()

	select {
	case <-enteredDownload:
	case <-time.After(projectLockWait):
		allowDownloadReturn()
		suite.FailNow("prepare worker did not enter the blocked download")
	}

	model.Update(quitKey)
	waitForProjectLockState(suite, model.projectLock, 1)
	assertProjectLockHeld(suite, lockPath)
	allowDownloadReturn()
	select {
	case result := <-finished:
		suite.ErrorIs(result.err, context.Canceled)
	case <-time.After(projectLockWait):
		suite.FailNow("prepare worker did not return after its download was released")
	}
	waitForProjectLockState(suite, model.projectLock, 0)
	acquireProjectLock(suite, lockPath)
}

func (suite *SubcommandTestSuite) TestSyncProjectLockWaitsForInstallWorkerAfterQuit() {
	for _, tc := range []struct {
		name       string
		installErr error
	}{
		{name: "late success"},
		{name: "late error", installErr: context.Canceled},
	} {
		suite.Run(tc.name, func() {
			projectDir := filepath.Join(suite.tempdir, strings.ReplaceAll(tc.name, " ", "-"))
			suite.Require().NoError(os.MkdirAll(projectDir, 0o755))
			listPath := filepath.Join(projectDir, "dbc.toml")
			suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
			model := SyncCmd{Path: listPath, Level: suite.configLevel, NoVerify: true}.newModel(testBaseModel())
			suite.T().Cleanup(model.cancel)
			items, err := model.prepare()
			suite.Require().NoError(err)
			suite.Require().Len(items, 1)
			suite.T().Cleanup(func() {
				for _, item := range items {
					removeDownload(item.File)
				}
			})

			enteredInstall := make(chan struct{})
			continueInstall := make(chan struct{})
			var releaseInstall sync.Once
			allowInstallReturn := func() { releaseInstall.Do(func() { close(continueInstall) }) }
			suite.T().Cleanup(allowInstallReturn)
			model.installPackage = func(context.Context, config.Config, string, *os.File, config.InstallPackageOptions) (config.Manifest, error) {
				close(enteredInstall)
				<-continueInstall
				return config.Manifest{}, tc.installErr
			}

			cmd := model.installDriver(model.cfg, items[0])
			suite.Nil(cmd())
			select {
			case <-enteredInstall:
			case <-time.After(projectLockWait):
				allowInstallReturn()
				suite.FailNow("install worker did not enter the blocked install")
			}

			model.Update(quitKey)
			lockPath := filepath.Join(projectDir, ".dbc.project.lock")
			waitForProjectLockState(suite, model.projectLock, 1)
			assertProjectLockHeld(suite, lockPath)
			allowInstallReturn()
			waitForProjectLockState(suite, model.projectLock, 0)
			acquireProjectLock(suite, lockPath)
		})
	}
}

func (suite *SubcommandTestSuite) TestSyncProjectLockReleasedWhenPreparedModelQuitsIdle() {
	listPath := suite.writeDriverList("test-driver-1")
	model := SyncCmd{Path: listPath, Level: suite.configLevel}.newModel(testBaseModel())
	suite.T().Cleanup(model.cancel)
	items, err := model.prepare()
	suite.Require().NoError(err)
	suite.Require().Len(items, 1)
	suite.T().Cleanup(func() {
		for _, item := range items {
			removeDownload(item.File)
		}
	})

	model.Update(quitKey)
	lockPath := filepath.Join(suite.tempdir, ".dbc.project.lock")
	waitForProjectLockState(suite, model.projectLock, 0)
	acquireProjectLock(suite, lockPath)
}
