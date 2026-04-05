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

package main

import (
	"errors"
	"io"
	"os"

	"github.com/columnar-tech/dbc/config"
	"golang.org/x/sys/windows/registry"
)

func (suite *SubcommandTestSuite) TearDownTest() {
	// Clean up the registry and filesystem after each test
	_, user := os.LookupEnv("DBC_TEST_LEVEL_USER")
	_, system := os.LookupEnv("DBC_TEST_LEVEL_SYSTEM")

	if user {
		suite.Require().NoError(deleteRegistryKeyRecursive(registry.CURRENT_USER, "SOFTWARE\\ADBC\\Drivers"))
		suite.Require().NoError(os.RemoveAll(config.Get()[config.ConfigUser].Location))
	}
	if system {
		suite.Require().NoError(deleteRegistryKeyRecursive(registry.LOCAL_MACHINE, "SOFTWARE\\ADBC\\Drivers"))
		suite.Require().NoError(os.RemoveAll(config.Get()[config.ConfigSystem].Location))
	}
}

// recursively deletes a registry key and all its subkeys
// TODO: Somewhat duplicated with clearRegistry in registry_test.go
// This is slightly more aggressive in that it deletes the top level key too
func deleteRegistryKeyRecursive(root registry.Key, path string) error {
	k, err := registry.OpenKey(root, path, registry.ALL_ACCESS)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer k.Close()

	// Delete all subkeys
	subkeys, err := k.ReadSubKeyNames(-1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	for _, subkey := range subkeys {
		if err := registry.DeleteKey(k, subkey); err != nil {
			return err
		}
	}

	// Delete the top level key
	return registry.DeleteKey(root, path)
}
