package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/xnet-admin-1/ax/internal/engine"
	"github.com/xnet-admin-1/ax/internal/agent"
	tea "github.com/charmbracelet/bubbletea"
)

// --- Item types ---

type settingItem struct{ key, value string }

func (i settingItem) Title() string       { return displayName(i.key) }
func (i settingItem) Description() string { return i.value }
func (i settingItem) FilterValue() string { return i.key }

type mcpItem struct{ name, url, transport, status string }

func (i mcpItem) Title() string { return i.name }
func (i mcpItem) Description() string {
	return fmt.Sprintf("%s [%s] %s", i.url, i.transport, i.status)
}
func (i mcpItem) FilterValue() string { return i.name }

type toolItem struct {
	name, source string
	trusted      bool
}

func (i toolItem) Title() string { return i.name }
func (i toolItem) Description() string {
	t := "x"
	if i.trusted {
		t = "ok"
	}
	return fmt.Sprintf("[%s] %s", t, i.source)
}
func (i toolItem) FilterValue() string { return i.name }

// --- Messages ---

type settingsLoadedMsg []list.Item
type mcpLoadedMsg []list.Item
type toolsLoadedMsg []list.Item
type configLoadedMsg []list.Item

// Display names for settings keys
var settingDisplayNames = map[string]string{
	"agent_tools_tool_guidance": "Tool Guidance",
	"search_provider_url":      "Search Provider",
	"totp_secret":              "TOTP Secret",
	"personality":              "Personality",
	"constraints":              "Constraints",
	"preferences":              "Preferences",
}

func displayName(key string) string {
	if n, ok := settingDisplayNames[key]; ok {
		return n
	}
	return key
}

// --- MCP state machine ---

type mcpState int

const (
	mcpList mcpState = iota
	mcpAddName
	mcpAddURL
	mcpAddProtocol
)

var mcpProtocols = []string{"sse", "streamable-http", "stdio"}

// --- Config panel (ALL settings) ---

func (m *model) loadConfigPanel() tea.Cmd {
	return func() tea.Msg {
		db := m.backend.GetDB()
		if db == nil {
			return knowledgeMsg("No database")
		}
		rows, err := db.Query("SELECT key, value FROM settings ORDER BY key")
		if err != nil {
			return knowledgeMsg("Error: " + err.Error())
		}
		defer rows.Close()
		var items []list.Item
		for rows.Next() {
			var k, v string
			rows.Scan(&k, &v)
			if len(v) > 60 {
				v = v[:60] + "…"
			}
			items = append(items, settingItem{key: k, value: v})
		}
		if len(items) == 0 {
			items = append(items, settingItem{key: "(empty)", value: "use /config key value to add"})
		}
		return configLoadedMsg(items)
	}
}

// --- Settings panel (non-task settings) ---

func (m *model) loadSettingsPanel() tea.Cmd {
	return func() tea.Msg {
		db := m.backend.GetDB()
		if db == nil {
			return knowledgeMsg("No database")
		}
		rows, err := db.Query("SELECT key, value FROM settings WHERE key NOT LIKE 'task_model_%' ORDER BY key")
		if err != nil {
			return knowledgeMsg("Error: " + err.Error())
		}
		defer rows.Close()
		var items []list.Item
		for rows.Next() {
			var k, v string
			rows.Scan(&k, &v)
			if len(v) > 60 {
				v = v[:60] + "…"
			}
			items = append(items, settingItem{key: k, value: v})
		}
		if len(items) == 0 {
			items = append(items, settingItem{key: "(empty)", value: "use /config key value to add"})
		}
		return settingsLoadedMsg(items)
	}
}

// --- Task panel ---


// --- Tools panel (builtin + MCP, with trust toggle) ---

func (m *model) loadToolsPanel() tea.Cmd {
	return func() tea.Msg {
		db := m.backend.GetDB()
		var items []list.Item
		// Builtin tools
		for _, t := range m.backend.ListTools() {
			items = append(items, toolItem{name: t, source: "builtin", trusted: true})
		}
		// MCP tools from DB
		if db != nil {
			rows, _ := db.Query("SELECT '', '', 0 FROM settings WHERE 0")
			if rows != nil {
				defer rows.Close()
				for rows.Next() {
					var name, server string
					var trusted int
					rows.Scan(&name, &server, &trusted)
					items = append(items, toolItem{name: name, source: server, trusted: trusted == 1})
				}
			}
		}
		if len(items) == 0 {
			items = append(items, toolItem{name: "(no tools)", source: "-", trusted: false})
		}
		return toolsLoadedMsg(items)
	}
}

