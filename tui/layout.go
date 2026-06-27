package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Layout breakpoints
const (
	breakpointNarrow = 80
	breakpointMedium = 120
	breakpointWide   = 160
)

// layoutMode determines current layout based on terminal width
type layoutMode int

const (
	layoutCompact layoutMode = iota // < 80 cols
	layoutNormal                    // 80-119 cols
	layoutWide                      // 120-159 cols
	layoutUltra                     // 160+ cols
)

func getLayoutMode(width int) layoutMode {
	switch {
	case width < breakpointNarrow:
		return layoutCompact
	case width < breakpointMedium:
		return layoutNormal
	case width < breakpointWide:
		return layoutWide
	default:
		return layoutUltra
	}
}

// composeSplitView renders chat and a side panel horizontally
func composeSplitView(chatView, sideView string, totalWidth, totalHeight int, ratio float64) string {
	chatWidth := int(float64(totalWidth) * ratio)
	sideWidth := totalWidth - chatWidth - 1 // 1 for divider

	if sideWidth < 30 {
		// Not enough room for split, return chat only
		return chatView
	}

	chatCol := lipgloss.NewStyle().
		Width(chatWidth).
		Height(totalHeight).
		Render(chatView)

	divider := lipgloss.NewStyle().
		Foreground(tokyoGutter).
		Height(totalHeight).
		Render("│")

	sideCol := lipgloss.NewStyle().
		Width(sideWidth).
		Height(totalHeight).
		Render(sideView)

	return lipgloss.JoinHorizontal(lipgloss.Top, chatCol, divider, sideCol)
}

// composeStatusBar builds the status bar using proper lipgloss alignment
func composeStatusBar(left, center, right string, width int) string {
	// Calculate visual widths
	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	centerW := lipgloss.Width(center)

	available := width - leftW - rightW - centerW
	if available < 0 {
		// Truncate center if no room
		center = ""
		centerW = 0
		available = width - leftW - rightW
		if available < 0 {
			available = 0
		}
	}
	leftGap := available / 2
	rightGap := available - leftGap

	bar := left +
		strings.Repeat(" ", leftGap) +
		center +
		strings.Repeat(" ", rightGap) +
		right

	// Apply background to the entire bar at once
	return lipgloss.NewStyle().
		Background(lipgloss.Color("#292e42")).
		Bold(true).
		Width(width).
		MaxWidth(width).
		Render(bar)
}

// composeOverlay renders content centered over background using lipgloss.Place
func composeOverlay(background, overlay string, bgWidth, bgHeight int) string {
	return lipgloss.Place(
		bgWidth, bgHeight,
		lipgloss.Center, lipgloss.Center,
		overlay,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("")),
	)
}

// floatingDialog creates a styled floating dialog box
func floatingDialog(title, content string, width int) string {
	if width > 80 {
		width = 80
	}
	if width < 40 {
		width = 40
	}

	titleBar := lipgloss.NewStyle().
		Foreground(tokyoCyan).
		Bold(true).
		Render(title)

	body := lipgloss.NewStyle().
		Width(width - 4).
		Render(content)

	dialog := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(tokyoPurple).
		Padding(1, 2).
		Width(width).
		Render(titleBar + "\n\n" + body)

	return dialog
}

// streamingBorder returns a border style that pulses during streaming
func streamingBorder(tick int) lipgloss.Style {
	colors := []lipgloss.Color{tokyoBlue, tokyoPurple, tokyoCyan}
	c := colors[tick%len(colors)]
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(c)
}
