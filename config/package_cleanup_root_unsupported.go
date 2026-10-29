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

//go:build js || plan9

package config

// os.Root does not pin directory identity across renames on these targets and
// its symlink confinement is vulnerable to TOCTOU races on js. Retaining the
// generation is safer than deleting by a pathname that may have changed.
func supportsPinnedCleanup() bool { return false }