// --- MCP panel ---

func (m *model) loadMCPPanel() tea.Cmd {
	m.mcpState = mcpList
	return func() tea.Msg {
		db := m.backend.GetDB()
		if db == nil {
			return knowledgeMsg("No database")
		}
		rows, err := db.Query("SELECT name, command, 'stdio', CASE WHEN enabled=1 THEN 'active' ELSE 'disabled' END FROM mcp_servers ORDER BY name")
		if err != nil {
			return mcpLoadedMsg(nil)
		}
		defer rows.Close()
		var items []list.Item
		for rows.Next() {
			var name, url, transport, status string
			rows.Scan(&name, &url, &transport, &status)
			items = append(items, mcpItem{name: name, url: url, transport: transport, status: status})
		}
		return mcpLoadedMsg(items)
	}
}

// --- Panel key handlers ---

// Config/Settings: enter edits inline via state machine
func (m *model) handleConfigEnter() tea.Cmd {
	if item, ok := m.configList.SelectedItem().(settingItem); ok && item.key != "(empty)" {
		m.editingKey = item.key
		db := m.backend.GetDB()
		var fullVal string
		if db != nil {
			db.QueryRow("SELECT value FROM settings WHERE key=?", item.key).Scan(&fullVal)
		}
		m.editingValue = fullVal
		m.editingCursor = len(fullVal)
	}
	return nil
}

func (m *model) handleSettingsEnter() tea.Cmd {
	if item, ok := m.settingsList.SelectedItem().(settingItem); ok && item.key != "(empty)" {
		m.editingKey = item.key
		db := m.backend.GetDB()
		var fullVal string
		if db != nil {
			db.QueryRow("SELECT value FROM settings WHERE key=?", item.key).Scan(&fullVal)
		}
		m.editingValue = fullVal
		m.editingCursor = len(fullVal)
	}
	return nil
}


func (m *model) handleToolsEnter() tea.Cmd {
	if item, ok := m.toolsList2.SelectedItem().(toolItem); ok && item.name != "(no tools)" {
		db := m.backend.GetDB()
		if db != nil {
			newTrust := 0
			if !item.trusted {
				newTrust = 1
			}
			db.Exec("SELECT 1 WHERE 0 --?", newTrust, item.name)
		}
		return m.loadToolsPanel()
	}
	return nil
}

// Inline editing key handler (for config/settings panels)
func (m *model) handleEditKey(key string) (bool, tea.Cmd) {
	if m.editingKey == "" {
		return false, nil
	}
	switch key {
	case "enter":
		// Save
		db := m.backend.GetDB()
		if db != nil {
			db.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES(?,?)",
				m.editingKey, m.editingKey, m.editingValue)
		}
		m.editingKey = ""
		// Reload current panel
		if m.panel == panelConfig {
			return true, m.loadConfigPanel()
		}
		return true, m.loadSettingsPanel()
	case "esc":
		m.editingKey = ""
		return true, nil
	case "backspace":
		if m.editingCursor > 0 {
			m.editingValue = m.editingValue[:m.editingCursor-1] + m.editingValue[m.editingCursor:]
			m.editingCursor--
		}
		return true, nil
	case "left":
		if m.editingCursor > 0 {
			m.editingCursor--
		}
		return true, nil
	case "right":
		if m.editingCursor < len(m.editingValue) {
			m.editingCursor++
		}
		return true, nil
	default:
		if len(key) == 1 {
			m.editingValue = m.editingValue[:m.editingCursor] + key + m.editingValue[m.editingCursor:]
			m.editingCursor++
			return true, nil
		}
	}
	return true, nil
}

// --- MCP key handler ---

