// Copyright 2025 Columnar Technologies Inc.
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

package completions

type ShellImpl interface {
	GetScript() string
}

type Cmd struct {
	Bash *Bash `arg:"subcommand" help:"Generate autocompletion script for bash"`
	Zsh  *Zsh  `arg:"subcommand" help:"Generate autocompletion script for zsh"`
	Fish *Fish `arg:"subcommand" help:"Generate autocompletion script for fish"`
}

func (Cmd) Description() string {
	return "Generate the autocompletion script for dbc for the requested shell.\n" +
		"See each sub-command's help for details on how to use the generated script.\n"
}
