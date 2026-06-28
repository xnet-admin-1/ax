// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ============================================================
// Tokyo Night Color Palette (consistent across all elements)
// ============================================================

var (
	// Core colors
	tokyoBg        = lipgloss.Color("#1a1b26")
	tokyoFg        = lipgloss.Color("#c0caf5")
	tokyoComment   = lipgloss.Color("#565f89")
	tokyoSelection = lipgloss.Color("#283457")
	tokyoCyan      = lipgloss.Color("#7dcfff")
	tokyoBlue      = lipgloss.Color("#7aa2f7")
	tokyoPurple    = lipgloss.Color("#bb9af7")
	tokyoGreen     = lipgloss.Color("#9ece6a")
	tokyoOrange    = lipgloss.Color("#ff9e64")
	tokyoRed       = lipgloss.Color("#f7768e")
	tokyoYellow    = lipgloss.Color("#e0af68")
	tokyoDark      = lipgloss.Color("#1f2335")
	tokyoGutter    = lipgloss.Color("#3b4261")

	// Status bar
	statusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#292e42")).
			Foreground(tokyoFg).
			Bold(true)

	statusModelStyle = lipgloss.NewStyle().
				Foreground(tokyoPurple).
				Bold(true)

	statusTokenStyle = lipgloss.NewStyle().
				Foreground(tokyoGreen)

	statusTimerStyle = lipgloss.NewStyle().
				Foreground(tokyoComment).
				Faint(true)

	// Messages
	userMsgStyle = lipgloss.NewStyle().
			Foreground(tokyoBlue).
			Bold(true)

	assistantGutter = lipgloss.NewStyle().
			Foreground(tokyoPurple).
			SetString("│ ")

	toolCallStyle = lipgloss.NewStyle().
			Foreground(tokyoGreen).
			Faint(true)

	agentResultStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#bb86fc")).
			Bold(true)

	errorMsgStyle = lipgloss.NewStyle().
			Foreground(tokyoRed).
			Bold(true)

	timestampStyle = lipgloss.NewStyle().
			Foreground(tokyoComment).
			Faint(true)

	// Panels
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(tokyoPurple).
			Padding(1)

	panelHintStyle = lipgloss.NewStyle().
			Foreground(tokyoComment).
			Faint(true)

	// Input
	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(tokyoGutter)

	inputActiveStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(tokyoBlue)

	placeholderColor = lipgloss.AdaptiveColor{Light: "#94a3b8", Dark: "#565f89"}

	// Autocomplete
	compMatchStyle = lipgloss.NewStyle().
			Foreground(tokyoCyan).
			Bold(true)

	compNormalStyle = lipgloss.NewStyle().
			Foreground(tokyoFg)

	compSelectedStyle = lipgloss.NewStyle().
				Background(tokyoSelection).
				Foreground(tokyoCyan).
				Bold(true)

	// Help bar
	helpBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#292e42")).
			Foreground(tokyoComment)

	helpKeyStyle = lipgloss.NewStyle().
			Foreground(tokyoYellow).
			Bold(true)

	helpDescStyle = lipgloss.NewStyle().
			Foreground(tokyoComment)

	// Breadcrumb
	breadcrumbStyle = lipgloss.NewStyle().
			Foreground(tokyoComment).
			Faint(true)

	breadcrumbActiveStyle = lipgloss.NewStyle().
				Foreground(tokyoCyan)

	// Panel content styles
	panelHeaderStyle = lipgloss.NewStyle().
				Foreground(tokyoCyan).
				Bold(true)

	panelLabelStyle = lipgloss.NewStyle().
			Foreground(tokyoComment)

	panelValueStyle = lipgloss.NewStyle().
			Foreground(tokyoFg)

	panelCursorStyle = lipgloss.NewStyle().
				Foreground(tokyoGreen).
				Bold(true)

	panelSelectedStyle = lipgloss.NewStyle().
				Foreground(tokyoCyan).
				Bold(true)

	panelCheckStyle = lipgloss.NewStyle().
			Foreground(tokyoGreen)

	// Progress bar characters
	progressFull  = "█"
	progressEmpty = "░"
)

// ============================================================
// Token Progress Bar (gradient fill)
// ============================================================

