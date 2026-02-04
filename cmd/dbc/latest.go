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

//go:build !no_notify_latest

package main

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
)

func notifyLatest() {
	latestVer, err := dbc.GetLatestDbcVersion()
	if dbc.Version != "(devel)" && err == nil {
		if semver.MustParse(dbc.Version).LessThan(latestVer) {
			fmt.Printf(descStyle.Render("dbc version %s is available! You are using version %s. Please upgrade.\n\n"),
				latestVer, dbc.Version)
		}
	}
}
