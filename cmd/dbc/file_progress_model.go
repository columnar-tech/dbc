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
	"fmt"

	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"
)

type FileProgressModel struct {
	progress.Model

	totalBytes int64
	written    int64
}

func NewFileProgress(opts ...progress.Option) FileProgressModel {
	return FileProgressModel{Model: progress.New(opts...)}
}

func (m FileProgressModel) Init() tea.Cmd {
	return nil
}

func (m *FileProgressModel) SetPercent(written, total int64) tea.Cmd {
	m.written = written
	m.totalBytes = total
	return m.Model.SetPercent(float64(written) / float64(total))
}

func (m FileProgressModel) Update(msg tea.Msg) (FileProgressModel, tea.Cmd) {
	var cmd tea.Cmd
	m.Model, cmd = m.Model.Update(msg)
	return m, cmd
}

func formatSize(n int64) string {
	const (
		KiB = 1024
		MiB = 1024 * KiB
	)

	switch {
	case n >= MiB:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(MiB))
	case n >= KiB:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(KiB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func (m FileProgressModel) View() string {
	return fmt.Sprintf("%s %s / %s", m.Model.View(), formatSize(m.written), formatSize(m.totalBytes))
}

func (m FileProgressModel) Percent() int {
	if m.totalBytes <= 0 {
		return 0
	}
	return int(float64(m.written) / float64(m.totalBytes) * 100)
}