func tokenProgressBar(tokens, maxTokens, width int) string {
	if maxTokens <= 0 {
		maxTokens = 128000
	}
	ratio := float64(tokens) / float64(maxTokens)
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio * float64(width))
	if filled > width {
		filled = width
	}

	// Gradient: green -> yellow -> red
	var color lipgloss.Color
	switch {
	case ratio < 0.5:
		color = tokyoGreen
	case ratio < 0.8:
		color = tokyoYellow
	default:
		color = tokyoRed
	}

	bar := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat(progressFull, filled)) +
		lipgloss.NewStyle().Foreground(tokyoGutter).Render(strings.Repeat(progressEmpty, width-filled))
	return bar
}

// ============================================================
// Model display with provider color-coding
// ============================================================

func coloredModel(displayID string) string {
	if idx := strings.Index(displayID, "/"); idx > 0 {
		provider := displayID[:idx]
		model := displayID[idx+1:]
		if len(model) > 24 {
			model = model[:24]
		}
		pStyle := lipgloss.NewStyle().Foreground(tokyoComment).Faint(true)
		mStyle := lipgloss.NewStyle().Foreground(tokyoPurple).Bold(true)
		return pStyle.Render(provider+"/") + mStyle.Render(model)
	}
	if len(displayID) > 28 {
		displayID = displayID[:28]
	}
	return statusModelStyle.Render(displayID)
}

// ============================================================
// Session Timer
// ============================================================

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%d:%02d:%02d", h, m, s)
}

// ============================================================
// Activity Spinners (different per type)
// ============================================================

