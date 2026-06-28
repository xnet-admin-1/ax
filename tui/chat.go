// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package tui

import (
	"fmt"
	"strings"
	"regexp"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/lipgloss/tree"
)

type chatMsg struct {
	role    string
	content string
}

func renderMessages(msgs []chatMsg, width int, showToolDetail bool, expanded map[int]bool, r *glamour.TermRenderer) string {
	var b strings.Builder

	for i, m := range msgs {
		switch m.role {
		case "user":
			b.WriteString(userMsgStyle.Render("You") + "\n")
			b.WriteString(wrapText(m.content, width-2) + "\n\n")
		case "assistant":
			if m.content != "" {
				var out string
				cleaned := filterToolMarkup(m.content, width)
				if r != nil {
					out, _ = r.Render(cleaned)
					if strings.TrimSpace(out) == "" {
						out = wrapText(cleaned, width-4)
					}
				} else {
					out = wrapText(cleaned, width-4)
				}
				lines := strings.Split(strings.TrimSpace(out), "\n")
				for _, line := range lines {
					b.WriteString(assistantGutter.Render("") + line + "\n")
				}
				b.WriteString("\n")
			}
		case "system":
			content := m.content
			// Word wrap long system messages
			if len(content) > width-4 {
				content = wordWrap(content, width-4)
			}
			if strings.HasPrefix(m.content, "Error") || strings.HasPrefix(m.content, "Spawn failed") {
				b.WriteString(errorMsgStyle.Render("│ " + content) + "\n\n")
			} else {
				b.WriteString(timestampStyle.Render("  " + content) + "\n\n")
			}
		case "tool_call":
			if m.content != "" {
				b.WriteString(toolCallStyle.Render(wordWrap(m.content, width-4)) + "\n")
			}
		case "tool_result":
			if m.content == "" {
				continue
			}
			isExpanded := showToolDetail || expanded[i]
			if isExpanded {
				rendered := renderToolResult(m.content, width-4)
				b.WriteString(toolCallStyle.Render("  ╰─ ") + rendered + "\n")
			} else {
				b.WriteString(toolCallStyle.Render(fmt.Sprintf("  ╰─ %d bytes (ctrl+o expand)", len(m.content))) + "\n")
			}
		case "agent_result":
			content := m.content
			// Strip the [AgentName] prefix for rendering
			if idx := strings.Index(content, "]"); idx > 0 && strings.HasPrefix(content, "[") {
				b.WriteString(agentResultStyle.Render(content[:idx+1]) + "\n")
				content = strings.TrimSpace(content[idx+1:])
			} else {
				b.WriteString(agentResultStyle.Render("Agent Result") + "\n")
			}
			if content != "" {
				var out string
				if looksLikeMarkdown(content) && r != nil {
					out, _ = r.Render(content)
				} else {
					out = wrapText(content, width-4)
				}
				lines := strings.Split(strings.TrimSpace(out), "\n")
				for _, line := range lines {
					b.WriteString(assistantGutter.Render("") + line + "\n")
				}
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderToolResult formats tool output — detects tables and directory listings
func renderToolResult(content string, width int) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")

	// Detect directory listing (lines ending with /)
	if isDirListing(lines) {
		return renderAsTree(lines)
	}

	// Detect tabular data (consistent column separators)
	if isTabular(lines) {
		return renderAsTable(lines, width)
	}

	// Default: word-wrapped text
	result := content
	if len(result) > 2000 {
		result = result[:2000] + "…"
	}
	return toolCallStyle.Render(wordWrap(result, width))
}

func isDirListing(lines []string) bool {
	if len(lines) < 2 {
		return false
	}
	dirCount := 0
	for _, l := range lines {
		if strings.HasSuffix(strings.TrimSpace(l), "/") {
			dirCount++
		}
	}
	return dirCount > len(lines)/3
}

func renderAsTree(lines []string) string {
	t := tree.Root(".")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			t.Child(l)
		}
	}
	return t.String()
}

func isTabular(lines []string) bool {
	if len(lines) < 2 {
		return false
	}
	// Check if lines have consistent tab or multi-space separation
	cols := strings.Fields(lines[0])
	if len(cols) < 2 {
		return false
	}
	consistent := 0
	for _, l := range lines[1:] {
		if len(strings.Fields(l)) == len(cols) {
			consistent++
		}
	}
	return consistent > len(lines)/2
}

func renderAsTable(lines []string, width int) string {
	if len(lines) == 0 {
		return ""
	}
	headers := strings.Fields(lines[0])
	var rows [][]string
	for _, l := range lines[1:] {
		fields := strings.Fields(l)
		if len(fields) == len(headers) {
			rows = append(rows, fields)
		} else if len(fields) > 0 {
			// Pad or join extra fields
			row := make([]string, len(headers))
			copy(row, fields)
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return strings.Join(lines, "\n")
	}

	t := table.New().
		Headers(headers...).
		Rows(rows...).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#3b4261"))).
		Width(width)

	return t.Render()
}

// looksLikeMarkdown returns true if content has markdown formatting markers
func looksLikeMarkdown(s string) bool {
	for _, line := range strings.SplitN(s, "\n", 30) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") ||
			strings.HasPrefix(trimmed, "## ") ||
			strings.HasPrefix(trimmed, "### ") ||
			strings.HasPrefix(trimmed, "```") ||
			strings.HasPrefix(trimmed, "- ") ||
			strings.HasPrefix(trimmed, "* ") ||
			strings.HasPrefix(trimmed, "+ ") ||
			strings.HasPrefix(trimmed, "> ") ||
			strings.Contains(trimmed, "**") ||
			strings.Contains(trimmed, "`") ||
			strings.Contains(trimmed, "](") ||
			(len(trimmed) > 2 && trimmed[0] >= '0' && trimmed[0] <= '9' && strings.Contains(trimmed[:3], ".")) {
			return true
		}
	}
	return false
}

// wrapText wraps text to the given width
func wrapText(s string, width int) string {
	if width <= 0 {
		return s
	}
	var result strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if len(line) <= width {
			result.WriteString(line + "\n")
			continue
		}
		words := strings.Fields(line)
		current := ""
		for _, word := range words {
			if current == "" {
				current = word
			} else if len(current)+1+len(word) <= width {
				current += " " + word
			} else {
				result.WriteString(current + "\n")
				current = word
			}
		}
		if current != "" {
			result.WriteString(current + "\n")
		}
	}
	return strings.TrimRight(result.String(), "\n")
}


