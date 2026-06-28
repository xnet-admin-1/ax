// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package tui

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type themeMode int

const (
	themeDark themeMode = iota
	themeLight
)

var activeTheme themeMode = themeDark

// DetectTheme checks AX_THEME env var (dark/light/system) and sets colors accordingly
func DetectTheme() {
	mode := os.Getenv("AX_THEME")
	switch mode {
	case "light":
		activeTheme = themeLight
		lipgloss.SetHasDarkBackground(false)
		applyLightTheme()
	case "dark":
		activeTheme = themeDark
		lipgloss.SetHasDarkBackground(true)
	default: // "system" or unset — detect from terminal background
		if !lipgloss.HasDarkBackground() {
			activeTheme = themeLight
			applyLightTheme()
		}
	}
}

func applyLightTheme() {
	// High contrast colors for light backgrounds
	tokyoBg = lipgloss.Color("#ffffff")
	tokyoFg = lipgloss.Color("#1a1b26")
	tokyoComment = lipgloss.Color("#6b7089")
	tokyoSelection = lipgloss.Color("#d4d8f0")
	tokyoCyan = lipgloss.Color("#0068a8")
	tokyoBlue = lipgloss.Color("#2244cc")
	tokyoPurple = lipgloss.Color("#7520a0")
	tokyoGreen = lipgloss.Color("#326e00")
	tokyoOrange = lipgloss.Color("#b35000")
	tokyoRed = lipgloss.Color("#c02040")
	tokyoYellow = lipgloss.Color("#7a5600")
	tokyoDark = lipgloss.Color("#f0f0f5")
	tokyoGutter = lipgloss.Color("#b0b4c8")

	// Re-apply styles with light colors
	statusBarStyle = lipgloss.NewStyle().
		Background(lipgloss.Color("#dde0f0")).
		Foreground(tokyoFg).
		Bold(true)

	userMsgStyle = lipgloss.NewStyle().
		Foreground(tokyoBlue).
		Bold(true)

	assistantGutter = lipgloss.NewStyle().
		Foreground(tokyoPurple).
		SetString("│ ")

	toolCallStyle = lipgloss.NewStyle().
		Foreground(tokyoGreen)

	agentResultStyle = lipgloss.NewStyle().
		Foreground(tokyoPurple).
		Bold(true)

	errorMsgStyle = lipgloss.NewStyle().
		Foreground(tokyoRed).
		Bold(true)

	timestampStyle = lipgloss.NewStyle().
		Foreground(tokyoComment)

	panelStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(tokyoPurple).
		Padding(1)

	inputStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(tokyoGutter)

	inputActiveStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(tokyoBlue)

	helpBarStyle = lipgloss.NewStyle().
		Background(lipgloss.Color("#dde0f0")).
		Foreground(tokyoComment)

	helpKeyStyle = lipgloss.NewStyle().
		Foreground(tokyoOrange).
		Bold(true)

	helpDescStyle = lipgloss.NewStyle().
		Foreground(tokyoComment)

	// Bubble styles
	userBubble = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(tokyoBlue).
		Padding(0, 1).
		MarginLeft(4)

	assistantBubble = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(tokyoPurple).
		Foreground(lipgloss.Color("#007700")).
		Padding(0, 1)

	toolBubble = lipgloss.NewStyle().
		Border(lipgloss.HiddenBorder()).
		BorderForeground(tokyoGreen).
		BorderLeft(true).
		Padding(0, 1).
		Foreground(tokyoComment)

	roleBadgeUser = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ffffff")).
		Background(tokyoBlue).
		Bold(true).
		Padding(0, 1).
		MarginLeft(4)

	roleBadgeAssistant = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ffffff")).
		Background(tokyoPurple).
		Bold(true).
		Padding(0, 1)

	roleBadgeAgent = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ffffff")).
		Background(tokyoPurple).
		Bold(true).
		Padding(0, 1)
}

// Theme panel state
var themeIdx int = 0
var themeOptions = []string{"dark", "light"}