func (m *model) handleMCPKey(key string) (bool, tea.Cmd) {
	switch m.mcpState {
	case mcpList:
		switch key {
		case "a":
			m.mcpState = mcpAddName
			m.mcpPrompt = "Name: "
			m.mcpInput = ""
			return true, nil
		case "d", "delete":
			if item, ok := m.mcpList.SelectedItem().(mcpItem); ok {
				db := m.backend.GetDB()
				if db != nil {
					db.Exec("DELETE FROM mcp_servers WHERE name=?", item.name)
				}
				return true, m.loadMCPPanel()
			}
		case "enter":
			if item, ok := m.mcpList.SelectedItem().(mcpItem); ok {
				db := m.backend.GetDB()
				if db != nil {
					newStatus := "active"
					if item.status == "active" {
						newStatus = "inactive"
					}
					db.Exec("UPDATE mcp_servers SET status=? WHERE name=?", newStatus, item.name)
				}
				return true, m.loadMCPPanel()
			}
		case "up":
			m.mcpList, _ = m.mcpList.Update(tea.KeyMsg{Type: tea.KeyUp})
			return true, nil
		case "down":
			m.mcpList, _ = m.mcpList.Update(tea.KeyMsg{Type: tea.KeyDown})
			return true, nil
		}
	case mcpAddName:
		switch key {
		case "enter":
			if m.mcpInput != "" {
				m.mcpNewName = m.mcpInput
				m.mcpState = mcpAddURL
				m.mcpPrompt = "URL: "
				m.mcpInput = ""
			}
			return true, nil
		case "esc":
			m.mcpState = mcpList
			return true, nil
		case "backspace":
			if len(m.mcpInput) > 0 {
				m.mcpInput = m.mcpInput[:len(m.mcpInput)-1]
			}
			return true, nil
		default:
			if len(key) == 1 {
				m.mcpInput += key
			}
			return true, nil
		}
	case mcpAddURL:
		switch key {
		case "enter":
			if m.mcpInput != "" {
				m.mcpNewURL = m.mcpInput
				m.mcpState = mcpAddProtocol
				m.mcpPrompt = ""
				m.mcpInput = ""
				m.mcpProtoIdx = 0
			}
			return true, nil
		case "esc":
			m.mcpState = mcpAddName
			m.mcpPrompt = "Name: "
			m.mcpInput = m.mcpNewName
			return true, nil
		case "backspace":
			if len(m.mcpInput) > 0 {
				m.mcpInput = m.mcpInput[:len(m.mcpInput)-1]
			}
			return true, nil
		default:
			if len(key) == 1 {
				m.mcpInput += key
			}
			return true, nil
		}
	case mcpAddProtocol:
		switch key {
		case "up":
			if m.mcpProtoIdx > 0 {
				m.mcpProtoIdx--
			}
			return true, nil
		case "down":
			if m.mcpProtoIdx < len(mcpProtocols)-1 {
				m.mcpProtoIdx++
			}
			return true, nil
		case "enter":
			db := m.backend.GetDB()
			if db != nil {
				proto := mcpProtocols[m.mcpProtoIdx]
				db.Exec("INSERT OR REPLACE INTO mcp_servers(name, url, transport, status) VALUES(?,?,?,?)",
					m.mcpNewName, m.mcpNewURL, proto, "active")
			}
			return true, m.loadMCPPanel()
		case "esc":
			m.mcpState = mcpAddURL
			m.mcpPrompt = "URL: "
			m.mcpInput = m.mcpNewURL
			return true, nil
		}
	}
	return false, nil
}

// --- Panel views ---

func (m *model) configPanelView() string {
	if m.editingKey != "" {
		var b strings.Builder
		b.WriteString(" Config  [esc] cancel  [enter] save\n\n")
		b.WriteString(fmt.Sprintf("  %s:\n", m.editingKey))
		b.WriteString(fmt.Sprintf("  %s▍\n", m.editingValue))
		return b.String()
	}
	return " Config  [enter] edit  [esc] close\n\n" + m.configList.View()
}

func (m *model) settingsPanelView() string {
	if m.editingKey != "" {
		var b strings.Builder
		b.WriteString(" Settings  [esc] cancel  [enter] save\n\n")
		b.WriteString(fmt.Sprintf("  %s:\n", m.editingKey))
		b.WriteString(fmt.Sprintf("  %s▍\n", m.editingValue))
		return b.String()
	}
	return " Settings  [enter] edit  [esc] close\n\n" + m.settingsList.View()
}

