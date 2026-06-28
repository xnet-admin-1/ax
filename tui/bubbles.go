// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// Message bubble styles
var (
	userBubble = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(tokyoBlue).
			Padding(0, 1).
			MarginLeft(4)

	assistantBubble = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(tokyoPurple).
			BorderLeft(true).
			BorderRight(true).
			BorderTop(true).
			BorderBottom(true).
			Padding(0, 1)

	toolBubble = lipgloss.NewStyle().
			Border(lipgloss.HiddenBorder()).
			BorderForeground(tokyoGreen).
			BorderLeft(true).
			Padding(0, 1).
			Foreground(tokyoComment)

	agentBubble = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#bb86fc")).
			Padding(0, 1)

	errorBubble = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(tokyoRed).
			Padding(0, 1)

	systemBubble = lipgloss.NewStyle().
			Foreground(tokyoComment).
			Faint(true).
			Italic(true).
			PaddingLeft(2)

	roleBadgeUser = lipgloss.NewStyle().
			Foreground(tokyoBg).
			Background(tokyoBlue).
			Bold(true).
			Padding(0, 1).
			MarginLeft(4)

	roleBadgeAssistant = lipgloss.NewStyle().
				Foreground(tokyoBg).
				Background(tokyoPurple).
				Bold(true).
				Padding(0, 1)

	roleBadgeAgent = lipgloss.NewStyle().
			Foreground(tokyoBg).
			Background(lipgloss.Color("#bb86fc")).
			Bold(true).
			Padding(0, 1)

	toolNameBadge = lipgloss.NewStyle().
			Foreground(tokyoGreen).
			Bold(true)
)

// renderBubbleMessages renders all messages using bubble-style containers
func renderBubbleMessages(msgs []chatMsg, width int, showToolDetail bool, expanded map[int]bool, r *glamour.TermRenderer) string {
	var b strings.Builder
	layout := getLayoutMode(width)
	var bubbleWidth int
	switch layout {
	case layoutCompact:
		bubbleWidth = width - 2
	case layoutNormal:
		bubbleWidth = width - 6
	default:
		bubbleWidth = int(float64(width) * 0.75)
	}
	if bubbleWidth < 40 {
		bubbleWidth = 40
	}

	for i, m := range msgs {
		switch m.role {
		case "user":
			b.WriteString(roleBadgeUser.Render(" You ") + "\n")
			content := wrapText(m.content, bubbleWidth-4)
			b.WriteString(userBubble.Width(bubbleWidth).Render(content) + "\n\n")

		case "assistant":
			if m.content == "" {
				continue
			}
			b.WriteString(roleBadgeAssistant.Render(" AX ") + "\n")
			cleaned := filterToolMarkup(m.content, bubbleWidth)
			var out string
			if r != nil {
				out, _ = r.Render(cleaned)
				out = strings.TrimSpace(out)
				if out == "" {
					out = wrapText(cleaned, bubbleWidth-4)
				}
			} else {
				out = wrapText(cleaned, bubbleWidth-4)
			}
			b.WriteString(assistantBubble.Width(bubbleWidth).Render(out) + "\n\n")

		case "system":
			content := m.content
			if strings.HasPrefix(content, "Error") || strings.HasPrefix(content, "Spawn failed") || strings.Contains(content, "error") {
				b.WriteString(errorBubble.Width(bubbleWidth).Render(wrapText(content, bubbleWidth-4)) + "\n\n")
			} else {
				b.WriteString(systemBubble.Width(bubbleWidth).Render(wrapText(content, bubbleWidth-4)) + "\n\n")
			}

		case "tool_call":
			if m.content != "" {
				parts := strings.SplitN(m.content, " ", 2)
				name := parts[0]
				args := ""
				if len(parts) > 1 {
					args = parts[1]
				}
				header := toolNameBadge.Render(""+name) + " " + timestampStyle.Render(truncateStr(args, 60))
				b.WriteString(toolBubble.Width(bubbleWidth).Render(header) + "\n")
			}

		case "tool_result":
			if m.content == "" {
				continue
			}
			isExpanded := showToolDetail || expanded[i]
			if isExpanded {
				rendered := m.content
				// Run through Glamour if it looks like code or markdown
				if r != nil && (looksLikeMarkdown(rendered) || looksLikeCode(rendered)) {
					if glamOut, err := r.Render("```\n" + rendered + "\n```"); err == nil && strings.TrimSpace(glamOut) != "" {
						rendered = strings.TrimSpace(glamOut)
					}
				} else {
					rendered = renderToolResult(rendered, bubbleWidth-4)
				}
				b.WriteString(toolBubble.Width(bubbleWidth).Render(rendered) + "\n\n")
			} else {
				summary := fmt.Sprintf("  ╰─ %d bytes", len(m.content))
				b.WriteString(toolBubble.Render(timestampStyle.Render(summary+" (ctrl+o expand)")) + "\n\n")
			}

		case "agent_result":
			content := m.content
			agentName := "Agent"
			if idx := strings.Index(content, "]"); idx > 0 && strings.HasPrefix(content, "[") {
				agentName = content[1:idx]
				content = strings.TrimSpace(content[idx+1:])
			}
			b.WriteString(roleBadgeAgent.Render(" "+agentName+" ") + "\n")
			var out string
			if looksLikeMarkdown(content) && r != nil {
				out, _ = r.Render(content)
				out = strings.TrimSpace(out)
			} else {
				out = wrapText(content, bubbleWidth-4)
			}
			b.WriteString(agentBubble.Width(bubbleWidth).Render(out) + "\n\n")
		}
	}
	return b.String()
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
