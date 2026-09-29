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

//go:build windows

package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"golang.org/x/sys/windows/registry"
)

func TestWindowsRegistrationStorePreservesRawValueTypeAndBytes(t *testing.T) {
	key, closeKey := createWindowsRegistrationTestKey(t, "raw")
	defer closeKey()
	store := windowsRegistrationValueStore{key: key}
	want := registrationValue{exists: true, kind: registry.BINARY, data: []byte{0xff, 0, 0x80, 0x7f}}
	if err := store.write("raw", want); err != nil {
		t.Fatal(err)
	}
	got, err := store.read("raw")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("raw value = %#v, want %#v", got, want)
	}
}

func TestWindowsRegistryReceiptFingerprintMatchesRoundTrip(t *testing.T) {
	cfg := Config{Level: ConfigUser, Location: t.TempDir()}
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	id := "dbc-receipt-" + hex.EncodeToString(suffix[:])
	generation := testPackageGenerationPath(t, cfg.Location, id, "generation")
	stage := t.TempDir()
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "driver.dll"), []byte("library"), 0o600); err != nil {
		t.Fatal(err)
	}
	driver := windowsRegistrationTestDriver()
	driver.ID = id
	driver.Version = semver.MustParse("1.2.3")
	driver.AdbcInfo.Version = semver.MustParse("1.0.0")
	driver.AdbcInfo.Features.Supported = []string{"transactions", "bulk_ingest"}
	driver.AdbcInfo.Features.Unsupported = []string{"substrait"}
	driver.Driver.Shared.Set(PlatformTuple(), filepath.Join(generation, "driver.dll"))
	manifest := Manifest{DriverInfo: driver}
	manifest.Files.Driver = "driver.dll"
	receipt, err := makePackageInstallReceipt(cfg, stage, filepath.Base(generation), id, PlatformTuple(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.RegistrationFingerprintValue == "" {
		t.Fatal("receipt has no registration fingerprint")
	}
	if registrationFingerprintIncludesADBC(cfg) {
		t.Fatal("registry fingerprint projection includes unpersisted ADBC metadata")
	}
	if err := createRuntimeRegistrationUnlocked(cfg, generation, driver); err != nil {
		t.Fatal(err)
	}
	driversKey, err := registry.OpenKey(registry.CURRENT_USER, regKeyADBC, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := registry.DeleteKey(driversKey, id); err != nil && !errors.Is(err, registry.ErrNotExist) {
			t.Errorf("delete test registration: %v", err)
		}
		if err := driversKey.Close(); err != nil {
			t.Errorf("close driver registry key: %v", err)
		}
	}()
	loaded, err := GetDriver(cfg, strings.ToUpper(id))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AdbcInfo.Version != nil || len(loaded.AdbcInfo.Features.Supported) != 0 || len(loaded.AdbcInfo.Features.Unsupported) != 0 {
		t.Fatalf("registry unexpectedly persisted ADBC metadata: %+v", loaded.AdbcInfo)
	}
	fingerprint, err := runtimeRegistrationFingerprint(cfg, receipt.RuntimeID, receipt.Platform, loaded, receipt.LibraryKind, receipt.OwnedLibraryFilename)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint != receipt.RegistrationFingerprintValue {
		t.Fatalf("round-trip fingerprint = %s, receipt = %s", fingerprint, receipt.RegistrationFingerprintValue)
	}
}

func TestWindowsRegistryRegistrationRejectsEmbeddedNULBeforeWriting(t *testing.T) {
	driver := windowsRegistrationTestDriver()
	driver.Name = "bad\x00name"
	if _, err := registryRegistrationChanges(driver); err == nil {
		t.Fatal("registry registration accepted an embedded NUL")
	}
}

func TestWindowsRegistryKeyHandleClosesOnlyOnce(t *testing.T) {
	closeCount := 0
	handle := &registryKeyHandle{closeFn: func() error {
		closeCount++
		return nil
	}}
	if err := handle.close(); err != nil {
		t.Fatal(err)
	}
	if err := handle.close(); err != nil {
		t.Fatalf("second close should be a no-op: %v", err)
	}
	if !handle.closed || closeCount != 1 {
		t.Fatalf("closed = %v, close count = %d; want true and 1", handle.closed, closeCount)
	}
}

func TestWindowsRegistrationUpdateRestoresRawValuesAndDeletesOptionalEntryPoint(t *testing.T) {
	key, closeKey := createWindowsRegistrationTestKey(t, "existing")
	defer closeKey()
	store := windowsRegistrationValueStore{key: key}
	oldVersion := registrationValue{exists: true, kind: registry.BINARY, data: []byte{0, 0xff, 4, 9}}
	oldEntrypoint := registrationValue{exists: true, kind: registry.EXPAND_SZ, data: []byte{0x41, 0, 0, 0, 0xff}}
	if err := store.write("version", oldVersion); err != nil {
		t.Fatal(err)
	}
	if err := store.write("entrypoint", oldEntrypoint); err != nil {
		t.Fatal(err)
	}

	driver := windowsRegistrationTestDriver()
	changes, err := registryRegistrationChanges(driver)
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("injected registry write failure")
	failing := &failingWindowsRegistrationStore{inner: store, name: "driver", err: wantErr}
	if err := updateRegistrationValues(failing, changes); !errors.Is(err, wantErr) {
		t.Fatalf("registration update error = %v, want injected failure", err)
	}
	gotVersion, err := store.read("version")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotVersion, oldVersion) {
		t.Fatalf("version after rollback = %#v, want raw prior value %#v", gotVersion, oldVersion)
	}
	gotEntrypoint, err := store.read("entrypoint")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotEntrypoint, oldEntrypoint) {
		t.Fatalf("entrypoint after rollback = %#v, want %#v", gotEntrypoint, oldEntrypoint)
	}

	if err := updateRegistrationValues(store, changes); err != nil {
		t.Fatal(err)
	}
	gotEntrypoint, err = store.read("entrypoint")
	if err != nil {
		t.Fatal(err)
	}
	if gotEntrypoint.exists {
		t.Fatalf("optional entrypoint remains after update: %#v", gotEntrypoint)
	}
}

