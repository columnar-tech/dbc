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
	"fmt"
)

var errRegistrationRollbackFailed = errors.New("driver registration rollback failed")

type registrationRollbackError struct {
	primary  error
	rollback error
}

func (e *registrationRollbackError) Error() string {
	return fmt.Sprintf("%v; rollback failed: %v", e.primary, e.rollback)
}

func (e *registrationRollbackError) Unwrap() []error {
	return []error{e.primary, e.rollback, errRegistrationRollbackFailed}
}

func combineRegistrationRollbackErrors(primary, rollback error) error {
	if rollback == nil {
		return primary
	}
	return &registrationRollbackError{primary: primary, rollback: rollback}
}

type registrationValue struct {
	exists bool
	kind   uint32
	data   []byte
}

type registrationValueStore interface {
	read(string) (registrationValue, error)
	write(string, registrationValue) error
	delete(string) error
}

type registrationValueChange struct {
	name  string
	value registrationValue
}

func registryKeyWasCreated(openedExisting bool) bool {
	return !openedExisting
}

func rollbackCreatedRegistryKeys(primary error, cleanup ...func() error) error {
	return combineRegistrationRollbackErrors(primary, cleanupRegistryKeys(cleanup...))
}

func cleanupRegistryKeys(cleanup ...func() error) error {
	var cleanupErr error
	for _, cleanupKey := range cleanup {
		if err := cleanupKey(); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("clean up created registry key: %w", err))
		}
	}
	return cleanupErr
}

func updateRegistrationValues(store registrationValueStore, changes []registrationValueChange) error {
	return updateRegistrationValuesWithRollback(store, changes, nil)
}

func updateRegistrationValuesWithRollback(store registrationValueStore, changes []registrationValueChange, rollbackNewKey func() error) error {
	previous := make([]registrationValue, len(changes))
	for i, change := range changes {
		value, err := store.read(change.name)
		if err != nil {
			readErr := fmt.Errorf("read registration value %q: %w", change.name, err)
			if rollbackNewKey != nil {
				return combineRegistrationRollbackErrors(readErr, wrapRegistrationKeyRollback(rollbackNewKey()))
			}
			return readErr
		}
		value.data = append([]byte(nil), value.data...)
		previous[i] = value
	}

	for i, change := range changes {
		var err error
		if change.value.exists {
			err = store.write(change.name, change.value)
		} else {
			err = store.delete(change.name)
		}
		if err == nil {
			continue
		}

		primaryErr := fmt.Errorf("write registration value %q: %w", change.name, err)
		var rollbackErr error
		for j := i; j >= 0; j-- {
			var restoreErr error
			if previous[j].exists {
				restoreErr = store.write(changes[j].name, previous[j])
			} else {
				restoreErr = store.delete(changes[j].name)
			}
			if restoreErr != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore registration value %q: %w", changes[j].name, restoreErr))
			}
		}
		if rollbackNewKey != nil {
			rollbackErr = errors.Join(rollbackErr, wrapRegistrationKeyRollback(rollbackNewKey()))
		}
		if rollbackErr != nil {
			return combineRegistrationRollbackErrors(primaryErr, fmt.Errorf("rollback registration values: %w", rollbackErr))
		}
		return primaryErr
	}
	return nil
}

func wrapRegistrationKeyRollback(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("rollback newly created registration key: %w", err)
}