var spinnerFrames = map[string][]string{
	"thinking": {"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	"shell":    {"▏", "▎", "▍", "▌", "▋", "▊", "▉", "█", "▉", "▊", "▋", "▌", "▍", "▎"},
	"reading":  {"←", "↖", "↑", "↗", "→", "↘", "↓", "↙"},
	"writing":  {"◐", "◓", "◑", "◒"},
	"default":  {"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"},
}

func getSpinnerFrame(activity string, tick int) string {
	frames, ok := spinnerFrames[activity]
	if !ok {
		frames = spinnerFrames["default"]
	}
	return frames[tick%len(frames)]
}

// ============================================================
// Help Bar (context-sensitive, vim-style)
// ============================================================

func (m *model) helpBar() string {
	var keys []string

	switch {
	case m.panel == panelModels:
		keys = []string{helpKey("enter", "select"), helpKey("/", "filter"), helpKey("esc", "close")}
	case m.panel == panelSessions:
		keys = []string{helpKey("enter", "open"), helpKey("n", "rename"), helpKey("d", "delete"), helpKey("esc", "close")}
	case m.panel == panelProvider:
		keys = []string{helpKey("enter", "toggle"), helpKey("a", "add"), helpKey("d", "delete"), helpKey("esc", "close")}
	case m.panel == panelMemory:
		keys = []string{helpKey("enter", "edit"), helpKey("a", "add"), helpKey("d", "delete"), helpKey("esc", "close")}
	case m.panel == panelRemote:
		keys = []string{helpKey("enter", "connect"), helpKey("a", "add"), helpKey("h", "health"), helpKey("p", "deploy"), helpKey("d", "delete"), helpKey("esc", "close")}
	case m.panel == panelSpawn:
		keys = []string{helpKey("enter", "spawn"), helpKey("b", "builder"), helpKey("/", "filter"), helpKey("esc", "close")}
	case m.panel == panelAgentBuilder:
		keys = []string{helpKey("enter", "edit"), helpKey("a", "add"), helpKey("d", "delete"), helpKey("esc", "close")}
	case m.panel == panelAgents:
		keys = []string{helpKey("enter", "inspect"), helpKey("p", "prompt"), helpKey("k", "kill"), helpKey("r", "refresh"), helpKey("esc", "close")}
	case m.panel == panelTools:
		keys = []string{helpKey("enter", "toggle"), helpKey("esc", "close")}
	case m.panel == panelMCP:
		keys = []string{helpKey("enter", "toggle"), helpKey("a", "add"), helpKey("d", "delete"), helpKey("esc", "close")}
	case m.panel == panelSettings:
		keys = []string{helpKey("enter", "edit"), helpKey("esc", "close")}
	case m.panel == panelConfig:
		keys = []string{helpKey("enter", "edit"), helpKey("esc", "close")}
	case m.panel == panelVectors:
		keys = []string{helpKey("r", "reindex"), helpKey("esc", "close")}
	case m.panel == panelUsage:
		keys = []string{helpKey("r", "reset"), helpKey("esc", "close")}
	case m.panel == panelTree:		keys = []string{helpKey("enter", "switch"), helpKey("esc", "close")}
	case m.panel == panelCompact:
		keys = []string{helpKey("enter", "run"), helpKey("esc", "close")}
	case m.panel == panelDebug:
		keys = []string{helpKey("tab", "cycle"), helpKey("enter", "on/off"), helpKey("esc", "back")}
	case m.panel == panelTheme:
		keys = []string{helpKey("tab", "cycle"), helpKey("enter", "apply"), helpKey("esc", "back")}
	case m.panel != panelNone:
		keys = []string{helpKey("esc", "close")}
	case m.streaming:
		keys = []string{helpKey("ctrl+c", "stop"), helpKey("enter", "interrupt")}
	default:
		keys = []string{helpKey("/", "cmd"), helpKey("shift+↑↓", "scroll"), helpKey("shift+←→", "history"), helpKey("ctrl+n", "new"), helpKey("ctrl+e", "editor")}
	}

	w := m.width
	if w < 20 {
		w = 20
	}
	// Wrap keys to fit width - show as many as fit, drop from the end
	bar := strings.Join(keys, "  ")
	for lipgloss.Width(bar) > w-2 && len(keys) > 1 {
		keys = keys[:len(keys)-1]
		bar = strings.Join(keys, "  ")
	}
	gap := w - lipgloss.Width(bar)
	if gap < 0 {
		gap = 0
	}
	return helpBarStyle.Width(w).Render(bar + strings.Repeat(" ", gap))
}

func helpKey(key, desc string) string {
	return helpKeyStyle.Render(key) + " " + helpDescStyle.Render(desc)
}

// ============================================================
// Breadcrumb for nested panel states
// ============================================================

func (m *model) breadcrumb() string {
	parts := []string{}

	switch m.panel {
	case panelProvider:
		parts = append(parts, "Provider")
		if m.provAddStep > 0 {
			steps := []string{"", "Name", "URL", "Key"}
			if m.provAddStep < len(steps) {
				parts = append(parts, "Add", steps[m.provAddStep])
			}
		}
	case panelRemote:
		parts = append(parts, "Remote")
		if m.remoteAddStep > 0 {
			steps := []string{"", "Name", "Host", "Port", "User"}
			if m.remoteAddStep < len(steps) {
				parts = append(parts, "Add", steps[m.remoteAddStep])
			}
		}
	case panelMemory:
		parts = append(parts, "Memory")
		if m.memEditStep > 0 {
			parts = append(parts, "Edit")
		}
	case panelAgentBuilder:
		parts = append(parts, "Agent")
		if m.agentBuildStep > 0 {
			steps := []string{"", "Name", "Mode", "Prompt"}
			if m.agentBuildStep < len(steps) {
				parts = append(parts, "Build", steps[m.agentBuildStep])
			}
		}
	case panelSpawn:
		parts = append(parts, "Spawn")
		if m.spawnTaskInput {
			parts = append(parts, m.spawnAgentName)
		}
	}

	if len(parts) == 0 {
		return ""
	}

	var rendered []string
	for i, p := range parts {
		if i == len(parts)-1 {
			rendered = append(rendered, breadcrumbActiveStyle.Render(p))
		} else {
			rendered = append(rendered, breadcrumbStyle.Render(p))
		}
	}
	return strings.Join(rendered, breadcrumbStyle.Render(" > ")) + "\n"
}

// ============================================================
// Character count indicator
// ============================================================

func charCountIndicator(current, max int) string {
	ratio := float64(current) / float64(max)
	var color lipgloss.Color
	switch {
	case ratio < 0.5:
		color = tokyoComment
	case ratio < 0.8:
		color = tokyoYellow
	default:
		color = tokyoRed
	}
	return lipgloss.NewStyle().Foreground(color).Render(fmt.Sprintf("%d", current))
}

// runeWidth returns the display width of a string (rune count, not byte count)
func runeWidth(s string) int {
	n := 0
	for _, r := range s {
		_ = r
		n++
	}
	return n
}