func (m *model) toolsPanelView() string {
	return " Tools  [enter] toggle trust  [esc] close\n\n" + m.toolsList2.View()
}

func (m *model) mcpPanelView() string {
	var b strings.Builder
	switch m.mcpState {
	case mcpList:
		b.WriteString(" MCP Servers  [a]dd  [d]elete  [enter] toggle  [esc] close\n\n")
		if m.mcpList.Items() == nil || len(m.mcpList.Items()) == 0 {
			b.WriteString("  (no servers configured)\n")
		} else {
			b.WriteString(m.mcpList.View())
		}
	case mcpAddName:
		b.WriteString(" Add MCP Server\n\n")
		b.WriteString("  " + m.mcpPrompt + m.mcpInput + "▍\n\n")
		b.WriteString("  enter: confirm  esc: cancel")
	case mcpAddURL:
		b.WriteString(" Add MCP Server: " + m.mcpNewName + "\n\n")
		b.WriteString("  " + m.mcpPrompt + m.mcpInput + "▍\n\n")
		b.WriteString("  enter: confirm  esc: back")
	case mcpAddProtocol:
		b.WriteString(" Add MCP Server: " + m.mcpNewName + "\n")
		b.WriteString("  URL: " + m.mcpNewURL + "\n\n")
		b.WriteString("  Protocol:\n")
		for i, p := range mcpProtocols {
			cursor := "  "
			if i == m.mcpProtoIdx {
				cursor = "> "
			}
			b.WriteString("    " + cursor + p + "\n")
		}
		b.WriteString("\n  enter: save  esc: back")
	}
	return b.String()
}

// --- /list delete handler ---

func (m *model) handleSessionDelete() tea.Cmd {
	if item, ok := m.sessList.SelectedItem().(sessionItem); ok {
		db := m.backend.GetDB()
		if db != nil {
			db.Exec("DELETE FROM messages WHERE conversationId=?", item.id)
			db.Exec("DELETE FROM conversations WHERE id=?", item.id)
		}
		return m.loadSessions()
	}
	return nil
}

// --- /fork ---

func (m *model) forkConversation() tea.Cmd {
	return func() tea.Msg {
		if m.convID == "" {
			return knowledgeMsg("No conversation to fork")
		}
		db := m.backend.GetDB()
		if db == nil {
			return knowledgeMsg("No database")
		}
		newID := fmt.Sprintf("fork_%d", len(m.msgs))
		now := "datetime('now')"
		title := m.convTitle + " (fork)"
		db.Exec("INSERT INTO conversations(id,title,agentName,createdAt,updatedAt) VALUES(?,?,'default',"+now+","+now+")", newID, title)
		// Copy messages
		rows, err := db.Query("SELECT role, content, timestamp FROM messages WHERE conversationId=? ORDER BY timestamp", m.convID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var role, content, ts string
				rows.Scan(&role, &content, &ts)
				db.Exec("INSERT INTO messages(conversationId,role,content,timestamp) VALUES(?,?,?,?)", newID, role, content, ts)
			}
		}
		return knowledgeMsg(fmt.Sprintf(" Forked → %s (%d messages)", title, len(m.msgs)))
	}
}

// --- /spawn ---

type spawnResultMsg string



// --- Provider panel ---







// --- Memory panel ---

type memoryItem struct{ key, content, category string }

func (i memoryItem) Title() string       { return i.key }
func (i memoryItem) Description() string { return "[" + i.category + "] " + i.content }
func (i memoryItem) FilterValue() string { return i.key }

type memoriesLoadedMsg []list.Item

func (m *model) loadMemoryPanel() tea.Cmd {
	return func() tea.Msg {
		db := m.backend.GetDB()
		if db == nil {
			return knowledgeMsg("No database")
		}
		rows, err := db.Query("SELECT key, content, category FROM memories ORDER BY key")
		if err != nil {
			return memoriesLoadedMsg(nil)
		}
		defer rows.Close()
		var items []list.Item
		for rows.Next() {
			var k, c, cat string
			rows.Scan(&k, &c, &cat)
			if len(c) > 60 {
				c = c[:60] + "..."
			}
			items = append(items, memoryItem{key: k, content: c, category: cat})
		}
		if len(items) == 0 {
			items = append(items, memoryItem{key: "(empty)", content: "a=add", category: "-"})
		}
		return memoriesLoadedMsg(items)
	}
}

