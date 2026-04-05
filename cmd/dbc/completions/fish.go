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

package completions

import _ "embed"

//go:embed dbc.fish
var fishScript string

type Fish struct{}

func (Fish) Description() string {
	return `Generate the autocompletion script for the fish shell.

To load completions in your current shell session:

    dbc completion fish | source

To load completions for every new session, execute once:

    dbc completion fish > ~/.config/fish/completions/dbc.fish

You will need to start a new shell for this setup to take effect.
`
}

func (Fish) GetScript() string {
	return fishScript
}
