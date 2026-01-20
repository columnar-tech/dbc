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

package dbc

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	itemStyle         = lipgloss.NewStyle().PaddingLeft(4)
	selectedItemStyle = lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("170"))
)

type SimpleItemDelegate struct {
	Prompt string
}

func (d SimpleItemDelegate) Height() int                         { return 1 }
func (d SimpleItemDelegate) Spacing() int                        { return 0 }
func (d SimpleItemDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (d SimpleItemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(fmt.Stringer)
	if !ok {
		return
	}

	str := fmt.Sprintf("%d. %s", index+1, i.String())
	fn := itemStyle.Render
	if index == m.Index() {
		fn = func(s ...string) string {
			return selectedItemStyle.Render(d.Prompt + " " + strings.Join(s, " "))
		}
	}
	fmt.Fprint(w, fn(str))
}
