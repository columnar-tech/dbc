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
	"os/exec"
	"path/filepath"
)

func isManaged(exe string) bool {
	// check if we're a deb install
	dpkgExe, err := exec.LookPath("dpkg")
	if err == nil {
		if err = exec.Command(dpkgExe, "-S", exe).Run(); err == nil {
			return true
		}
	}

	// check if we're an rpm install
	rpmExe, err := exec.LookPath("rpm")
	if err == nil {
		if err = exec.Command(rpmExe, "-qf", exe).Run(); err == nil {
			return true
		}
	}

	if filepath.Dir(exe) == "/usr/local/bin" {
		// pip installs here on linux
		return true
	}

	return false
}