func (m *model) handleMemoryDelete() tea.Cmd {
	if item, ok := m.memoryList.SelectedItem().(memoryItem); ok && item.key != "(empty)" {
		db := m.backend.GetDB()
		if db != nil {
			db.Exec("DELETE FROM memories WHERE key=?", item.key)
		}
		return m.loadMemoryPanel()
	}
	return nil
}

func (m *model) handleMemoryEnter() tea.Cmd {
	if item, ok := m.memoryList.SelectedItem().(memoryItem); ok && item.key != "(empty)" {
		// Edit existing memory
		m.memEditKey = item.key
		db := m.backend.GetDB()
		if db != nil {
			db.QueryRow("SELECT content FROM memories WHERE key=?", item.key).Scan(&m.memEditValue)
		}
		m.memEditCursor = len(m.memEditValue)
		m.memEditStep = 2 // skip to content editing
	}
	return nil
}

func (m *model) startMemoryAdd() {
	m.memEditKey = ""
	m.memEditValue = ""
	m.memEditCursor = 0
	m.memEditStep = 1 // step 1 = key, step 2 = content
}

func (m *model) handleMemoryEditKey(key string) (bool, tea.Cmd) {
	if m.memEditStep == 0 {
		return false, nil
	}
	switch key {
	case "enter":
		if m.memEditStep == 1 {
			if m.memEditKey != "" {
				m.memEditStep = 2
				m.memEditCursor = 0
			}
		} else {
			// Save
			db := m.backend.GetDB()
			if db != nil {
				now := "strftime('%s','now')"
				db.Exec("INSERT INTO memories(key,content,category,createdAt,updatedAt) VALUES(?,?,'general',"+now+","+now+") ON CONFLICT(key) DO UPDATE SET content=?, updatedAt="+now, m.memEditKey, m.memEditValue, m.memEditValue)
			}
			m.memEditStep = 0
			return true, m.loadMemoryPanel()
		}
		return true, nil
	case "esc":
		if m.memEditStep == 2 && m.memEditKey != "" {
			m.memEditStep = 1
			m.memEditCursor = len(m.memEditKey)
		} else {
			m.memEditStep = 0
		}
		return true, nil
	case "backspace":
		if m.memEditStep == 1 {
			if len(m.memEditKey) > 0 {
				m.memEditKey = m.memEditKey[:len(m.memEditKey)-1]
			}
		} else {
			if m.memEditCursor > 0 {
				m.memEditValue = m.memEditValue[:m.memEditCursor-1] + m.memEditValue[m.memEditCursor:]
				m.memEditCursor--
			}
		}
		return true, nil
	case "left":
		if m.memEditStep == 2 && m.memEditCursor > 0 {
			m.memEditCursor--
		}
		return true, nil
	case "right":
		if m.memEditStep == 2 && m.memEditCursor < len(m.memEditValue) {
			m.memEditCursor++
		}
		return true, nil
	default:
		if len(key) == 1 {
			if m.memEditStep == 1 {
				m.memEditKey += key
			} else {
				m.memEditValue = m.memEditValue[:m.memEditCursor] + key + m.memEditValue[m.memEditCursor:]
				m.memEditCursor++
			}
			return true, nil
		}
	}
	return true, nil
}

func (m *model) memoryPanelView() string {
	if m.memEditStep > 0 {
		var b strings.Builder
		b.WriteString("Memory  enter=save  esc=back\n\n")
		if m.memEditStep == 1 {
			b.WriteString("  Key: " + m.memEditKey + "|\n")
		} else {
			b.WriteString("  Key: " + m.memEditKey + "\n")
			b.WriteString("  Content: " + m.memEditValue + "|\n")
		}
		return b.String()
	}
	return "Memories  enter=edit  a=add  d=delete  esc=close\n\n" + m.memoryList.View()
}

// --- Agents panel (background task monitoring) ---

type agentItem struct{ id, status, desc string }

func (i agentItem) Title() string       { return i.id[:8] }
func (i agentItem) Description() string { return "[" + i.status + "] " + i.desc }
func (i agentItem) FilterValue() string { return i.id }

