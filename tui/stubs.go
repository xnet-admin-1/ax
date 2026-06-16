package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/xnet-admin-1/ax/internal/agent"
	"github.com/xnet-admin-1/ax/internal/engine"
	"github.com/xnet-admin-1/ax/internal/gateway"
	"github.com/xnet-admin-1/ax/internal/knowledge"
	"github.com/xnet-admin-1/ax/internal/provider"
	tea "github.com/charmbracelet/bubbletea"
)

type handoffState struct {
	Active    bool
	AgentName string
}

type agentBuilderItem struct {
	name  string
	model string
	tools int
}

func (i agentBuilderItem) Title() string       { return i.name }
func (i agentBuilderItem) Description() string { return fmt.Sprintf("model=%s tools=%d", i.model, i.tools) }
func (i agentBuilderItem) FilterValue() string { return i.name }

func truncStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

type agentDoneMsg struct{}
type pendingHandoffMsg struct{ Agent, Message string }
type toolCheck struct {
	Command string
	Reason  string
	Allow   chan bool
}
type gwProvidersLoadedMsg []list.Item
type spawnLoadedMsg []list.Item
type agentListLoadedMsg []list.Item
type agentBuilderLoadedMsg []list.Item
type pollAgainMsg struct{}

// providerItem implements list.Item for the provider list.
type providerItem struct{ name, base, status string }

func (i providerItem) Title() string       { return i.name }
func (i providerItem) Description() string { return fmt.Sprintf("%s [%s]", i.base, i.status) }
func (i providerItem) FilterValue() string { return i.name }

func (m *model) loadProviderPanelFixed() tea.Cmd {
	return func() tea.Msg {
		db := m.backend.GetDB()
		if db == nil {
			return gwProvidersLoadedMsg(nil)
		}
		svc := &provider.Service{DB: db}
		providers, err := svc.List()
		if err != nil {
			return gwProvidersLoadedMsg(nil)
		}
		var items []list.Item
		for _, p := range providers {
			status := "disabled"
			if p.Enabled {
				status = "enabled"
			}
			items = append(items, providerItem{name: p.Name, base: p.APIBase, status: status})
		}
		if len(items) == 0 {
			items = append(items, providerItem{name: "(none)", base: "a=add", status: "-"})
		}
		return gwProvidersLoadedMsg(items)
	}
}

func (m *model) handleProviderAddSave() tea.Cmd {
	db := m.backend.GetDB()
	if db == nil {
		return nil
	}
	svc := &provider.Service{DB: db}
	p := &provider.Provider{
		Name:    m.provAddLabel,
		APIKey:  m.provAddKey,
		APIBase: m.provAddBase,
		Enabled: true,
	}
	svc.Save(p)
	return m.loadProviderPanelFixed()
}

func (m *model) handleProviderToggle() tea.Cmd {
	item, ok := m.providerList.SelectedItem().(providerItem)
	if !ok || item.name == "(none)" {
		return nil
	}
	db := m.backend.GetDB()
	if db == nil {
		return nil
	}
	svc := &provider.Service{DB: db}
	svc.Toggle(item.name)
	return m.loadProviderPanelFixed()
}

func (m *model) handleProviderDeleteFixed() tea.Cmd {
	item, ok := m.providerList.SelectedItem().(providerItem)
	if !ok || item.name == "(none)" {
		return nil
	}
	db := m.backend.GetDB()
	if db == nil {
		return nil
	}
	svc := &provider.Service{DB: db}
	svc.Delete(item.name)
	return m.loadProviderPanelFixed()
}

func (m *model) spawnPanelView() string {
	if m.spawnTaskInput {
		var b strings.Builder
		b.WriteString("Spawn: " + m.spawnAgentName + "  enter=go  esc=back\n\n")
		b.WriteString("  Task: " + m.spawnTaskBuf + "|\n\n")
		userBox := "(*)"
		agentBox := "( )"
		if m.spawnReportToAgent {
			userBox = "( )"
			agentBox = "(*)"
		}
		b.WriteString(fmt.Sprintf("  Report to:  %s User  %s Primary Agent\n", userBox, agentBox))
		b.WriteString("              tab=toggle\n")
		return b.String()
	}
	return "Spawn  enter=select  esc=close\n\n" + m.spawnList.View()
}

func (m *model) getAgentManager() *agent.Manager {
	if mgr, ok := m.backend.GetAgentManager().(*agent.Manager); ok {
		return mgr
	}
	return nil
}

