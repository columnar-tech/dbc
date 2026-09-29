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

package config

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"
)

var errDriverRegistrationChanged = errors.New("driver registration changed")

// driverInstallLock serializes driver mutations that acquire it for a specific
// installation location.
type driverInstallLock struct {
	releaseFn   func() error
	releaseOnce sync.Once
	releaseErr  error
}

// release releases the driver lock.
func (l *driverInstallLock) release() error {
	if l == nil || l.releaseFn == nil {
		return nil
	}
	l.releaseOnce.Do(func() { l.releaseErr = l.releaseFn() })
	return l.releaseErr
}

// acquireDriverInstallLockWith acquires the lock identified by location and
// runtimeID. The lock file is stored directly under location.
func acquireDriverInstallLockWith(ctx context.Context, location, runtimeID string, timeout time.Duration) (*driverInstallLock, error) {
	if location == "" {
		return nil, errors.New("driver lock location is empty")
	}
	if runtimeID == "" {
		return nil, errors.New("driver lock runtime ID is empty")
	}
	lockPath, err := driverInstallLockPath(location, runtimeID)
	if err != nil {
		return nil, err
	}
	return acquireDriverInstallLock(ctx, lockPath, timeout)
}

func driverInstallLockPath(location, runtimeID string) (string, error) {
	canonicalLocation, err := filepath.Abs(location)
	if err != nil {
		return "", fmt.Errorf("resolve driver lock location: %w", err)
	}
	canonicalLocation = filepath.Clean(canonicalLocation)
	canonicalID := runtimeID
	if runtime.GOOS == "windows" {
		canonicalLocation = strings.ToLower(canonicalLocation)
		canonicalID = strings.ToLower(canonicalID)
	}
	key := sha256.Sum256([]byte(canonicalID))
	return filepath.Join(canonicalLocation, fmt.Sprintf(".dbc.install.%x.lock", key)), nil
}

func UninstallDriver(cfg Config, info DriverInfo) (err error) {
	location, err := uninstallLockLocation(cfg, info)
	if err != nil {
		return err
	}
	if err := prepareDriverUninstallLockLocation(cfg, location); err != nil {
		return fmt.Errorf("prepare driver uninstall lock location: %w", err)
	}
	lock, err := acquireDriverInstallLockWith(context.Background(), location, info.ID, 10*time.Second)
	if err != nil {
		return fmt.Errorf("acquire driver uninstall lock: %w", err)
	}
	defer func() { err = errors.Join(err, lock.release()) }()

	current, err := readDriverRegistrationForUninstall(cfg, info)
	if err != nil {
		return fmt.Errorf("re-read driver registration before uninstall: %w", err)
	}
	if !sameDriverRegistration(info, current) {
		return fmt.Errorf("driver %q changed since it was selected; refusing to uninstall: %w", info.ID, errDriverRegistrationChanged)
	}
	return uninstallDriverUnlocked(cfg, current)
}

func sameDriverRegistration(a, b DriverInfo) bool {
	return a.ID == b.ID && canonicalRegistrationPath(a.FilePath) == canonicalRegistrationPath(b.FilePath) &&
		a.Name == b.Name && a.Publisher == b.Publisher && a.License == b.License &&
		sameVersion(a.Version, b.Version) && a.Source == b.Source &&
		sameVersion(a.AdbcInfo.Version, b.AdbcInfo.Version) &&
		reflect.DeepEqual(a.AdbcInfo.Features, b.AdbcInfo.Features) &&
		a.Driver.Entrypoint == b.Driver.Entrypoint && sameDriverMap(a.Driver.Shared, b.Driver.Shared)
}

func sameVersion(a, b *semver.Version) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.String() == b.String()
}

func canonicalRegistrationPath(path string) string {
	if path == "" {
		return ""
	}
	clean, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		clean = filepath.Clean(path)
	}
	if runtime.GOOS == "windows" {
		clean = strings.ToLower(clean)
	}
	return clean
}

func sameDriverMap(a, b driverMap) bool {
	if a.defaultPath != b.defaultPath || len(a.platformMap) != len(b.platformMap) {
		return false
	}
	for platform, path := range a.platformMap {
		if b.platformMap[platform] != path {
			return false
		}
	}
	return true
}
