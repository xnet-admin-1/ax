package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type paletteItem struct {
	Name string
	Desc string
}

type paletteModel struct {
	showing  bool
	filter   string
	selected int
	filtered []paletteItem
}

var allCommands = []paletteItem{
	{"/help", "Show help panel"},
	{"/model", "Select AI model"},
	{"/new", "New conversation"},
	{"/list", "List sessions"},
	{"/knowledge", "Search knowledge base"},
	{"/tools", "Toggle tools panel"},
	{"/usage", "Token usage stats"},
	{"/compact", "Compact conversation"},
	{"/clear", "Clear messages"},
	{"/settings", "Edit settings"},
	{"/mcp", "MCP servers"},
	{"/config", "Configuration"},
	{"/provider", "Provider management"},
	{"/memory", "Memory store"},
	{"/remote", "Remote nodes"},
	{"/vectors", "Vector index stats"},
	{"/export", "Export conversation"},
	{"/agent", "Agent builder"},
	{"/monitor", "Monitor agents"},
	{"/attach", "Attach file"},
	{"/status", "System status"},
	{"/fork", "Fork conversation"},
	{"/spawn", "Spawn agent task"},
	{"/theme", "Switch color theme"},
	{"/exit", "Exit application"},
}

func newPalette() paletteModel {
	return paletteModel{filtered: allCommands}
}

func (p *paletteModel) toggle() {
	p.showing = !p.showing
	p.filter = ""
	p.selected = 0
	p.filtered = allCommands
}

func (p *paletteModel) close() {
	p.showing = false
	p.filter = ""
	p.selected = 0
	p.filtered = allCommands
}

func (p *paletteModel) applyFilter() {
	if p.filter == "" {
		p.filtered = allCommands
		p.selected = 0
		return
	}
	lower := strings.ToLower(p.filter)
	p.filtered = nil
	for _, c := range allCommands {
		if strings.Contains(strings.ToLower(c.Name+c.Desc), lower) {
			p.filtered = append(p.filtered, c)
		}
	}
	if p.selected >= len(p.filtered) {
		p.selected = max(0, len(p.filtered)-1)
	}
}

func (p *paletteModel) handleKey(key string) (command string, done bool) {
	switch key {
	case "esc":
		p.close()
		return "", true
	case "up":
		if p.selected > 0 {
			p.selected--
		}
		return "", false
	case "down":
		if p.selected < len(p.filtered)-1 {
			p.selected++
		}
		return "", false
	case "enter":
		if len(p.filtered) > 0 {
			cmd := p.filtered[p.selected].Name
			p.close()
			return cmd, true
		}
		return "", true
	case "backspace":
		if len(p.filter) > 0 {
			p.filter = p.filter[:len(p.filter)-1]
			p.applyFilter()
		}
		return "", false
	default:
		if len(key) == 1 {
			p.filter += key
			p.applyFilter()
		}
		return "", false
	}
}

func (p *paletteModel) view(width, height int) string {
	boxW := 50
	if boxW > width-4 {
		boxW = width - 4
	}
	maxItems := 12
	if maxItems > height-6 {
		maxItems = height - 6
	}

	title := lipgloss.NewStyle().Foreground(tokyoPurple).Bold(true).Render("Command Palette")
	filterLine := lipgloss.NewStyle().Foreground(tokyoFg).Render("> " + p.filter + "█")

	var lines []string
	lines = append(lines, title)
	lines = append(lines, filterLine)
	lines = append(lines, "")

	for i, item := range p.filtered {
		if i >= maxItems {
			break
		}
		name := item.Name
		desc := item.Desc
		if i == p.selected {
			line := lipgloss.NewStyle().Foreground(tokyoCyan).Bold(true).Render(name) +
				"  " + lipgloss.NewStyle().Foreground(tokyoComment).Render(desc)
			lines = append(lines, "▸ "+line)
		} else {
			line := lipgloss.NewStyle().Foreground(tokyoFg).Render(name) +
				"  " + lipgloss.NewStyle().Foreground(tokyoComment).Faint(true).Render(desc)
			lines = append(lines, "  "+line)
		}
	}

	if len(p.filtered) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(tokyoComment).Faint(true).Render("  No matches"))
	}

	content := strings.Join(lines, "\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(tokyoPurple).
		Padding(1, 2).
		Width(boxW).
		Render(content)

	// Center vertically and horizontally
	boxH := lipgloss.Height(box)
	topPad := (height - boxH) / 3
	if topPad < 1 {
		topPad = 1
	}
	leftPad := (width - lipgloss.Width(box)) / 2
	if leftPad < 0 {
		leftPad = 0
	}

	padded := strings.Repeat("\n", topPad) + strings.Repeat(" ", leftPad) + box
	// Pad lines for overlay effect
	result := ""
	for _, l := range strings.Split(padded, "\n") {
		result += strings.Repeat(" ", leftPad) + l + "\n"
	}
	return padded
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