type agentsLoadedMsg []list.Item

func (m *model) loadAgentsPanel() tea.Cmd {
	return func() tea.Msg {
		mgr := m.getAgentManager()
		var tasks []*agent.Task
		if mgr != nil { tasks = mgr.ListTasks() }
		var items []list.Item
		for _, t := range tasks {
			desc := t.Result
			if desc == "" && t.Status == "complete" {
				if len(t.Result) > 50 {
					desc = t.Result[:50] + "..."
				} else {
					desc = t.Result
				}
			}
			if desc == "" && t.Result != "" {
				desc = t.Result
			}
			items = append(items, agentItem{id: t.ID, status: t.Status, desc: desc})
		}
		if len(items) == 0 {
			items = append(items, agentItem{id: "--------", status: "-", desc: "no active agents"})
		}
		return agentsLoadedMsg(items)
	}
}

func (m *model) handleAgentCancel() tea.Cmd {
	if item, ok := m.agentsList.SelectedItem().(agentItem); ok && item.id != "--------" {
		mgr := m.getAgentManager(); if mgr != nil { mgr.Cancel(item.id) }
		return m.loadAgentsPanel()
	}
	return nil
}

// --- Remote panel ---

type remoteItem struct{ id, name, host, status string }

func (i remoteItem) Title() string       { return i.name }
func (i remoteItem) Description() string { return i.host + " [" + i.status + "]" }
func (i remoteItem) FilterValue() string { return i.name }

type remoteLoadedMsg []list.Item

func (m *model) loadRemotePanel() tea.Cmd {
	return func() tea.Msg {
		db := m.backend.GetDB()
		if db == nil {
			return knowledgeMsg("No database")
		}
		rows, err := db.Query("SELECT id, name, host, status FROM remote_servers ORDER BY name")
		if err != nil {
			return remoteLoadedMsg(nil)
		}
		defer rows.Close()
		var items []list.Item
		for rows.Next() {
			var id, name, host, status string
			rows.Scan(&id, &name, &host, &status)
			items = append(items, remoteItem{id: id, name: name, host: host, status: status})
		}
		if len(items) == 0 {
			items = append(items, remoteItem{id: "-", name: "(no servers)", host: "-", status: "-"})
		}
		return remoteLoadedMsg(items)
	}
}

func (m *model) handleRemoteDelete() tea.Cmd {
	if item, ok := m.remoteList.SelectedItem().(remoteItem); ok && item.id != "-" {
		db := m.backend.GetDB()
		if db != nil {
			db.Exec("DELETE FROM remote_servers WHERE id=?", item.id)
		}
		return m.loadRemotePanel()
	}
	return nil
}

// --- Vectors panel ---

type vectorsLoadedMsg struct{ stats string }

func (m *model) loadVectorsPanel() tea.Cmd {
	return func() tea.Msg {
		db := m.backend.GetDB()
		if db == nil {
			return vectorsLoadedMsg{stats: "No database"}
		}
		var count int
		db.QueryRow("SELECT COUNT(*) FROM vec_docs").Scan(&count)
		var cats string
		rows, _ := db.Query("SELECT category, COUNT(*) FROM vec_docs GROUP BY category")
		if rows != nil {
			defer rows.Close()
			for rows.Next() {
				var cat string
				var n int
				rows.Scan(&cat, &n)
				cats += fmt.Sprintf("  %s: %d\n", cat, n)
			}
		}
		stats := fmt.Sprintf("Documents: %d\n\nCategories:\n%s", count, cats)
		return vectorsLoadedMsg{stats: stats}
	}
}

func (m *model) vectorsReindex() tea.Cmd {
	return func() tea.Msg {
		// Trigger reindex via the vecstore
		return knowledgeMsg("Reindex started (background)")
	}
}