func (m *model) pollSpawnResults() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(_ time.Time) tea.Msg {
		return pollAgainMsg{}
	})
}

func (m *model) deliverPendingReports() (tea.Model, tea.Cmd) {
	mgr := m.getAgentManager()
	if mgr == nil {
		return m, nil
	}
	for _, t := range mgr.ListTasks() {
		if (t.Status == "done" || t.Status == "error") && !t.Internal {
			if _, seen := m.reportedTasks[t.ID]; !seen {
				m.reportedTasks[t.ID] = 1
				label := "[" + t.Agent + "] done"
				if t.Status == "error" {
					label = "[" + t.Agent + "] error"
				}
				result := t.Result
				display := result
				if len(display) > 200 {
					display = display[:200] + "..."
				}

				if t.ReportTo == "agent" {
					// Feed back to main agent as context
					prompt := fmt.Sprintf("[Agent %s completed]\n\n%s\n\nPresent these findings to the user.", t.Agent, result)
					m.msgs = append(m.msgs, chatMsg{role: "tool_result", content: label})
					m.cachedMsgCount = 0
					if !m.streaming {
						return m, m.startChat(prompt)
					}
				} else {
					// Report to user directly
					m.msgs = append(m.msgs, chatMsg{role: "tool_result", content: label + "\n" + display})
					m.addSystemMsg(label + " - /monitor for full result")
					m.cachedMsgCount = 0
					m.updateViewport()
				}
			}
		}
	}
	for _, t := range mgr.ListTasks() {
		if t.Status == "running" {
			return m, m.pollSpawnResults()
		}
	}
	return m, nil
}

func (m *model) loadAgentBuilderPanel() tea.Cmd {
	return func() tea.Msg {
		mgr := m.getAgentManager()
		if mgr == nil {
			return agentBuilderLoadedMsg(nil)
		}
		roster := mgr.GetRoster()
		var items []list.Item
		for _, a := range roster {
			items = append(items, agentBuilderItem{name: a.Name, model: a.Model, tools: len(a.Tools)})
		}
		if len(items) == 0 {
			items = append(items, agentBuilderItem{name: "(empty)", model: "a=add", tools: 0})
		}
		return agentBuilderLoadedMsg(items)
	}
}

func (m *model) handleHandoff(name string) tea.Cmd {
	mgr := m.getAgentManager()
	if mgr == nil {
		return nil
	}
	// Gather recent conversation as task context
	var task string
	for _, msg := range m.msgs {
		if msg.role == "user" {
			task = msg.content
		}
	}
	if task == "" {
		task = "Continue the conversation."
	}
	id, err := mgr.Spawn(name, task)
	if err != nil {
		m.msgs = append(m.msgs, chatMsg{role: "system", content: "handoff error: " + err.Error()})
		m.cachedRender = ""
		return nil
	}
	_ = id
	return m.pollSpawnResults()
}

func (m *model) handleReturn()                                             {}

