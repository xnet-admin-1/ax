package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xnet-admin-1/ax/internal/debug"
)

// dbg is the TUI's reference to the shared logger
var dbg = debug.D

// Debug panel state
var debugLevelIdx int = 0
var debugLevels = []struct {
	name  string
	level debug.Level
}{
	{"info", debug.Info},
	{"warning", debug.Warning},
	{"error", debug.Error},
	{"verbose", debug.Verbose},
}

func (m *model) debugPanelView() string {
	var b strings.Builder
	b.WriteString("Debug Logging")
	b.WriteString("  tab=cycle  enter=on/off  esc=back\n\n")

	status := "OFF"
	statusStyle := lipgloss.NewStyle().Foreground(tokyoRed).Bold(true)
	if dbg.Enabled() {
		status = "ON"
		statusStyle = lipgloss.NewStyle().Foreground(tokyoGreen).Bold(true)
	}
	b.WriteString("  Status: " + statusStyle.Render(status) + "\n")
	b.WriteString("  Level:  ")

	for i, l := range debugLevels {
		if i == debugLevelIdx {
			b.WriteString(panelSelectedStyle.Render("["+l.name+"]") + " ")
		} else {
			b.WriteString(panelLabelStyle.Render(" "+l.name+" ") + " ")
		}
	}
	b.WriteString("\n\n")
	b.WriteString(panelLabelStyle.Render("  Output: /tmp/ax-debug.log"))
	return b.String()
}

func (m *model) handleDebugKey(key string) (bool, tea.Cmd) {
	switch key {
	case "tab":
		debugLevelIdx = (debugLevelIdx + 1) % len(debugLevels)
		return true, nil
	case "enter":
		if dbg.Enabled() {
			dbg.SetLevel(debug.Off)
		} else {
			dbg.SetLevel(debugLevels[debugLevelIdx].level)
		}
		return true, nil
	case "esc":
		m.panel = panelNone
		return true, nil
	}
	return false, nil
}