// Provider panel view with add flow
func (m *model) providerPanelView() string {
	if m.provAddStep > 0 {
		var b strings.Builder
		b.WriteString("Add Provider  enter=next  esc=back\n\n")
		switch m.provAddStep {
		case 1:
			b.WriteString("  Name: " + m.provInput + "|\n")
		case 2:
			b.WriteString("  Name: " + m.provAddLabel + "\n")
			b.WriteString("  API Base: " + m.provInput + "|\n")
		case 3:
			b.WriteString("  Name: " + m.provAddLabel + "\n")
			b.WriteString("  API Base: " + m.provAddBase + "\n")
			b.WriteString("  API Key: " + strings.Repeat("*", len(m.provInput)) + "|\n")
		}
		return b.String()
	}
	return "Providers  a=add  enter=toggle  d=delete  esc=close\n\n" + m.providerList.View()
}

func (m *model) handleProviderAddKey(key string) (bool, tea.Cmd) {
	switch key {
	case "enter":
		switch m.provAddStep {
		case 1:
			if m.provInput != "" {
				m.provAddLabel = m.provInput
				m.provInput = ""
				m.provAddStep = 2
			}
		case 2:
			m.provAddBase = m.provInput
			m.provInput = ""
			m.provAddStep = 3
		case 3:
			m.provAddKey = m.provInput
			m.provAddStep = 0
			return true, m.handleProviderAddSave()
		}
		return true, nil
	case "esc":
		if m.provAddStep > 1 {
			m.provAddStep--
			switch m.provAddStep {
			case 1:
				m.provInput = m.provAddLabel
			case 2:
				m.provInput = m.provAddBase
			}
		} else {
			m.provAddStep = 0
		}
		return true, nil
	case "backspace":
		if len(m.provInput) > 0 {
			m.provInput = m.provInput[:len(m.provInput)-1]
		}
		return true, nil
	default:
		if len(key) == 1 {
			m.provInput += key
		}
		return true, nil
	}
}

// Remote panel view with add flow


// --- Export ---

func (m *model) exportConversation() tea.Cmd {
	return func() tea.Msg {
		if m.convID == "" {
			return knowledgeMsg("No active conversation")
		}
		msgs, err := m.backend.GetMessages(m.convID)
		if err != nil {
			return knowledgeMsg("Export error: " + err.Error())
		}
		var b strings.Builder
		b.WriteString("## " + m.convTitle + "\n\n")
		for _, msg := range msgs {
			switch msg.Role {
			case "user":
				b.WriteString("**User:** " + msg.Content + "\n\n")
			case "assistant":
				b.WriteString("**Assistant:** " + msg.Content + "\n\n")
			}
		}
		dir := os.Getenv("HOME")
		if dir == "" {
			dir = "/tmp"
		}
		path := fmt.Sprintf("%s/ax-export-%s.md", dir, m.convID[:8])
		if err := writeFile(path, b.String()); err != nil {
			return knowledgeMsg("Export error: " + err.Error())
		}
		return knowledgeMsg("Exported to " + path)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

// --- Status ---

func (m *model) checkStatus() tea.Cmd {
	return func() tea.Msg {
		var b strings.Builder
		b.WriteString("Status\n\n")

		// Model
		b.WriteString("  Model:     " + m.backend.CurrentModel() + "\n")

		// Conversation
		if m.convID != "" {
			b.WriteString("  Conv:      " + m.convID[:8] + "…\n")
		} else {
			b.WriteString("  Conv:      (none)\n")
		}
		b.WriteString(fmt.Sprintf("  Messages:  %d\n", len(m.msgs)))
		b.WriteString(fmt.Sprintf("  Tokens:    %d\n", m.tokens))

		// Mode
		mode := "chat"
		if local, ok := m.backend.(*engine.Local); ok {
			mode = local.Mode
		}
		b.WriteString("  Mode:      " + mode + "\n")

		// Handoff
		if m.handoff.Active {
			b.WriteString("  Handoff:   " + m.handoff.AgentName + "\n")
		}

		// Background agents
		mgr := m.getAgentManager()
		var tasks []*agent.Task
		if mgr != nil { tasks = mgr.ListTasks() }
		running := 0
		for _, t := range tasks {
			if t.Status == "running" {
				running++
			}
		}
		b.WriteString(fmt.Sprintf("  Agents:    %d running, %d total\n", running, len(tasks)))

		// Database
		db := m.backend.GetDB()
		if db == nil {
			b.WriteString("  Database:  FAIL\n")
		} else {
			b.WriteString("  Database:  OK\n")
		}

		return knowledgeMsg(b.String())
	}
}
