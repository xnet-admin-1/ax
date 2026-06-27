// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type inspectorModel struct {
	showing      bool
	content      string
	scrollOffset int
	title        string
	lines        []string
}

func (ins *inspectorModel) open(title, content string) {
	ins.showing = true
	ins.title = title
	ins.content = content
	ins.scrollOffset = 0
	ins.lines = strings.Split(content, "\n")
}

func (ins *inspectorModel) close() {
	ins.showing = false
}

func (ins *inspectorModel) scrollDown() {
	if ins.scrollOffset < len(ins.lines)-1 {
		ins.scrollOffset++
	}
}

func (ins *inspectorModel) scrollUp() {
	if ins.scrollOffset > 0 {
		ins.scrollOffset--
	}
}

func (ins *inspectorModel) goTop() {
	ins.scrollOffset = 0
}

func (ins *inspectorModel) goBottom(visibleLines int) {
	max := len(ins.lines) - visibleLines
	if max < 0 {
		max = 0
	}
	ins.scrollOffset = max
}

func (ins *inspectorModel) view(width, height int) string {
	modalW := width * 80 / 100
	modalH := height * 70 / 100
	if modalW < 40 {
		modalW = 40
	}
	if modalH < 10 {
		modalH = 10
	}

	contentH := modalH - 4 // title + footer + borders
	if contentH < 1 {
		contentH = 1
	}

	// Visible slice
	end := ins.scrollOffset + contentH
	if end > len(ins.lines) {
		end = len(ins.lines)
	}
	visible := ins.lines[ins.scrollOffset:end]

	// Color diff lines
	var rendered []string
	innerW := modalW - 4
	for _, line := range visible {
		trimmed := strings.TrimSpace(line)
		if len(line) > innerW {
			line = line[:innerW]
		}
		if strings.HasPrefix(trimmed, "+") && !strings.HasPrefix(trimmed, "+++") {
			rendered = append(rendered, lipgloss.NewStyle().Foreground(tokyoGreen).Render(line))
		} else if strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "---") {
			rendered = append(rendered, lipgloss.NewStyle().Foreground(tokyoRed).Render(line))
		} else {
			rendered = append(rendered, line)
		}
	}

	// Pad to fill height
	for len(rendered) < contentH {
		rendered = append(rendered, "")
	}

	body := strings.Join(rendered, "\n")

	footer := fmt.Sprintf(" %d/%d lines  j/k:scroll g/G:top/bot q:close",
		ins.scrollOffset+1, len(ins.lines))

	titleStyle := lipgloss.NewStyle().Foreground(tokyoCyan).Bold(true)
	footerStyle := lipgloss.NewStyle().Foreground(tokyoComment).Faint(true)

	content := titleStyle.Render(" "+ins.title) + "\n" + body + "\n" + footerStyle.Render(footer)

	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(tokyoPurple).
		Width(modalW).
		Height(modalH).
		Render(content)

	// Center the modal
	padTop := (height - modalH) / 2
	padLeft := (width - modalW) / 2
	if padTop < 0 {
		padTop = 0
	}
	if padLeft < 0 {
		padLeft = 0
	}

	return strings.Repeat("\n", padTop) + strings.Repeat(" ", padLeft) + modal
}