func (m *model) chainAgents(args []string, task string) tea.Cmd {
	mgr := m.getAgentManager()
	if mgr == nil || len(args) == 0 {
		return nil
	}
	go func() {
		input := task
		for _, name := range args {
			id, err := mgr.Spawn(name, input)
			if err != nil {
				return
			}
			// Wait for completion
			for {
				t := mgr.GetTask(id)
				if t != nil && (t.Status == "done" || t.Status == "error") {
					input = t.Result
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
		}
	}()
	return m.pollSpawnResults()
}

func (m *model) spawnParallel(args []string, task string) tea.Cmd {
	mgr := m.getAgentManager()
	if mgr == nil {
		return nil
	}
	for _, name := range args {
		mgr.Spawn(name, task)
	}
	return m.pollSpawnResults()
}
func (m *model) loadSpawnPanel() tea.Cmd {
	return func() tea.Msg {
		mgr := m.getAgentManager()
		if mgr == nil {
			return spawnLoadedMsg(nil)
		}
		roster := mgr.GetRoster()
		var items []list.Item
		for _, a := range roster {
			items = append(items, agentBuilderItem{name: a.Name, model: a.Model, tools: len(a.Tools)})
		}
		if len(items) == 0 {
			items = append(items, agentBuilderItem{name: "(no agents)", model: "use /agent to create", tools: 0})
		}
		return spawnLoadedMsg(items)
	}
}
func (m *model) remotePanelViewFixed() string {
	return "Remote  enter=connect  h=health  d=delete  a=add  esc=close\n\n" + m.remoteList.View()
}
func (m *model) agentBuilderView() string {
	if m.agentBuildStep > 0 {
		var b strings.Builder
		b.WriteString("Agent Builder  enter=next  esc=cancel\n\n")
		switch m.agentBuildStep {
		case 1:
			b.WriteString("  Name: " + m.agentBuildInput + "|\n")
		case 2:
			b.WriteString("  Name: " + m.agentBuildName + "\n")
			b.WriteString("  Model: " + m.agentBuildInput + "|\n")
		case 3:
			b.WriteString("  Name: " + m.agentBuildName + "\n")
			b.WriteString("  System Prompt: " + m.agentBuildInput + "|\n")
		}
		return b.String()
	}
	if m.agentViewState == 1 {
		// Detail view
		mgr := m.getAgentManager()
		if mgr == nil {
			return "Agent detail (no db)"
		}
		roster := mgr.GetRoster()
		if m.agentDetailIdx >= len(roster) {
			return "Agent not found"
		}
		a := roster[m.agentDetailIdx]
		var b strings.Builder
		b.WriteString("Agent Detail  e=edit field  esc=back\n\n")
		fields := []string{
			fmt.Sprintf("  Name:   %s", a.Name),
			fmt.Sprintf("  Model:  %s", a.Model),
			fmt.Sprintf("  Tools:  %v", a.Tools),
			fmt.Sprintf("  Prompt: %s", truncStr(a.SystemPrompt, 60)),
		}
		for i, f := range fields {
			cursor := "  "
			if i == m.agentFieldIdx {
				cursor = "> "
			}
			b.WriteString(cursor + f + "\n")
		}
		return b.String()
	}
	return "Agents  enter=view  a=add  d=delete  esc=close\n\n" + m.agentBuilderList.View()
}
func (m *model) agentsTreeView() string {
	mgr := m.getAgentManager()
	if mgr == nil {
		return "Agents Monitor  esc=close\n\n  (no agent manager)"
	}
	// Detail view for specific task
	if m.agentLogID != "" {
		t := mgr.GetTask(m.agentLogID)
		if t == nil {
			m.agentLogID = ""
		} else {
			var b strings.Builder
			status := t.Status
			if status == "running" {
				status = "running " + getSpinnerFrame(int(time.Since(t.StartedAt).Seconds()))
			}
			b.WriteString(fmt.Sprintf("[%s] %s  esc=back  k=kill\n", t.Agent, status))
			b.WriteString(fmt.Sprintf("  ID: %s  Elapsed: %s\n", t.ID[:12], time.Since(t.StartedAt).Truncate(time.Second)))
			b.WriteString("  " + strings.Repeat("-", 40) + "\n")
			// Show live log with better formatting
			log := t.GetLog()
			for _, ev := range log {
				switch ev.Type {
				case "delta":
					text := ev.Text
					if len(text) > 500 {
						text = text[:500] + "..."
					}
					// Wrap lines
					for _, line := range strings.Split(text, "\n") {
						b.WriteString("  " + line + "\n")
					}
				case "tool_call":
					b.WriteString("\n  >> " + ev.Text + "\n")
				case "tool_result":
					text := ev.Text
					if len(text) > 300 {
						text = text[:300] + "..."
					}
					lines := strings.Split(text, "\n")
					for i, line := range lines {
						if i > 8 {
							b.WriteString("     ... (" + fmt.Sprintf("%d", len(lines)-8) + " more lines)\n")
							break
						}
						b.WriteString("     " + line + "\n")
					}
					b.WriteString("\n")
				case "done":
					b.WriteString("\n  -- COMPLETED --\n")
				}
			}
			if t.Status == "running" {
				b.WriteString("\n  ... working ...\n")
			}
			return b.String()
		}
	}
	// List view
	tasks := mgr.ListTasks()
	if len(tasks) == 0 {
		return "Agents Monitor  enter=view  k=kill  esc=close\n\n  (no active tasks)"
	}
	var b strings.Builder
	b.WriteString("Agents Monitor  enter=view  k=kill  esc=close\n\n")
	for i, t := range tasks {
		cursor := "  "
		if i == m.agentsIdx {
			cursor = "> "
		}
		// Status indicator
		status := "[done]"
		switch t.Status {
		case "running":
			status = "[" + getSpinnerFrame(int(time.Since(t.StartedAt).Seconds())) + "]"
		case "error":
			status = "[ERR]"
		}
		// Description
		desc := t.Agent
		logEntries := t.GetLog()
		if len(logEntries) > 0 {
			last := logEntries[len(logEntries)-1]
			switch last.Type {
			case "tool_call":
				desc += " > " + last.Text
			case "delta":
				txt := last.Text
				if len(txt) > 40 {
					txt = txt[:40] + "..."
				}
				desc += ": " + strings.ReplaceAll(txt, "\n", " ")
			}
		}
		if len(desc) > 60 {
			desc = desc[:60] + "..."
		}
		elapsed := time.Since(t.StartedAt).Truncate(time.Second).String()
		b.WriteString(fmt.Sprintf("%s%s %s (%s)\n", cursor, status, desc, elapsed))
	}
	return b.String()
}

func getSpinnerFrame(tick int) string {
	frames := []string{".", "..", "...", ".."}
	return frames[tick%len(frames)]
}
func (m *model) handleAgentDone(msg agentDoneMsg) (tea.Model, tea.Cmd) {
	return m.deliverPendingReports()
}
func (m *model) handleAgentBuildKey(key string) (bool, tea.Cmd) {
	switch key {
	case "enter":
		switch m.agentBuildStep {
		case 1:
			if m.agentBuildInput != "" {
				m.agentBuildName = m.agentBuildInput
				m.agentBuildInput = ""
				m.agentBuildStep = 2
			}
		case 2:
			m.agentEditBuf = m.agentBuildInput // store model
			m.agentBuildInput = ""
			m.agentBuildStep = 3
		case 3:
			// Save new agent
			mgr := m.getAgentManager()
			if mgr != nil {
				roster := mgr.GetRoster()
				roster = append(roster, agent.Agent{Name: m.agentBuildName, Model: m.agentEditBuf, SystemPrompt: m.agentBuildInput})
				mgr.SaveRoster(roster)
			}
			m.agentBuildStep = 0
			m.agentBuildInput = ""
			return true, m.loadAgentBuilderPanel()
		}
		return true, nil
	case "esc":
		m.agentBuildStep = 0
		m.agentBuildInput = ""
		return true, nil
	case "backspace":
		if len(m.agentBuildInput) > 0 {
			m.agentBuildInput = m.agentBuildInput[:len(m.agentBuildInput)-1]
		}
		return true, nil
	default:
		if len(key) == 1 {
			m.agentBuildInput += key
			return true, nil
		}
	}
	return true, nil
}
func (m *model) handleAgentPanelKey(key string) (bool, tea.Cmd) {
	switch key {
	case "esc":
		m.agentViewState = 0
		return true, nil
	case "up":
		if m.agentFieldIdx > 0 {
			m.agentFieldIdx--
		}
		return true, nil
	case "down":
		if m.agentFieldIdx < 3 {
			m.agentFieldIdx++
		}
		return true, nil
	case "e":
		// Edit selected field inline
		mgr := m.getAgentManager()
		if mgr == nil {
			return true, nil
		}
		roster := mgr.GetRoster()
		if m.agentDetailIdx >= len(roster) {
			return true, nil
		}
		a := roster[m.agentDetailIdx]
		switch m.agentFieldIdx {
		case 0:
			m.agentEditBuf = a.Name
		case 1:
			m.agentEditBuf = a.Model
		case 3:
			m.agentEditBuf = a.SystemPrompt
		}
		m.agentViewState = 2
		return true, nil
	}
	return false, nil
}
func (m *model) handleAgentPromptKey(key tea.KeyMsg) (bool, tea.Cmd) {
	return false, nil
}
func (m *model) handleAgentsKey(key tea.KeyMsg) (bool, tea.Cmd) {
	return m.handleAgentsKeyStr(key.String())
}
func (m *model) handleAgentsKeyStr(key string) (bool, tea.Cmd) {
	mgr := m.getAgentManager()
	if mgr == nil {
		return false, nil
	}
	tasks := mgr.ListTasks()
	switch key {
	case "up":
		if m.agentsIdx > 0 {
			m.agentsIdx--
		}
		return true, nil
	case "down":
		if m.agentsIdx < len(tasks)-1 {
			m.agentsIdx++
		}
		return true, nil
	case "k":
		if m.agentsIdx < len(tasks) {
			mgr.Cancel(tasks[m.agentsIdx].ID)
		}
		return true, nil
	case "r":
		return true, nil
	case "enter":
		if m.agentsIdx < len(tasks) {
			m.agentLogID = tasks[m.agentsIdx].ID
		}
		return true, nil
	}
	return false, nil
}
func (m *model) handleRemoteAddKeyFixed(key string) (bool, tea.Cmd)        { return false, nil }
func (m *model) handleSpawnEnter() tea.Cmd {
	if m.spawnTaskInput {
		// Submit task
		task := m.spawnTaskBuf
		name := m.spawnAgentName
		m.spawnTaskInput = false
		m.spawnTaskBuf = ""
		m.panel = panelNone
		if task == "" || name == "" {
			return nil
		}
		mgr := m.getAgentManager()
		if mgr == nil {
			return nil
		}
		// Use the toggle value
		reportTo := "user"
		if m.spawnReportToAgent {
			reportTo = "agent"
		}
		id, err := mgr.Spawn(name, task, reportTo)
		if err != nil {
			m.addSystemMsg("spawn error: " + err.Error())
			return nil
		}
		label := "Spawned: " + name + " (" + id[:8] + ")"
		if reportTo == "agent" {
			label += " [reports to agent]"
		}
		m.addSystemMsg(label)
		return m.pollSpawnResults()
	}
	// Select agent, prompt for task
	if item, ok := m.spawnList.SelectedItem().(agentBuilderItem); ok && item.name != "(no agents)" {
		m.spawnAgentName = item.name
		m.spawnTaskInput = true
		m.spawnTaskBuf = ""
	}
	return nil
}
func (m *model) handleSpawnKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !m.spawnTaskInput {
		return false, nil
	}
	switch msg.String() {
	case "enter":
		return true, m.handleSpawnEnter()
	case "esc":
		m.spawnTaskInput = false
		return true, nil
	case "tab":
		m.spawnReportToAgent = !m.spawnReportToAgent
		return true, nil
	case "backspace":
		if len(m.spawnTaskBuf) > 0 {
			m.spawnTaskBuf = m.spawnTaskBuf[:len(m.spawnTaskBuf)-1]
		}
		return true, nil
	default:
		s := msg.String()
		if len(s) == 1 {
			m.spawnTaskBuf += s
			return true, nil
		}
	}
	return true, nil
}
func (m *model) handleAgentBuilderEdit() tea.Cmd {
	if item, ok := m.agentBuilderList.SelectedItem().(agentBuilderItem); ok && item.name != "(empty)" {
		mgr := m.getAgentManager()
		if mgr == nil {
			return nil
		}
		roster := mgr.GetRoster()
		for i, a := range roster {
			if a.Name == item.name {
				m.agentDetailIdx = i
				m.agentViewState = 1
				m.agentFieldIdx = 0
				break
			}
		}
	}
	return nil
}
func (m *model) handleAgentBuilderDelete() tea.Cmd {
	if item, ok := m.agentBuilderList.SelectedItem().(agentBuilderItem); ok && item.name != "(empty)" {
		mgr := m.getAgentManager()
		if mgr == nil {
			return nil
		}
		roster := mgr.GetRoster()
		var newRoster []agent.Agent
		for _, a := range roster {
			if a.Name != item.name {
				newRoster = append(newRoster, a)
			}
		}
		mgr.SaveRoster(newRoster)
		return m.loadAgentBuilderPanel()
	}
	return nil
}
func (m *model) handleRemoteConnect() tea.Cmd                              { return nil }
func (m *model) handleRemoteHealth() tea.Cmd                               { return nil }
func (m *model) handleRemoteDeploy() tea.Cmd                               { return nil }
func (m *model) handleSessionRename() tea.Cmd {
	if m.sessList.SelectedItem() == nil {
		return nil
	}
	// Use editingKey mechanism to rename
	m.editingKey = "rename_session"
	m.editingValue = m.convTitle
	return nil
}
func (m *model) handleHandoffFromTool(result string)                       {}

// Run starts the TUI with the given engine backend.
func Run(eng *engine.Engine) error {
	backend := engine.NewLocal(eng.DB, eng.Gateway)
	backend.AgentMgr = agent.NewManager(eng.DB, eng.Gateway)
	m := NewLocalWithOpts(backend, LaunchOpts{})
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// RunWithModel starts the TUI with a pre-configured model.
func RunWithModel(m tea.Model) error {
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

type knowledgeMsg string

func (m *model) togglePanel(p panelType)         { if m.panel == p { m.panel = panelNone } else { m.panel = p } }
func (m *model) searchKnowledge(q string) tea.Cmd {
	return func() tea.Msg {
		db := m.backend.GetDB()
		if db == nil {
			return knowledgeMsg("No database")
		}
		store := knowledge.NewStore(db)
		results, err := store.Search(q, 5)
		if err != nil {
			return knowledgeMsg("Error: " + err.Error())
		}
		if len(results) == 0 {
			return knowledgeMsg("No results for: " + q)
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Knowledge results (%d):\n", len(results)))
		for _, r := range results {
			fmt.Fprintf(&b, "\n[%s] (score %.0f)\n%s\n", r.Path, r.Score, r.Chunk)
		}
		return knowledgeMsg(b.String())
	}
}

func (m *model) knowledgeAdd(path string) tea.Cmd {
	return func() tea.Msg {
		db := m.backend.GetDB()
		if db == nil {
			return knowledgeMsg("No database")
		}
		store := knowledge.NewStore(db)
		if err := store.Add(path); err != nil {
			return knowledgeMsg("Error: " + err.Error())
		}
		_, chunks := store.Stats()
		return knowledgeMsg(fmt.Sprintf("Indexed: %s (%d total chunks)", path, chunks))
	}
}

func (m *model) knowledgeList() tea.Cmd {
	return func() tea.Msg {
		db := m.backend.GetDB()
		if db == nil {
			return knowledgeMsg("No database")
		}
		store := knowledge.NewStore(db)
		paths, err := store.List()
		if err != nil {
			return knowledgeMsg("Error: " + err.Error())
		}
		if len(paths) == 0 {
			return knowledgeMsg("No indexed files.")
		}
		return knowledgeMsg("Indexed files:\n  " + strings.Join(paths, "\n  "))
	}
}

func (m *model) knowledgeDelete(path string) tea.Cmd {
	return func() tea.Msg {
		db := m.backend.GetDB()
		if db == nil {
			return knowledgeMsg("No database")
		}
		store := knowledge.NewStore(db)
		if err := store.Delete(path); err != nil {
			return knowledgeMsg("Error: " + err.Error())
		}
		return knowledgeMsg("Removed: " + path)
	}
}

func (m *model) compactContext() tea.Cmd {
	return func() tea.Msg {
		if len(m.msgs) < 5 {
			return knowledgeMsg("Not enough messages to compact")
		}
		// Get summary model
		db := m.backend.GetDB()
		summaryModel := m.backend.CurrentModel()
		if db != nil {
			var sm string
			if err := db.QueryRow("SELECT value FROM settings WHERE key='task_model_summary'").Scan(&sm); err == nil && sm != "" {
				summaryModel = sm
			}
		}
		// Resolve model
		gw := gateway.NewRouter(db)
		apiBase, apiKey, upstream, err := gw.Resolve(summaryModel)
		if err != nil {
			return knowledgeMsg("Compact error: " + err.Error())
		}
		// Build conversation text
		var conv strings.Builder
		for _, msg := range m.msgs {
			conv.WriteString(msg.role + ": " + msg.content + "\n")
		}
		// Non-streaming request for summary
		reqMsgs := []map[string]any{
			{"role": "system", "content": "Summarize this conversation concisely, preserving key decisions and context."},
			{"role": "user", "content": conv.String()},
		}
		body := map[string]any{"model": upstream, "messages": reqMsgs, "max_tokens": 1024}
		jsonBody, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", apiBase+"/chat/completions", strings.NewReader(string(jsonBody)))
		req.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
		if err != nil {
			return knowledgeMsg("Compact error: " + err.Error())
		}
		defer resp.Body.Close()
		var result struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		if len(result.Choices) == 0 {
			return knowledgeMsg("Compact error: no response from LLM")
		}
		summary := result.Choices[0].Message.Content
		// Replace messages: summary + last 4
		last4 := m.msgs
		if len(last4) > 4 {
			last4 = last4[len(last4)-4:]
		}
		m.msgs = append([]chatMsg{{role: "system", content: "[Summary] " + summary}}, last4...)
		m.cachedRender = ""
		m.updateViewport()
		return knowledgeMsg("Context compacted via LLM summary")
	}
}

func (m *model) handleProviderEdit() tea.Cmd {
	if item, ok := m.providerList.SelectedItem().(providerItem); ok && item.name != "(empty)" {
		// Start edit mode - reuse provAdd fields
		m.provAddLabel = item.name
		m.provAddKey = ""
		m.provAddBase = ""
		m.provAddStep = 1 // key step
		m.addSystemMsg("Editing " + item.name + " - enter new API key (or leave empty to keep current)")
	}
	return nil
}