// filterToolMarkup removes raw tool call syntax that bleeds into display
func filterToolMarkup(s string, width int) string {
	// Render complete thought blocks as dimmed text, hard-wrap by char count
	s = regexp.MustCompile("(?s)<thought>(.*?)</thought>").ReplaceAllStringFunc(s, func(m string) string {
		inner := regexp.MustCompile("(?s)<thought>(.*?)</thought>").FindStringSubmatch(m)
		if len(inner) < 2 { return "" }
		return wrapThought(inner[1], width)
	})
	// Render incomplete thought block (still streaming)
	s = regexp.MustCompile("(?s)<thought>(.*)$").ReplaceAllStringFunc(s, func(m string) string {
		inner := regexp.MustCompile("(?s)<thought>(.*)$").FindStringSubmatch(m)
		if len(inner) < 2 { return "" }
		return wrapThought(inner[1], width)
	})
	result := s
	for _, marker := range []string{"<|tool_call", "<|im_end", "<|assistant", "<|function_call"} {
		for {
			idx := strings.Index(result, marker)
			if idx < 0 {
				break
			}
			end := strings.Index(result[idx:], "|>")
			if end >= 0 {
				result = result[:idx] + result[idx+end+2:]
			} else {
				nl := strings.Index(result[idx:], "\n")
				if nl >= 0 {
					result = result[:idx] + result[idx+nl:]
				} else {
					result = result[:idx]
				}
			}
		}
	}
	return result
}

func wordWrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	var result strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if lipgloss.Width(line) <= width {
			result.WriteString(line + "\n")
			continue
		}
		// For lines with ANSI codes, just hard-cut at visual width
		for lipgloss.Width(line) > width {
			cut := 0
			vis := 0
			lastSpace := -1
			inEsc := false
			for i, r := range line {
				if r == '\x1b' {
					inEsc = true
				}
				if inEsc {
					if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
						inEsc = false
					}
					cut = i + 1
					continue
				}
				vis++
				if r == ' ' {
					lastSpace = i + 1
				}
				if vis >= width {
					cut = i + 1
					break
				}
				cut = i + 1
			}
			if lastSpace > 0 && lastSpace < cut {
				cut = lastSpace
			}
			result.WriteString(line[:cut] + "\n")
			line = line[cut:]
		}
		if line != "" {
			result.WriteString(line + "\n")
		}
	}
	return strings.TrimRight(result.String(), "\n")
}

// wrapThought wraps thought content at word boundaries within width.
// Breaks at existing newlines and at the last space before width limit.
func wrapThought(s string, width int) string {
	maxW := width - 4
	if maxW < 40 {
		maxW = 40
	}
	lines := strings.Split(s, "\n")
	var out strings.Builder
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		for len(l) > maxW {
			// Find last space within maxW
			cut := maxW
			for i := maxW; i > maxW/2; i-- {
				if l[i] == ' ' {
					cut = i
					break
				}
			}
			out.WriteString("> " + l[:cut] + "\n")
			l = strings.TrimSpace(l[cut:])
		}
		if l != "" {
			out.WriteString("> " + l + "\n")
		}
	}
	return out.String()
}

// looksLikeCode returns true if content appears to be source code or structured output
func looksLikeCode(s string) bool {
	indicators := []string{"func ", "def ", "class ", "import ", "package ", "const ", "var ", "type ", "{", "};", "=>", "->"}
	lines := strings.SplitN(s, "\n", 10)
	hits := 0
	for _, line := range lines {
		for _, ind := range indicators {
			if strings.Contains(line, ind) {
				hits++
				break
			}
		}
	}
	return hits >= 2
}
