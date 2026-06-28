package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ============================================================
// #3 - Thought blocks: dim/faint styling
// ============================================================

var thoughtStyle = lipgloss.NewStyle().
	Foreground(tokyoComment).
	Faint(true)

// renderThought renders thought content as dimmed faint text
func renderThought(s string, width int) string {
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
			cut := maxW
			for i := maxW; i > maxW/2; i-- {
				if l[i] == ' ' {
					cut = i
					break
				}
			}
			out.WriteString(thoughtStyle.Render(l[:cut]) + "\n")
			l = strings.TrimSpace(l[cut:])
		}
		if l != "" {
			out.WriteString(thoughtStyle.Render(l) + "\n")
		}
	}
	return out.String()
}

// ============================================================
// #2 - Tool call cards
// ============================================================

var (
	toolCardBorder = lipgloss.NewStyle().
			Border(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("#4a5280")).
			Padding(0, 1)

	toolCardName = lipgloss.NewStyle().
			Foreground(tokyoGreen).
			Bold(true)

	toolCardArgs = lipgloss.NewStyle().
			Foreground(tokyoComment)

	toolCardTime = lipgloss.NewStyle().
			Foreground(tokyoYellow)
)

// renderToolCard renders a structured tool call card
func renderToolCard(name, args string, elapsed string, width int) string {
	cardW := width - 8
	if cardW < 30 {
		cardW = 30
	}

	// Format args nicely - truncate long values
	argDisplay := args
	if len(argDisplay) > cardW-len(name)-4 {
		argDisplay = argDisplay[:cardW-len(name)-10] + "..."
	}

	header := toolCardName.Render(name)
	if elapsed != "" {
		header += " " + toolCardTime.Render(elapsed)
	}

	content := header
	if argDisplay != "" {
		content += "\n" + toolCardArgs.Render(argDisplay)
	}

	return toolCardBorder.Width(cardW).Render(content)
}

// ============================================================
// #6 - Diff highlighting
// ============================================================

var (
	diffAdd    = lipgloss.NewStyle().Foreground(tokyoGreen)
	diffRemove = lipgloss.NewStyle().Foreground(tokyoRed)
	diffHeader = lipgloss.NewStyle().Foreground(tokyoCyan).Bold(true)
)

// renderDiff highlights a diff-style output with green/red
func renderDiff(content string) string {
	lines := strings.Split(content, "\n")
	var out strings.Builder
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "+"):
			out.WriteString(diffAdd.Render(l) + "\n")
		case strings.HasPrefix(l, "-"):
			out.WriteString(diffRemove.Render(l) + "\n")
		case strings.HasPrefix(l, "@@"):
			out.WriteString(diffHeader.Render(l) + "\n")
		default:
			out.WriteString(l + "\n")
		}
	}
	return out.String()
}

// isDiff checks if content looks like a diff
func isDiff(s string) bool {
	lines := strings.SplitN(s, "\n", 10)
	diffLines := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") || strings.HasPrefix(l, "@@") {
			diffLines++
		}
	}
	return diffLines >= 3
}

// ============================================================
// #8 - Token budget bar
// ============================================================

func renderTokenBar(tokens, maxTokens, width int) string {
	if maxTokens <= 0 {
		maxTokens = 128000
	}
	ratio := float64(tokens) / float64(maxTokens)
	if ratio > 1 {
		ratio = 1
	}
	barW := width - 12 // room for label
	if barW < 10 {
		barW = 10
	}
	filled := int(ratio * float64(barW))

	var color lipgloss.Color
	switch {
	case ratio < 0.5:
		color = tokyoGreen
	case ratio < 0.8:
		color = tokyoYellow
	default:
		color = tokyoRed
	}

	bar := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("━", filled)) +
		lipgloss.NewStyle().Foreground(tokyoGutter).Render(strings.Repeat("─", barW-filled))

	pct := fmt.Sprintf(" %d%%", int(ratio*100))
	return bar + lipgloss.NewStyle().Foreground(tokyoComment).Render(pct)
}

// ============================================================
// #9 - Message separators
// ============================================================

func renderSeparator(width int) string {
	sepW := width - 4
	if sepW < 10 {
		sepW = 10
	}
	return lipgloss.NewStyle().Foreground(tokyoGutter).Render(strings.Repeat("─", sepW)) + "\n"
}

// ============================================================
// #4 - Progress indicator for tool calls
// ============================================================

// formatProgress formats bytes/lines progress for display
func formatProgress(output string) string {
	lines := strings.Count(output, "\n")
	bytes := len(output)
	if bytes > 1024*1024 {
		return fmt.Sprintf("%.1fMB %dL", float64(bytes)/(1024*1024), lines)
	}
	if bytes > 1024 {
		return fmt.Sprintf("%.1fKB %dL", float64(bytes)/1024, lines)
	}
	return fmt.Sprintf("%dB %dL", bytes, lines)
}
