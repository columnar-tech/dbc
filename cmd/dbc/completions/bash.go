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

import _ "embed"

//go:embed dbc.bash
var bashScript string

type Bash struct{}

func (Bash) Description() string {
	return `Generate the autocompletion script for the bash shell.

This script depends on the 'bash-completion' package.
If it is not installed already, you can install it via your OS's package manager.

To load completions in your current shell session:

    source <(dbc completion bash)

To load completions for every new session, execute once:

#### Linux:

    dbc completion bash > /etc/bash_completion.d/dbc
	
#### macOS:

	dbc completion bash > $(brew --prefix)/etc/bash_completion.d/dbc

You will need to start a new shell for this setup to take effect.
`
}

func (Bash) GetScript() string {
	return bashScript
}