func TestWindowsRegistrationUpdateDeletesNewDriverKeyAfterFailure(t *testing.T) {
	parent, closeParent := createWindowsRegistrationTestKey(t, "parent")
	defer closeParent()
	key, openedExisting, err := registry.CreateKey(parent, "driver", registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	if openedExisting {
		t.Fatal("test driver key unexpectedly existed")
	}
	handle := &registryKeyHandle{key: key}
	store := failingWindowsRegistrationStore{
		inner: windowsRegistrationValueStore{key: key},
		name:  "name",
		err:   errors.New("injected registry write failure"),
	}
	changes, err := registryRegistrationChanges(windowsRegistrationTestDriver())
	if err != nil {
		t.Fatal(err)
	}
	err = updateRegistrationValuesWithRollback(&store, changes, func() error {
		return deleteCreatedRegistryKey(parent, "driver", handle, true)
	})
	if !errors.Is(err, store.err) {
		t.Fatalf("registration update error = %v, want injected failure", err)
	}
	if !handle.closed {
		t.Fatal("new driver key handle was not closed during rollback")
	}
	if _, err := registry.OpenKey(parent, "driver", registry.QUERY_VALUE); !errors.Is(err, registry.ErrNotExist) {
		t.Fatalf("new driver key remains after rollback: %v", err)
	}
}

type failingWindowsRegistrationStore struct {
	inner  windowsRegistrationValueStore
	name   string
	err    error
	failed bool
}

func (s *failingWindowsRegistrationStore) read(name string) (registrationValue, error) {
	return s.inner.read(name)
}

func (s *failingWindowsRegistrationStore) write(name string, value registrationValue) error {
	if err := s.inner.write(name, value); err != nil {
		return err
	}
	if name == s.name && !s.failed {
		s.failed = true
		return s.err
	}
	return nil
}

func (s *failingWindowsRegistrationStore) delete(name string) error {
	return s.inner.delete(name)
}

func windowsRegistrationTestDriver() DriverInfo {
	driver := DriverInfo{
		ID:        "driver",
		Name:      "test driver",
		Publisher: "test publisher",
		License:   "MIT",
		Version:   semver.MustParse("1.2.3"),
		Source:    "external",
	}
	driver.Driver.Shared.Set(PlatformTuple(), "C:\\drivers\\driver.dll")
	return driver
}

func createWindowsRegistrationTestKey(t *testing.T, label string) (registry.Key, func()) {
	t.Helper()
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("Software\\dbc-registration-test-%s-%s", label, hex.EncodeToString(suffix[:]))
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	return key, func() {
		if err := key.Close(); err != nil {
			t.Errorf("close test registry key: %v", err)
		}
		if err := registry.DeleteKey(registry.CURRENT_USER, path); err != nil && !errors.Is(err, registry.ErrNotExist) {
			t.Errorf("delete test registry key: %v", err)
		}
	}
}
