package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

var slashCommands = []string{
	// Chat
	"/new", "/list", "/fork", "/clear", "/config",
	// Model/Provider
	"/model", "/provider",
	// Agents
	"/spawn", "/agent", "/monitor",
	// Tools/Config
	"/tools", "/mcp", "/config", "/settings",
	// Data
	"/memory", "/knowledge", "/vectors",
	// Infrastructure
	"/remote", "/status",
	// Utility
	"/attach", "/export", "/compact", "/usage",
	// Mode
	"/mode",
	// Meta
	"/help", "/exit",
}

type inputModel struct {
	ta            textarea.Model
	completions   []string
	compIdx       int
	showComp      bool
	executeOnNext bool
	history       []string
	histIdx       int // -1 = not browsing
}

func newInput() inputModel {
	ta := textarea.New()
	ta.Placeholder = "Ask a question or describe a task ↵"
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	ta.CharLimit = 0
	ta.FocusedStyle.Base = inputStyle
	ta.FocusedStyle.Placeholder = ta.FocusedStyle.Placeholder.Foreground(placeholderColor)
	ta.Focus()
	return inputModel{ta: ta, histIdx: -1}
}

func (i *inputModel) Update(msg tea.Msg) tea.Cmd {
	// Skip textarea update if autocomplete is handling navigation
	if i.showComp {
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "up", "down", "tab", "enter", "esc":
				return nil
			}
		}
	}

	var cmd tea.Cmd
	i.ta, cmd = i.ta.Update(msg)

	// Auto-grow up to 8 lines
	lines := strings.Count(i.ta.Value(), "\n") + 1
	h := lines
	if h < 3 {
		h = 3
	}
	if h > 8 {
		h = 8
	}
	i.ta.SetHeight(h)

	// Slash autocomplete
	val := i.ta.Value()
	if i.histIdx == -1 && strings.HasPrefix(val, "/") && !strings.Contains(val, " ") && len(val) >= 1 {
		i.completions = filterCommands(val)
		if i.compIdx >= len(i.completions) {
			i.compIdx = 0
		}
		i.showComp = len(i.completions) > 0
	} else {
		i.showComp = false
		i.completions = nil
	}

	return cmd
}

func (i *inputModel) HandleCompKey(key string) bool {
	if !i.showComp {
		return false
	}
	switch key {
	case "tab":
		// Tab fills the prompt, doesn't execute
		if len(i.completions) > 0 {
			cmd := i.completions[i.compIdx]
			i.ta.Reset()
			i.ta.SetValue(cmd + " ")
			i.ta.CursorEnd()
			i.showComp = false
			i.completions = nil
		}
		return true
	case "enter":
		// Enter selects AND executes the command
		if len(i.completions) > 0 {
			cmd := i.completions[i.compIdx]
			i.ta.Reset()
			i.ta.SetValue(cmd)
			i.showComp = false
			i.completions = nil
			i.executeOnNext = true
		}
		return true
	case "down":
		if i.compIdx < len(i.completions)-1 {
			i.compIdx++
		}
		return true
	case "up":
		if i.compIdx > 0 {
			i.compIdx--
		}
		return true
	case "esc":
		i.showComp = false
		i.completions = nil
		return true
	}
	return false
}

func (i *inputModel) View() string {
	v := i.ta.View()
	if i.showComp {
		v = i.compView() + v
	}
	val := i.ta.Value()
	charCount := len(val)
	lineCount := strings.Count(val, "\n") + 1
	style := inputStyle
	if charCount > 0 {
		style = inputActiveStyle
	}
	result := style.Render(v)
	if charCount > 0 {
		badge := charCountIndicator(charCount, 4000)
		if lineCount > 1 {
			badge += timestampStyle.Render(" L" + strings.Repeat("", 0) + string(rune(lineCount+48)))
		}
		result += " " + badge
	}
	return result
}

func (i *inputModel) compView() string {
	maxVisible := 8
	if len(i.completions) < maxVisible {
		maxVisible = len(i.completions)
	}
	offset := 0
	if i.compIdx >= maxVisible {
		offset = i.compIdx - maxVisible + 1
	}
	typed := i.ta.Value()
	var b strings.Builder
	end := offset + maxVisible
	if end > len(i.completions) {
		end = len(i.completions)
	}
	for idx := offset; idx < end; idx++ {
		comp := i.completions[idx]
		prefixLen := len(typed)
		if prefixLen > len(comp) {
			prefixLen = len(comp)
		}
		if idx == i.compIdx {
			b.WriteString(compSelectedStyle.Render(" > "+comp) + "\n")
		} else {
			matched := comp[:prefixLen]
			rest := comp[prefixLen:]
			b.WriteString("   " + compMatchStyle.Render(matched) + compNormalStyle.Render(rest) + "\n")
		}
	}
	return b.String()
}

func (i *inputModel) Value() string {
	return strings.TrimSpace(i.ta.Value())
}

func (i *inputModel) Reset() {
	i.ta.Reset()
	i.ta.SetHeight(3)
	i.showComp = false
	i.completions = nil
	i.compIdx = 0
	i.histIdx = -1
}

func (i *inputModel) PushHistory(s string) {
	if s == "" || (len(i.history) > 0 && i.history[len(i.history)-1] == s) {
		return
	}
	i.history = append(i.history, s)
}

func (i *inputModel) HistoryUp() bool {
	if len(i.history) == 0 {
		return false
	}
	if i.ta.Value() != "" && i.histIdx == -1 {
		return false
	}
	if i.histIdx == -1 {
		i.histIdx = len(i.history) - 1
	} else if i.histIdx > 0 {
		i.histIdx--
	}
	i.ta.SetValue(i.history[i.histIdx])
	i.ta.CursorEnd()
	i.showComp = false
	i.completions = nil
	return true
}

func (i *inputModel) HistoryDown() bool {
	if i.histIdx == -1 {
		return false
	}
	if i.histIdx < len(i.history)-1 {
		i.histIdx++
		i.ta.SetValue(i.history[i.histIdx])
		i.ta.CursorEnd()
	} else {
		i.histIdx = -1
		i.ta.Reset()
	}
	i.showComp = false
	i.completions = nil
	return true
}

func (i *inputModel) SetWidth(w int) {
	i.ta.SetWidth(w - 2)
}

func filterCommands(prefix string) []string {
	var out []string
	for _, c := range slashCommands {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

