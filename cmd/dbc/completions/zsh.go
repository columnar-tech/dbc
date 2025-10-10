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

//go:embed dbc.zsh
var zshScript string

type Zsh struct{}

func (Zsh) Description() string {
	return `Generate the autocompletion script for the zsh shell.

If shell completion is not already enabled in your environment you will need
to enable it. You can execute the following once:

	echo "autoload -U compinit; compinit" >> ~/.zshrc

To load completions in your current shell session:

    source <(dbc completion zsh)

To load completions for every new session, execute once:

#### Linux:

    dbc completion zsh > "${fpath[1]}/_dbc"
	
#### macOS:

	dbc completion zsh > $(brew --prefix)/share/zsh/site-functions/_dbc

You will need to start a new shell for this setup to take effect.
`
}

func (Zsh) GetScript() string {
	return zshScript
}
