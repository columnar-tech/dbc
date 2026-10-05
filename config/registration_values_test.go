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
	"errors"
	"reflect"
	"strings"
	"testing"
)

type memoryRegistrationStore struct {
	values      map[string]registrationValue
	failWrite   map[string]error
	failDelete  map[string]error
	failRestore map[string]error
	writeCalls  map[string]int
	deleteCalls map[string]int
}

func (s *memoryRegistrationStore) read(name string) (registrationValue, error) {
	value, ok := s.values[name]
	value.exists = ok
	return value, nil
}

func (s *memoryRegistrationStore) write(name string, value registrationValue) error {
	if s.writeCalls == nil {
		s.writeCalls = make(map[string]int)
	}
	s.writeCalls[name]++
	s.values[name] = registrationValue{exists: true, kind: value.kind, data: append([]byte(nil), value.data...)}
	if err := s.failWrite[name]; err != nil {
		delete(s.failWrite, name)
		return err
	}
	if s.writeCalls[name] > 1 && s.failRestore != nil {
		return s.failRestore[name]
	}
	return nil
}

func (s *memoryRegistrationStore) delete(name string) error {
	if s.deleteCalls == nil {
		s.deleteCalls = make(map[string]int)
	}
	s.deleteCalls[name]++
	delete(s.values, name)
	if err := s.failDelete[name]; err != nil {
		delete(s.failDelete, name)
		return err
	}
	if s.deleteCalls[name] > 1 && s.failRestore != nil {
		return s.failRestore[name]
	}
	return nil
}

func TestUpdateRegistrationValuesRestoresRawValuesAfterEveryWriteFailure(t *testing.T) {
	changes := []registrationValueChange{
		{name: "name", value: registrationValue{exists: true, kind: 1, data: []byte("new-name\x00")}},
		{name: "version", value: registrationValue{exists: true, kind: 4, data: []byte{9, 8, 7, 6}}},
		{name: "entrypoint", value: registrationValue{}},
	}
	initial := map[string]registrationValue{
		"name":       {exists: true, kind: 7, data: []byte{0xff, 0x00, 0x80}},
		"version":    {exists: true, kind: 3, data: []byte{1, 2, 3}},
		"entrypoint": {exists: true, kind: 1, data: []byte("old-entry\x00")},
	}

	for _, tc := range []struct {
		name       string
		failWrite  bool
		failDelete bool
	}{
		{name: "name", failWrite: true},
		{name: "version", failWrite: true},
		{name: "entrypoint", failDelete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantErr := errors.New("registry write failed")
			store := &memoryRegistrationStore{
				values:      cloneRegistrationValues(initial),
				failWrite:   make(map[string]error),
				failDelete:  make(map[string]error),
				failRestore: make(map[string]error),
			}
			if tc.failWrite {
				store.failWrite[tc.name] = wantErr
			}
			if tc.failDelete {
				store.failDelete[tc.name] = wantErr
			}
			err := updateRegistrationValues(store, changes)
			if !errors.Is(err, wantErr) {
				t.Fatalf("update error = %v, want wrapped failure", err)
			}
			if strings.Contains(err.Error(), "rollback registration values") {
				t.Fatalf("successful rollback was reported as failed: %v", err)
			}
			if errors.Is(err, errRegistrationRollbackFailed) {
				t.Fatalf("successful rollback has rollback failure marker: %v", err)
			}
			if !reflect.DeepEqual(store.values, initial) {
				t.Fatalf("values after rollback = %#v, want %#v", store.values, initial)
			}
		})
	}
}

func TestRegistryKeyCreationDispositionAndCleanupErrors(t *testing.T) {
	if !registryKeyWasCreated(false) || registryKeyWasCreated(true) {
		t.Fatal("registry CreateKey disposition was interpreted incorrectly")
	}

	primaryErr := errors.New("create child failed")
	cleanupErr := errors.New("remove newly created parent failed")
	cleaned := false
	err := rollbackCreatedRegistryKeys(primaryErr, func() error {
		cleaned = true
		return cleanupErr
	})
	if !cleaned || !errors.Is(err, primaryErr) || !errors.Is(err, cleanupErr) || !errors.Is(err, errRegistrationRollbackFailed) {
		t.Fatalf("rollback error = %v, cleanup called = %v", err, cleaned)
	}
}

func TestUpdateRegistrationValuesReportsRollbackFailures(t *testing.T) {
	writeErr := errors.New("registry write failed")
	valueRollbackErr := errors.New("value rollback failed")
	keyRollbackErr := errors.New("key rollback failed")
	store := &memoryRegistrationStore{
		values:      map[string]registrationValue{"name": {exists: true, kind: 7, data: []byte{1}}},
		failWrite:   map[string]error{"name": writeErr},
		failRestore: map[string]error{"name": valueRollbackErr},
	}
	keyRolledBack := false
	err := updateRegistrationValuesWithRollback(store, []registrationValueChange{
		{name: "name", value: registrationValue{exists: true, kind: 1, data: []byte("new\x00")}},
	}, func() error {
		keyRolledBack = true
		return keyRollbackErr
	})
	if !errors.Is(err, writeErr) || !errors.Is(err, valueRollbackErr) || !errors.Is(err, keyRollbackErr) || !errors.Is(err, errRegistrationRollbackFailed) {
		t.Fatalf("update error = %v, want primary and rollback errors", err)
	}
	var typedErr *registrationRollbackError
	if !errors.As(err, &typedErr) {
		t.Fatalf("update error type = %T, want *registrationRollbackError", err)
	}
	if !keyRolledBack {
		t.Fatal("new key rollback was not attempted")
	}
}

func TestUpdateRegistrationValuesSnapshotsBeforeWriting(t *testing.T) {
	readErr := errors.New("registry read failed")
	store := &failingReadRegistrationStore{
		memoryRegistrationStore: &memoryRegistrationStore{
			values: map[string]registrationValue{},
		},
		failName: "second",
		failErr:  readErr,
	}
	err := updateRegistrationValues(store, []registrationValueChange{
		{name: "first", value: registrationValue{exists: true, kind: 1, data: []byte("new\x00")}},
		{name: "second", value: registrationValue{exists: true, kind: 1, data: []byte("new\x00")}},
	})
	if !errors.Is(err, readErr) {
		t.Fatalf("update error = %v, want read failure", err)
	}
	if len(store.values) != 0 {
		t.Fatalf("writes occurred before all snapshots were read: %#v", store.values)
	}
}

type failingReadRegistrationStore struct {
	*memoryRegistrationStore
	failName string
	failErr  error
}

func (s *failingReadRegistrationStore) read(name string) (registrationValue, error) {
	if name == s.failName {
		return registrationValue{}, s.failErr
	}
	return s.memoryRegistrationStore.read(name)
}

func cloneRegistrationValues(values map[string]registrationValue) map[string]registrationValue {
	cloned := make(map[string]registrationValue, len(values))
	for name, value := range values {
		value.data = append([]byte(nil), value.data...)
		cloned[name] = value
	}
	return cloned
}
