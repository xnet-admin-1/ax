package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xnet-admin-1/ax/internal/engine"
)

type treeItem struct {
	id    string
	title string
}

type treeLoadedMsg []treeItem

func (m *model) loadTree() tea.Cmd {
	return func() tea.Msg {
		convs, err := m.backend.ListConversations(50)
		if err != nil {
			return errMsg(err)
		}
		items := make([]treeItem, len(convs))
		for i, c := range convs {
			t := c.Title
			if t == "" {
				t = "(untitled)"
			}
			items[i] = treeItem{id: c.ID, title: t}
		}
		return treeLoadedMsg(items)
	}
}

func (m *model) treePanelView() string {
	var b strings.Builder
	b.WriteString("Sessions  enter=switch  d=delete  esc=close\n\n")
	if len(m.treeItems) == 0 {
		b.WriteString("  (no sessions)")
		return b.String()
	}
	for i, item := range m.treeItems {
		prefix := "  "
		connector := "├─ "
		if i == len(m.treeItems)-1 {
			connector = "└─ "
		}
		title := item.title
		if len(title) > 30 {
			title = title[:30]
		}
		line := fmt.Sprintf("%s%s%s", prefix, connector, title)
		if i == m.treeIdx {
			line = "> " + connector + title
		}
		if item.id == m.convID {
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(tokyoCyan).Render(line) + "\n")
		} else if i == m.treeIdx {
			b.WriteString(lipgloss.NewStyle().Foreground(tokyoFg).Render(line) + "\n")
		} else {
			b.WriteString(lipgloss.NewStyle().Faint(true).Render(line) + "\n")
		}
	}
	return b.String()
}

func (m *model) handleTreeEnter() tea.Cmd {
	if len(m.treeItems) == 0 {
		return nil
	}
	item := m.treeItems[m.treeIdx]
	m.panel = panelNone
	return m.loadConv(item.id)
}

// Ensure errMsg is usable (it's already defined elsewhere, this uses engine import)
var _ = engine.Conversation{}

func (m *model) handleTreeDelete() tea.Cmd {
	if len(m.treeItems) == 0 {
		return nil
	}
	item := m.treeItems[m.treeIdx]
	if item.id == "" {
		return nil
	}
	m.backend.GetDB().Exec("DELETE FROM messages WHERE conv_id=?", item.id)
	m.backend.GetDB().Exec("DELETE FROM conversations WHERE id=?", item.id)
	m.treeItems = append(m.treeItems[:m.treeIdx], m.treeItems[m.treeIdx+1:]...)
	if m.treeIdx >= len(m.treeItems) && m.treeIdx > 0 {
		m.treeIdx--
	}
	m.addSystemMsg("Deleted: " + item.title)
	m.updateViewport()
	return nil
}
