// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

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