func (m *model) themePanelView() string {
	var b strings.Builder
	b.WriteString("Theme  tab=cycle  enter=apply  esc=back\n\n")

	for i, t := range themeOptions {
		active := ""
		if (t == "dark" && activeTheme == themeDark) || (t == "light" && activeTheme == themeLight) {
			active = " *"
		}
		if t == "system" && os.Getenv("AX_THEME") == "" && i == themeIdx {
			// system is selected
		}
		if i == themeIdx {
			b.WriteString("  " + panelSelectedStyle.Render("["+t+"]") + active + "\n")
		} else {
			b.WriteString("  " + panelLabelStyle.Render(" "+t+" ") + active + "\n")
		}
	}
	b.WriteString("\n" + panelLabelStyle.Render("  Current: "+themeOptions[themeIdx]))
	return b.String()
}

func (m *model) handleThemeKey(key string) (bool, tea.Cmd) {
	switch key {
	case "tab", "down", "j":
		themeIdx = (themeIdx + 1) % len(themeOptions)
		return true, nil
	case "up", "k":
		themeIdx = (themeIdx - 1 + len(themeOptions)) % len(themeOptions)
		return true, nil
	case "enter":
		switch themeOptions[themeIdx] {
		case "dark":
			activeTheme = themeDark
			lipgloss.SetHasDarkBackground(true)
			applyDarkTheme()
		case "light":
			activeTheme = themeLight
			lipgloss.SetHasDarkBackground(false)
			applyLightTheme()
		}
		// Force glamour re-render with new theme
		m.glamRenderer = nil
		m.glamWidth = 0
		m.cachedMsgCount = 0
		m.panel = panelNone
		m.updateViewport()
		return true, nil
	case "esc":
		m.panel = panelNone
		return true, nil
	}
	return false, nil
}

func applyDarkTheme() {
	tokyoBg = lipgloss.Color("#1a1b26")
	tokyoFg = lipgloss.Color("#c0caf5")
	tokyoComment = lipgloss.Color("#565f89")
	tokyoSelection = lipgloss.Color("#283457")
	tokyoCyan = lipgloss.Color("#7dcfff")
	tokyoBlue = lipgloss.Color("#7aa2f7")
	tokyoPurple = lipgloss.Color("#bb9af7")
	tokyoGreen = lipgloss.Color("#9ece6a")
	tokyoOrange = lipgloss.Color("#ff9e64")
	tokyoRed = lipgloss.Color("#f7768e")
	tokyoYellow = lipgloss.Color("#e0af68")
	tokyoDark = lipgloss.Color("#1f2335")
	tokyoGutter = lipgloss.Color("#3b4261")

	statusBarStyle = lipgloss.NewStyle().
		Background(lipgloss.Color("#292e42")).
		Foreground(tokyoFg).
		Bold(true)

	userMsgStyle = lipgloss.NewStyle().Foreground(tokyoBlue).Bold(true)
	assistantGutter = lipgloss.NewStyle().Foreground(tokyoPurple).SetString("│ ")
	toolCallStyle = lipgloss.NewStyle().Foreground(tokyoGreen).Faint(true)
	agentResultStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#bb86fc")).Bold(true)
	errorMsgStyle = lipgloss.NewStyle().Foreground(tokyoRed).Bold(true)
	timestampStyle = lipgloss.NewStyle().Foreground(tokyoComment).Faint(true)
	panelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tokyoPurple).Padding(1)
	inputStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tokyoGutter)
	inputActiveStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tokyoBlue)
	helpBarStyle = lipgloss.NewStyle().Background(lipgloss.Color("#292e42")).Foreground(tokyoComment)
	helpKeyStyle = lipgloss.NewStyle().Foreground(tokyoYellow).Bold(true)
	helpDescStyle = lipgloss.NewStyle().Foreground(tokyoComment)

	userBubble = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tokyoBlue).Padding(0, 1).MarginLeft(4)
	assistantBubble = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tokyoPurple).Padding(0, 1)
	toolBubble = lipgloss.NewStyle().Border(lipgloss.HiddenBorder()).BorderForeground(tokyoGreen).BorderLeft(true).Padding(0, 1).Foreground(tokyoComment)
	roleBadgeUser = lipgloss.NewStyle().Foreground(tokyoBg).Background(tokyoBlue).Bold(true).Padding(0, 1).MarginLeft(4)
	roleBadgeAssistant = lipgloss.NewStyle().Foreground(tokyoBg).Background(tokyoPurple).Bold(true).Padding(0, 1)
	roleBadgeAgent = lipgloss.NewStyle().Foreground(tokyoBg).Background(lipgloss.Color("#bb86fc")).Bold(true).Padding(0, 1)
}
