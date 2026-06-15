package tui

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
	"os/exec"
	"time"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xnet-admin-1/ax/internal/engine"
	"github.com/xnet-admin-1/ax/internal/llm"
)

// Messages
type (
	eventMsg     engine.Event
	modelsMsg    []string
	sessionsMsg  []engine.Conversation
	convLoadMsg  []engine.Message
	newConvMsg   string
	editorMsg    string
	errMsg         error
	renderTickMsg  time.Time
	launchAgentMsg string
)

type model struct {
	backend engine.Backend
	width   int
	height  int
	vp      viewport.Model
	panelVp viewport.Model
	panelCache string
	input   inputModel
	msgs      []chatMsg
	convID    string
	convTitle string
	mode      string // chat, plan, build
	activity  string // current tool activity indicator

	// Charm components
	spinner  spinner.Model
	progress progress.Model
	modelList list.Model
	sessList  list.Model

	streaming      bool
	streamBuf      string
	inReasoning    bool
	showToolDetail bool
	panel          panelType
	settingsList list.Model
	mcpList      list.Model
	mcpState    mcpState
	mcpInput    string
	mcpPrompt   string
	mcpNewName  string
	mcpNewURL   string
	mcpProtoIdx  int
	taskEditMode   string
	configList     list.Model
	toolsList2     list.Model
	editingKey     string
	editingValue   string
	editingCursor  int
	compactIdx     int
	providerList   list.Model
	memoryList     list.Model
	remoteList     list.Model
	vectorsStats   string
	agentsList     list.Model
	agentsIdx      int
	agentLogID        string
	agentRedirectMode  bool
	subchatHandoff     string
	confirmCmd         string // command awaiting approval
	confirmReason      string
	confirmCh          chan bool // response channel
	agentResultMsgs   []chatMsg
	pendingReport     *agentDoneMsg
	pendingContext    []string
	reportedTasks     map[string]int
	agentTurns        map[string]int
	agentRedirectBuf   string
	agentViewState    int
	agentDetailIdx    int
	agentFieldIdx     int
	agentEditBuf      string
	agentPickIdx      int
	agentToolChecks   []toolCheck
	agentPromptTA     textarea.Model
	agentPromptEdit   bool
	panelTA           textarea.Model
	handoff           handoffState
	savedPrompt       string
	savedTools        []string
	savedModel        string
	pendingHandoff    *pendingHandoffMsg
	launchAgent       string
	resumeConvID      string
	memEditKey     string
	memEditValue   string
	memEditCursor  int
	memEditStep    int
	provAddStep    int
	provAddLabel   string
	provAddBase    string
	provAddKey     string
	provInput      string
	remoteAddStep  int
	remoteAddName  string
	remoteAddHost  string
	remoteAddUser  string
	remoteInput    string
	remoteAddPort  string
	spawnList      list.Model
	spawnAgentName string
	spawnTaskInput bool
	spawnTaskBuf      string
	spawnReportToAgent bool
	spawnReportToChat bool
	agentBuilderList list.Model
	agentBuildStep    int
	agentBuildInput   string
	agentBuildName    string
	agentBuildModeIdx int
	attachPath       string
	sessionStart   time.Time
	spinTick       int
	tokens         int
	ctrlCPressed   bool
	eventCh        <-chan engine.Event
	deltaAccum     int
	streamGen      int
	cachedRender   string
	cachedMsgCount int
	expandedTools  map[int]bool // tool result indices that are expanded
	batcher        *eventBatcher
	glamRenderer   *glamour.TermRenderer
	glamWidth      int
	palette        paletteModel
	inspector      inspectorModel
	treeItems      []treeItem
	treeIdx        int
}

type LaunchOpts struct {
	Agent        string
	ResumeConvID string
}

func NewLocal(b engine.Backend) tea.Model {
	return NewLocalWithOpts(b, LaunchOpts{})
}

func NewLocalWithOpts(b engine.Backend, opts LaunchOpts) tea.Model {
	vp := viewport.New(80, 20)
	panelVp := viewport.New(80, 20)
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7"))
	p := progress.New(progress.WithDefaultGradient())
	p.Width = 30
	gr, _ := glamour.NewTermRenderer(glamour.WithStandardStyle(styles.DarkStyle), glamour.WithWordWrap(76))
	// Get initial terminal size to avoid "Loading..." screen
	w, h := 80, 24
	if ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ); err == nil {
		w, h = int(ws.Col), int(ws.Row)
	}
	m := &model{
		backend:       b,
		mode:          "chat",
		width:         w,
		height:        h,
		sessionStart:  time.Now(),
		vp:            vp,
		panelVp:       panelVp,
		input:         newInput(),
		spinner:       s,
		progress:      p,
		expandedTools: make(map[int]bool),
		glamRenderer:  gr,
		palette:       newPalette(),
		launchAgent:   opts.Agent,
		resumeConvID:  opts.ResumeConvID,
		reportedTasks: make(map[string]int),
		agentTurns:    make(map[string]int),
	}
	return m
}

func (m *model) Init() tea.Cmd {
	// Load persisted model
	if db := m.backend.GetDB(); db != nil {
		var model string
		if db.QueryRow("SELECT value FROM settings WHERE key='selected_model'").Scan(&model) == nil && model != "" {
			m.backend.SetModel(model)
		}
	}
	cmds := []tea.Cmd{m.input.ta.Focus(), tea.EnableMouseCellMotion, tea.EnableBracketedPaste, m.spinner.Tick, tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg { return renderTickMsg(t) })}
	// Launch with agent handoff if specified
	if m.launchAgent != "" {
		cmds = append(cmds, func() tea.Msg {
			return launchAgentMsg(m.launchAgent)
		})
	}
	// Resume last conversation if specified
	if m.resumeConvID != "" {
		rid := m.resumeConvID
		m.convID = rid
		cmds = append(cmds, func() tea.Msg {
			msgs, err := m.backend.GetMessages(rid)
			if err != nil {
				return errMsg(err)
			}
			// Load title
			if db := m.backend.GetDB(); db != nil {
				var title string
				db.QueryRow("SELECT title FROM conversations WHERE id=?", rid).Scan(&title)
				if title != "" {
					m.convTitle = title
				}
			}
			return convLoadMsg(msgs)
		})
	}
	// Always poll for background agent results
	cmds = append(cmds, m.pollSpawnResults())
	return tea.Batch(cmds...)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalcLayout()
		return m, nil

	case tea.KeyMsg:
		// Filter out cursor position report responses (e.g. [1;1R)
		s := msg.String()
		if len(s) > 1 && s[len(s)-1] == 'R' && strings.ContainsAny(s, ";0123456789") {
			return m, nil
		}
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case eventMsg:
		return m.handleEvent(engine.Event(msg))

	case spinner.TickMsg:
		m.spinTick++
		if m.activity != "" {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, m.spinner.Tick

	case launchAgentMsg:
		return m, m.handleHandoff(string(msg))

	case renderTickMsg:
		if m.streaming {
			m.updateViewport()
			return m, tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg { return renderTickMsg(t) })
		}
		return m, tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg { return renderTickMsg(t) })

	case progress.FrameMsg:
		pm, cmd := m.progress.Update(msg)
		m.progress = pm.(progress.Model)
		return m, cmd

	case modelsMsg:
		items := make([]list.Item, len(msg))
		for i, m := range []string(msg) {
			items[i] = modelItem(m)
		}
		m.modelList = list.New(items, list.NewDefaultDelegate(), m.width-4, m.height-6)
		m.modelList.SetShowHelp(false)
		m.modelList.Title = "Models"
		m.modelList.SetFilteringEnabled(true)
		// Select current model
		cur := m.backend.CurrentModel()
		for i, mod := range []string(msg) {
			if mod == cur || strings.HasSuffix(mod, "/"+cur) || strings.Contains(mod, cur) {
				m.modelList.Select(i)
				break
			}
		}
		return m, nil

	case treeLoadedMsg:
		m.treeItems = []treeItem(msg)
		m.treeIdx = 0
		return m, nil
	case sessionsMsg:
		convs := []engine.Conversation(msg)
		items := make([]list.Item, len(convs))
		for i, c := range convs {
			title := c.Title
			if title == "" {
				title = "(untitled)"
			}
			items[i] = sessionItem{id: c.ID, title: title, ts: c.UpdatedAt}
		}
		m.sessList = list.New(items, list.NewDefaultDelegate(), m.width-4, m.height-6)
		m.sessList.SetShowHelp(false)
		m.sessList.Title = "Sessions"
		m.sessList.SetFilteringEnabled(true)
		return m, nil

	case convLoadMsg:
		m.msgs = nil
		for _, msg := range []engine.Message(msg) {
			m.msgs = append(m.msgs, chatMsg{role: msg.Role, content: msg.Content})
		}
		m.updateViewport()
		return m, nil

	case newConvMsg:
		m.convID = string(msg)
		m.msgs = nil
		m.updateViewport()
		return m, nil

	case errMsg:
		m.addSystemMsg("" + error(msg).Error())
		return m, nil


	case settingsLoadedMsg:
		items := []list.Item(msg)
		m.settingsList = list.New(items, list.NewDefaultDelegate(), m.width-4, m.height-8)
		m.settingsList.SetShowHelp(false)
		m.settingsList.Title = "Settings (enter to edit, esc to close)"
		return m, nil
	case mcpLoadedMsg:
		items := []list.Item(msg)
		if items == nil { items = []list.Item{} }
		m.mcpList = list.New(items, list.NewDefaultDelegate(), m.width-4, m.height-8)
		m.mcpList.SetShowHelp(false)
		m.mcpList.Title = "🔌 MCP Servers (enter for info, esc to close)"
		return m, nil
	case configLoadedMsg:
		items := []list.Item(msg)
		m.configList = list.New(items, list.NewDefaultDelegate(), m.width-4, m.height-8)
		m.configList.SetShowHelp(false)
		m.configList.Title = ""
		m.configList.SetShowTitle(false)
		m.configList.SetFilteringEnabled(true)
		return m, nil
	case toolsLoadedMsg:
		items := []list.Item(msg)
		m.toolsList2 = list.New(items, list.NewDefaultDelegate(), m.width-4, m.height-8)
		m.toolsList2.SetShowHelp(false)
		m.toolsList2.Title = ""
		m.toolsList2.SetShowTitle(false)
		return m, nil
	case memoriesLoadedMsg:
		items := []list.Item(msg)
		if items == nil { items = []list.Item{} }
		m.memoryList = list.New(items, list.NewDefaultDelegate(), m.width-4, m.height-8)
		m.memoryList.SetShowHelp(false)
		m.memoryList.SetShowTitle(false)
		return m, nil
	case remoteLoadedMsg:
		items := []list.Item(msg)
		if items == nil { items = []list.Item{} }
		m.remoteList = list.New(items, list.NewDefaultDelegate(), m.width-4, m.height-8)
		m.remoteList.SetShowHelp(false)
		m.remoteList.SetShowTitle(false)
		return m, nil
	case gwProvidersLoadedMsg:
		items := []list.Item(msg)
		if items == nil { items = []list.Item{} }
		m.providerList = list.New(items, list.NewDefaultDelegate(), m.width-4, m.height-8)
		m.providerList.SetShowHelp(false)
		m.providerList.SetShowTitle(false)
		return m, nil
	case spawnLoadedMsg:
		items := []list.Item(msg)
		m.spawnList = list.New(items, list.NewDefaultDelegate(), m.width-4, m.height-8)
		m.spawnList.SetShowHelp(false)
		m.spawnList.SetShowTitle(false)
		return m, nil
	case agentListLoadedMsg:
		items := []list.Item(msg)
		m.agentBuilderList = list.New(items, list.NewDefaultDelegate(), m.width-4, m.height-8)
		m.agentBuilderList.SetShowHelp(false)
		m.agentBuilderList.SetShowTitle(false)
		return m, nil
	case agentBuilderLoadedMsg:
		items := []list.Item(msg)
		m.agentBuilderList = list.New(items, list.NewDefaultDelegate(), m.width-4, m.height-8)
		m.agentBuilderList.SetShowHelp(false)
		m.agentBuilderList.SetShowTitle(false)
		return m, nil
	case agentsLoadedMsg:
		items := []list.Item(msg)
		if items == nil { items = []list.Item{} }
		m.agentsList = list.New(items, list.NewDefaultDelegate(), m.width-4, m.height-8)
		m.agentsList.SetShowHelp(false)
		m.agentsList.SetShowTitle(false)
		return m, nil
	case vectorsLoadedMsg:
		m.vectorsStats = msg.stats
		return m, nil
	case pollAgainMsg:
		return m.deliverPendingReports()
	case agentDoneMsg:
		return m.handleAgentDone(msg)
	case spawnResultMsg:
		m.addSystemMsg(string(msg))
		// If it was a spawn start, begin polling for completion
		if strings.HasPrefix(string(msg), "Spawned: ") {
			return m, m.pollSpawnResults()
		}
		return m, nil
	case knowledgeMsg:
		m.addSystemMsg(string(msg))
		return m, nil

	case editorMsg:
		content := strings.TrimSpace(string(msg))
		if content != "" {
			m.msgs = append(m.msgs, chatMsg{role: "user", content: content})
			m.streaming = true
			m.streamBuf = ""
			if m.convTitle == "" || m.convTitle == "New Chat" {
				title := content
				if len(title) > 50 {
					title = title[:50] + "…"
				}
				m.convTitle = title
			}
			m.updateViewport()
			return m, m.startChat(content)
		}
		return m, nil
	}

	cmd := m.input.Update(msg)
	return m, cmd
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Dangerous command confirmation intercepts all keys
	if m.confirmCh != nil {
		switch msg.String() {
		case "y", "Y":
			m.confirmCh <- true
			m.addSystemMsg("ok Approved: " + m.confirmCmd)
			m.confirmCh = nil
			m.confirmCmd = ""
			m.confirmReason = ""
			m.updateViewport()
			return m, m.readNextEvent()
		case "n", "N", "esc":
			m.confirmCh <- false
			m.addSystemMsg("x Denied: " + m.confirmCmd)
			m.confirmCh = nil
			m.confirmCmd = ""
			m.confirmReason = ""
			m.updateViewport()
			return m, m.readNextEvent()
		}
		return m, nil
	}
	// Inspector modal intercepts keys when showing
	if m.inspector.showing {
		switch msg.String() {
		case "q", "esc":
			m.inspector.close()
			return m, nil
		case "j", "down":
			m.inspector.scrollDown()
			return m, nil
		case "k", "up":
			m.inspector.scrollUp()
			return m, nil
		case "g":
			m.inspector.goTop()
			return m, nil
		case "G":
			visH := m.height * 70 / 100 - 4
			m.inspector.goBottom(visH)
			return m, nil
		}
		return m, nil
	}

	// Command palette intercepts all keys when showing
	if m.palette.showing {
		cmd, done := m.palette.handleKey(msg.String())
		if cmd != "" {
			m.input.ta.SetValue(cmd)
			return m.sendInput()
		}
		if done {
			return m, nil
		}
		return m, nil
	}

	// Slash autocomplete intercepts (skip when panel open)
	if m.panel == panelNone && m.input.HandleCompKey(msg.String()) {
		if m.input.executeOnNext {
			m.input.executeOnNext = false
			return m.sendInput()
		}
		return m, nil
	}

	// Inline edit mode intercepts all keys
	if m.editingKey != "" {
		handled, cmd := m.handleEditKey(msg.String())
		if handled {
			return m, cmd
		}
	}

	// Spawn panel: b opens builder
	if m.panel == panelSpawn && !m.spawnTaskInput && msg.String() == "b" {
		m.panel = panelAgentBuilder
		return m, m.loadAgentBuilderPanel()
	}

	// Agent config panel intercepts keys in detail/edit view
	if m.panel == panelAgentBuilder && m.agentViewState > 0 {
		if m.agentPromptEdit {
			if h, cmd := m.handleAgentPromptKey(msg); h {
				return m, cmd
			}
		}
		if h, cmd := m.handleAgentPanelKey(msg.String()); h {
			return m, cmd
		}
	}

	// Agent redirect mode intercepts all keys
	if m.panel == panelAgents && m.agentRedirectMode {
		if h, cmd := m.handleAgentsKey(msg); h {
			if m.subchatHandoff != "" {
				prompt := m.subchatHandoff
				m.subchatHandoff = ""
				return m, m.sendChat(prompt)
			}
			return m, cmd
		}
	}

	// Spawn task input intercepts keys
	if m.panel == panelSpawn && m.spawnTaskInput {
		handled, cmd := m.handleSpawnKey(msg)
		if handled {
			return m, cmd
		}
	}

	// Agent builder input intercepts keys
	if m.panel == panelAgentBuilder && m.agentBuildStep > 0 {
		handled, cmd := m.handleAgentBuildKey(msg.String())
		if handled {
			return m, cmd
		}
	}

	// Provider add mode intercepts keys
	if m.panel == panelProvider && m.provAddStep > 0 {
		handled, cmd := m.handleProviderAddKey(msg.String())
		if handled {
			return m, cmd
		}
	}

	// Remote add mode intercepts keys
	if m.panel == panelRemote && m.remoteAddStep > 0 {
		handled, cmd := m.handleRemoteAddKeyFixed(msg.String())
		if handled {
			return m, cmd
		}
	}

	// Memory edit mode intercepts keys
	if m.panel == panelMemory && m.memEditStep > 0 {
		handled, cmd := m.handleMemoryEditKey(msg.String())
		if handled {
			return m, cmd
		}
	}

	// MCP panel intercepts all keys
	if m.panel == panelMCP {
		if msg.String() == "esc" && m.mcpState == mcpList {
			m.panel = panelNone
			return m, nil
		}
		handled, cmd := m.handleMCPKey(msg.String())
		if handled {
			return m, cmd
		}
	}
	switch msg.String() {
	case "ctrl+c":
		if m.editingKey != "" {
			m.editingKey = ""
			return m, nil
		}
		if m.panel != panelNone {
			m.panel = panelNone
			return m, m.input.ta.Focus()
		}
		if m.streaming {
			m.backend.Cancel(m.convID)
			m.streaming = false
			return m, nil
		}
		if m.ctrlCPressed {
			return m, tea.Quit
		}
		m.ctrlCPressed = true
		return m, nil
	case "ctrl+d":
		if m.panel != panelNone {
			m.panelVp.HalfPageDown()
		} else {
			m.vp.HalfPageDown()
		}
		return m, nil
	case "ctrl+u":
		if m.panel != panelNone {
			m.panelVp.HalfPageUp()
		} else {
			m.vp.HalfPageUp()
		}
		return m, nil
	case "ctrl+n":
		return m, m.newConversation()
	case "ctrl+y":
		// Copy last assistant message to clipboard
		for i := len(m.msgs) - 1; i >= 0; i-- {
			if m.msgs[i].role == "assistant" {
				clipboard.WriteAll(m.msgs[i].content)
				m.addSystemMsg("Copied to clipboard")
				break
			}
		}
		return m, nil
	case "ctrl+x":
		// Cut input text to clipboard
		if m.panel == panelNone && !m.streaming {
			text := m.input.ta.Value()
			if text != "" {
				clipboard.WriteAll(text)
				m.input.ta.Reset()
				m.addSystemMsg("Cut to clipboard")
			}
		}
		return m, nil
	case "ctrl+k":
		m.palette.toggle()
		return m, nil
	case "ctrl+e":
		return m, m.openEditor()
	case "ctrl+g":
		m.panel = panelAgents
		return m, nil
	case "ctrl+h":
		if m.panel == panelAgents && m.agentLogID != "" {
			if h, _ := m.handleAgentsKeyStr("ctrl+h"); h {
				if m.subchatHandoff != "" {
					prompt := m.subchatHandoff
					m.subchatHandoff = ""
					return m, m.sendChat(prompt)
				}
			}
		}
		return m, nil
	case "ctrl+o":
		m.showToolDetail = !m.showToolDetail
		m.updateViewport()
		return m, nil
	case "esc":
		if m.panel == panelAgents && m.agentLogID != "" { m.agentLogID = ""; return m, nil }
		if m.panel != panelNone {
			m.panel = panelNone
			return m, m.input.ta.Focus()
		}
		return m, nil
	case "enter":
		if m.panel == panelAgents { if h, c := m.handleAgentsKeyStr("enter"); h { return m, c } }
		if m.panel == panelModels {
			if item, ok := m.modelList.SelectedItem().(modelItem); ok {
				if m.taskEditMode != "" {
					db := m.backend.GetDB()
					if db != nil {
						db.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES(?,?)", "task_model_"+m.taskEditMode, "task_model_"+m.taskEditMode, string(item))
					}
					m.addSystemMsg("" + m.taskEditMode + " → " + string(item))
					m.taskEditMode = ""
				} else {
					m.backend.SetModel(string(item))
					db := m.backend.GetDB()
					if db != nil {
						db.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES(?,?)", "selected_model", string(item))
					}
					m.addSystemMsg("Model: " + string(item))
				}
			}
			m.panel = panelNone
			return m, nil
		}
		if m.panel == panelSessions {
			if item, ok := m.sessList.SelectedItem().(sessionItem); ok {
				m.panel = panelNone
				return m, m.loadConv(item.id)
			}
			return m, nil
		}
		if m.panel == panelSettings {
			return m, m.handleSettingsEnter()
		}
		if m.panel == panelConfig {
			return m, m.handleConfigEnter()
		}
		if m.panel == panelTools {
			return m, m.handleToolsEnter()
		}
		if m.panel == panelCompact {
			return m, m.handleCompactEnter()
		}
		if m.panel == panelTree {			return m, m.handleTreeEnter()		}
		if m.panel == panelProvider {
			return m, m.handleProviderToggle()
		}
		if m.panel == panelSpawn {
			return m, m.handleSpawnEnter()
		}
		if m.panel == panelAgentBuilder && m.agentBuildStep == 0 {
			return m, m.handleAgentBuilderEdit()
		}
		if m.panel == panelMemory {
			return m, m.handleMemoryEnter()
		}
		if m.panel == panelRemote {
			return m, m.handleRemoteConnect()
		}
		return m.sendInput()
	case "up":
		if m.panel == panelAgents { if h, c := m.handleAgentsKeyStr("enter"); h { return m, c } }
		if m.panel == panelModels {
			m.modelList, _ = m.modelList.Update(msg)
			return m, nil
		}
		if m.panel == panelSessions {
			m.sessList, _ = m.sessList.Update(msg)
			return m, nil
		}
		if m.panel == panelSettings { m.settingsList, _ = m.settingsList.Update(msg); return m, nil }
		if m.panel == panelConfig { m.configList, _ = m.configList.Update(msg); return m, nil }
		if m.panel == panelTools { m.toolsList2, _ = m.toolsList2.Update(msg); return m, nil }
		if m.panel == panelCompact { if m.compactIdx > 0 { m.compactIdx-- }; return m, nil }
		if m.panel == panelTree { if m.treeIdx > 0 { m.treeIdx-- }; return m, nil }
		if m.panel == panelProvider { m.providerList, _ = m.providerList.Update(msg); return m, nil }
		if m.panel == panelMemory { m.memoryList, _ = m.memoryList.Update(msg); return m, nil }
		if m.panel == panelRemote { m.remoteList, _ = m.remoteList.Update(msg); return m, nil }
		if m.panel == panelAgents { if h, c := m.handleAgentsKeyStr("up"); h { return m, c } }
		if m.panel == panelSpawn { m.spawnList, _ = m.spawnList.Update(msg); return m, nil }
		if m.panel == panelAgentBuilder { m.agentBuilderList, _ = m.agentBuilderList.Update(msg); return m, nil }
		if m.panel != panelNone { m.panelVp.LineUp(1); return m, nil }
		if m.panel == panelNone && m.input.HistoryUp() {
			return m, nil
		}
	case "h":
		if m.panel == panelRemote {
			return m, m.handleRemoteHealth()
		}
	case "p":
		if m.panel == panelAgents { if h, c := m.handleAgentsKeyStr("p"); h { return m, c } }
		if m.panel == panelRemote {
			return m, m.handleRemoteDeploy()
		}
	case "r":
		if m.panel == panelUsage { m.tokens = 0; return m, nil }
		if m.panel == panelVectors { return m, m.vectorsReindex() }
		if m.panel == panelAgents { if h, c := m.handleAgentsKeyStr("r"); h { return m, c } }
	case "k":
		if m.panel == panelAgents { if h, c := m.handleAgentsKeyStr("k"); h { return m, c } }
	case "l":
		if m.panel == panelAgents { if h, c := m.handleAgentsKeyStr("l"); h { return m, c } }
	case "n":
		if m.panel == panelSessions {
			return m, m.handleSessionRename()
		}
	case "a":
		if m.panel == panelMemory {
			m.startMemoryAdd()
			return m, nil
		}
		if m.panel == panelProvider {
			m.provAddStep = 1
			m.provInput = ""
			return m, nil
		}
		if m.panel == panelAgentBuilder {
			m.agentBuildStep = 1
			m.agentBuildInput = ""
			return m, nil
		}
		if m.panel == panelRemote {
			m.remoteAddStep = 1
			m.remoteInput = ""
			return m, nil
		}
	case "d", "delete":
		if m.panel == panelTree {
			return m, m.handleTreeDelete()
		}
		if m.panel == panelSessions {
			return m, m.handleSessionDelete()
		}
		if m.panel == panelProvider {
			return m, m.handleProviderDeleteFixed()
		}
		if m.panel == panelAgentBuilder {
			return m, m.handleAgentBuilderDelete()
		}
		if m.panel == panelMemory {
			return m, m.handleMemoryDelete()
		}
		if m.panel == panelRemote {
			return m, m.handleRemoteDelete()
		}
		if m.panel == panelAgents { if h, c := m.handleAgentsKeyStr("d"); h { return m, c } }
	case "down":
		if m.panel == panelAgents { if h, c := m.handleAgentsKeyStr("enter"); h { return m, c } }
		if m.panel == panelModels {
			m.modelList, _ = m.modelList.Update(msg)
			return m, nil
		}
		if m.panel == panelSessions {
			m.sessList, _ = m.sessList.Update(msg)
			return m, nil
		}
		if m.panel == panelSettings { m.settingsList, _ = m.settingsList.Update(msg); return m, nil }
		if m.panel == panelConfig { m.configList, _ = m.configList.Update(msg); return m, nil }
		if m.panel == panelTools { m.toolsList2, _ = m.toolsList2.Update(msg); return m, nil }
		if m.panel == panelCompact { if m.compactIdx < 2 { m.compactIdx++ }; return m, nil }
		if m.panel == panelTree { if m.treeIdx < len(m.treeItems)-1 { m.treeIdx++ }; return m, nil }
		if m.panel == panelProvider { m.providerList, _ = m.providerList.Update(msg); return m, nil }
		if m.panel == panelMemory { m.memoryList, _ = m.memoryList.Update(msg); return m, nil }
		if m.panel == panelRemote { m.remoteList, _ = m.remoteList.Update(msg); return m, nil }
		if m.panel == panelAgents { if h, c := m.handleAgentsKeyStr("down"); h { return m, c } }
		if m.panel == panelSpawn { m.spawnList, _ = m.spawnList.Update(msg); return m, nil }
		if m.panel == panelAgentBuilder { m.agentBuilderList, _ = m.agentBuilderList.Update(msg); return m, nil }
		if m.panel != panelNone { m.panelVp.LineDown(1); return m, nil }
		if m.panel == panelNone && m.input.HistoryDown() {
			return m, nil
		}
	}

	m.ctrlCPressed = false
	if m.panel != panelNone {
		return m, nil
	}
	cmd := m.input.Update(msg)
	return m, cmd
}

func (m *model) sendInput() (tea.Model, tea.Cmd) {
	val := m.input.Value()
	if val == "" {
		return m, nil
	}
	m.input.PushHistory(val)
	m.input.Reset()

	if strings.HasPrefix(val, "/") {
		cmd := m.handleCommand(val)
		return m, cmd
	}

	// Interrupt current response if streaming
	if m.streaming {
		m.backend.Cancel(m.convID)
		if m.streamBuf != "" {
			m.msgs = append(m.msgs, chatMsg{role: "assistant", content: filterToolMarkup(m.streamBuf) + " [interrupted]"})
		}
		m.streaming = false
		m.streamBuf = ""
		m.activity = ""
		m.streamGen++
		m.eventCh = nil
	}

	// Include attachment if set
	if m.attachPath != "" {
		data, err := os.ReadFile(m.attachPath)
		if err == nil {
			val = "[File: " + m.attachPath + "]\n" + string(data) + "\n\n" + val
		}
		m.attachPath = ""
	}
	m.msgs = append(m.msgs, chatMsg{role: "user", content: val})
	m.streaming = true
	m.streamBuf = ""
	m.streamGen++

	// Auto-generate title from first user message
	if m.convTitle == "" || m.convTitle == "New Chat" {
		title := val
		if len(title) > 50 {
			title = title[:50] + "…"
		}
		m.convTitle = title
	}

	m.updateViewport()

	return m, m.startChat(val)
}

// sendChat injects a message into the chat and starts streaming (used by agent reports)
func (m *model) sendChat(content string) tea.Cmd {
	m.streaming = true
	m.streamBuf = ""
	m.streamGen++
	m.updateViewport()
	return m.startChat(content)
}

func (m *model) startChat(content string) tea.Cmd {
	return func() tea.Msg {
		if m.convID == "" {
			id, err := m.backend.CreateConversation(m.convTitle)
			if err != nil {
				return errMsg(err)
			}
			m.convID = id
		}
		if local, ok := m.backend.(*engine.Local); ok {
			local.Mode = m.mode
		}
		ch, err := m.backend.Chat(m.convID, content)
		if err != nil {
			return errMsg(err)
		}
		m.eventCh = ch
		// Read first event
		ev, ok := <-ch
		if !ok {
			return eventMsg(engine.Event{Type: "end"})
		}
		return eventMsg(ev)
	}
}

func (m *model) readNextEvent() tea.Cmd {
	return func() tea.Msg {
		if m.eventCh == nil {
			return nil
		}
		ev, ok := <-m.eventCh
		if !ok {
			return eventMsg(engine.Event{Type: "end"})
		}
		return eventMsg(ev)
	}
}

func (m *model) handleEvent(ev engine.Event) (tea.Model, tea.Cmd) {
	switch ev.Type {
	case "delta":
		if ev.Reasoning != "" {
			if !m.inReasoning {
				m.inReasoning = true
			}
			m.streamBuf += ev.Reasoning
		}
		if ev.Delta != "" {
			if m.inReasoning {
				m.inReasoning = false
				m.streamBuf += "\n\n---\n\n"
			}
			m.streamBuf += ev.Delta
		}
		m.tokens += len(strings.Fields(ev.Delta))
		if m.activity == "" {
			m.activity = "thinking"
		}
		return m, m.readNextEvent()
	case "reasoning":
		m.streamBuf += ev.Delta
		return m, m.readNextEvent()
	case "tool_call":
		// Show tool name with truncated args
		args := ev.ToolArgs
		if len(args) > 60 {
			args = args[:60] + "..."
		}
		m.msgs = append(m.msgs, chatMsg{role: "tool_call", content: ev.ToolName + "(" + args + ")"})
		// Set activity indicator based on tool name
		// Flush streaming text before tool call so it persists in chat
		if m.streamBuf != "" {
			m.msgs = append(m.msgs, chatMsg{role: "assistant", content: filterToolMarkup(m.streamBuf)})
			m.streamBuf = ""
		}
		toolBase := ev.ToolName
		if idx := strings.IndexAny(toolBase, ":*"); idx >= 0 { toolBase = toolBase[:idx] }
		switch {
		case toolBase == "run_sh" || strings.Contains(toolBase, "sh") || strings.Contains(toolBase, "exec") || strings.Contains(toolBase, "shell"):
			m.activity = "shell"
		case ev.ToolName == "read_file" || strings.HasPrefix(ev.ToolName, "read"):
			m.activity = "reading"
		case ev.ToolName == "write_file" || strings.HasPrefix(ev.ToolName, "write") || strings.HasPrefix(ev.ToolName, "edit"):
			m.activity = "writing"
		case ev.ToolName == "list_directory" || strings.HasPrefix(ev.ToolName, "list"):
			m.activity = "listing"
		case ev.ToolName == "search_web" || ev.ToolName == "search_images":
			m.activity = "searching"
		case ev.ToolName == "fetch_url" || strings.HasPrefix(ev.ToolName, "fetch") || strings.HasPrefix(ev.ToolName, "http"):
			m.activity = "fetching"
		case strings.Contains(ev.ToolName, "delete") || strings.Contains(ev.ToolName, "remove"):
			m.activity = "removing"
		default:
			m.activity = ev.ToolName
		}
		m.updateViewport()
		return m, tea.Batch(m.readNextEvent(), m.spinner.Tick)
	case "tool_result":
		content := ev.ToolResult
		if content == "" {
			content = "(ok)"
		}
		m.msgs = append(m.msgs, chatMsg{role: "tool_result", content: content})
		// Check for handoff tool result
		if ev.ToolName == "handoff" && strings.Contains(ev.ToolResult, `"handoff": true`) {
			m.handleHandoffFromTool(ev.ToolResult)
		}
		m.updateViewport()
		return m, m.readNextEvent()
	case "progress":
		return m, m.readNextEvent()
	case "confirm":
		m.confirmCmd = ev.ToolName
		m.confirmReason = ev.ToolResult
		m.confirmCh = ev.ConfirmCh
		m.addSystemMsg(fmt.Sprintf("⚠ Dangerous: %s (%s) — y/n?", ev.ToolName, ev.ToolResult))
		m.updateViewport()
		return m, nil // stop reading events until user responds
	case "error":
		m.streaming = false
		m.addSystemMsg("" + ev.Error)
		return m, m.pollSpawnResults()
	case "title":
		m.convTitle = ev.Delta
		return m, m.readNextEvent()
	case "end":
		if m.streamBuf != "" {
			m.msgs = append(m.msgs, chatMsg{role: "assistant", content: filterToolMarkup(m.streamBuf)})
			m.streamBuf = ""
		}
		m.streaming = false
		m.activity = ""
		m.tokens += ev.TotalTokens
		m.updateViewport()
		// Auto-compact: if tokens exceed 75% of context window, compact
		if m.tokens > 0 && len(m.msgs) > 8 {
			ctxLimit := 32000 // default
			if cfg, ok := m.backend.GetModelConfig(); ok && cfg.ContextTokens > 0 {
				ctxLimit = cfg.ContextTokens
			}
			if m.tokens > ctxLimit*75/100 {
				return m, m.compactContext()
			}
		}
		// Deliver pending agent reports now that chat is idle
		if len(m.pendingContext) > 0 {
			return m.deliverPendingReports()
		}
		// Execute pending handoff from tool call
		if m.pendingHandoff != nil {
			h := m.pendingHandoff
			m.pendingHandoff = nil
			m.handleHandoff(h.Agent)
			// Send the handoff message as first input to specialist
			m.msgs = append(m.msgs, chatMsg{role: "user", content: h.Message})
			m.updateViewport()
			return m, m.sendChat(h.Message)
		}
		return m, m.pollSpawnResults()
	}
	return m, m.readNextEvent()
}

func (m *model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	status := m.statusBar()
	inputView := m.input.View()
	inputH := lipgloss.Height(inputView)

	// Layout: status(1) + chat + activity(1) + input(inputH) + help(1)
	chatH := m.height - 3 - inputH
	if chatH < 3 {
		chatH = 3
	}
	m.vp.Height = chatH
	m.vp.Width = m.width

	var chatView string
	if m.panel != panelNone {
		bc := m.breadcrumb()
		pContent := wordWrap(m.panelView(m.width-4), m.width-6)
		m.panelVp.Width = m.width - 4
		m.panelVp.Height = chatH - 2
		if pContent != m.panelCache {
			yOff := m.panelVp.YOffset
			m.panelCache = pContent
			m.panelVp.SetContent(pContent)
			// Auto-scroll to bottom for agent log view
			if m.panel == panelAgents && m.agentLogID != "" {
				m.panelVp.GotoBottom()
			} else {
				m.panelVp.SetYOffset(yOff)
			}
		}
		chatView = panelStyle.Width(m.width - 4).Height(chatH - 2).Render(bc + m.panelVp.View())
	} else {
		chatView = m.vp.View()
	}

	activityLine := ""
	act := m.activity
	if act == "" && m.streaming {
		act = "thinking"
	}
	if act != "" {
		frame := getSpinnerFrame(act, m.spinTick)
		// Ensure single line, clamp to width
		if idx := strings.IndexByte(act, 10); idx >= 0 { act = act[:idx] }
		if len(act) > m.width-4 { act = act[:m.width-4] }
		activityLine = lipgloss.NewStyle().Foreground(tokyoPurple).Render(frame) + " " + lipgloss.NewStyle().Foreground(tokyoPurple).Faint(true).Render(act)
	}

	if m.palette.showing {
		chatView = m.palette.view(m.width, chatH)
	}

	helpLine := m.helpBar()
	if activityLine == "" {
		activityLine = " "
	}
	base := lipgloss.JoinVertical(lipgloss.Left, status, chatView, activityLine, inputView, helpLine)
	if m.inspector.showing {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.inspector.view(m.width, m.height), lipgloss.WithWhitespaceChars(" "))
	}
	return base
}

func (m *model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if m.panel == panelModels {
			m.modelList, _ = m.modelList.Update(msg)
		} else if m.panel == panelSessions {
			m.sessList, _ = m.sessList.Update(msg)
		} else if m.panel == panelSettings {
			m.settingsList, _ = m.settingsList.Update(msg)
		} else if m.panel == panelMCP {
			m.mcpList, _ = m.mcpList.Update(msg)
		} else if m.panel != panelNone {
			m.panelVp.LineUp(3)
		} else {
			m.vp.ScrollUp(3)
		}
	case tea.MouseButtonWheelDown:
		if m.panel == panelModels {
			m.modelList, _ = m.modelList.Update(msg)
		} else if m.panel == panelSessions {
			m.sessList, _ = m.sessList.Update(msg)
		} else if m.panel == panelSettings {
			m.settingsList, _ = m.settingsList.Update(msg)
		} else if m.panel == panelMCP {
			m.mcpList, _ = m.mcpList.Update(msg)
		} else if m.panel != panelNone {
			m.panelVp.LineDown(3)
		} else {
			m.vp.ScrollDown(3)
		}
	case tea.MouseButtonLeft:
		// Click on tool results to expand/collapse
		if m.panel == panelNone {
			// Calculate which message line was clicked
			clickY := msg.Y - 1 // subtract status bar
			if clickY >= 0 {
				m.toggleToolAtLine(clickY)
			}
		}
	}
	return m, nil
}

func (m *model) recalcLayout() {
	m.input.SetWidth(m.width)
	m.vp.Width = m.width
	inputH := 3
	lines := strings.Count(m.input.ta.Value(), "\n") + 1
	if lines > inputH {
		inputH = lines
	}
	if inputH > 8 {
		inputH = 8
	}
	chatH := m.height - 1 - inputH - 2
	if chatH < 3 {
		chatH = 3
	}
	m.vp.Height = chatH

	// Resize all panel lists (only if initialized)
	listW := m.width - 4
	listH := chatH - 4
	if listH < 3 { listH = 3 }
	if len(m.modelList.Items()) > 0 { m.modelList.SetSize(listW, listH) }
	if len(m.sessList.Items()) > 0 { m.sessList.SetSize(listW, listH) }
	if len(m.settingsList.Items()) > 0 { m.settingsList.SetSize(listW, listH) }
	if len(m.configList.Items()) > 0 { m.configList.SetSize(listW, listH) }
	if len(m.toolsList2.Items()) > 0 { m.toolsList2.SetSize(listW, listH) }
	if len(m.providerList.Items()) > 0 { m.providerList.SetSize(listW, listH) }
	if len(m.memoryList.Items()) > 0 { m.memoryList.SetSize(listW, listH) }
	if len(m.remoteList.Items()) > 0 { m.remoteList.SetSize(listW, listH) }
	if len(m.agentsList.Items()) > 0 { m.agentsList.SetSize(listW, listH) }
	if len(m.spawnList.Items()) > 0 { m.spawnList.SetSize(listW, listH) }
	if len(m.agentBuilderList.Items()) > 0 { m.agentBuilderList.SetSize(listW, listH) }
	m.updateViewport()
}

func (m *model) statusBar() string {
	w := m.width
	if w < 20 {
		w = 20
	}

	// Handoff indicator
	if m.handoff.Active {
		bar := fmt.Sprintf(" [%s] /return to hand back", m.handoff.AgentName)
		return statusBarStyle.Width(w).Render(bar)
	}

	// Simple reliable layout: left | right
	ind := "*"
	if m.streaming {
		ind = getSpinnerFrame("thinking", m.spinTick)
	}

	title := "AX"
	if m.convTitle != "" {
		title = m.convTitle
	}
	if len(title) > 25 {
		title = title[:25]
	}

	modelName := m.backend.CurrentModel()
	if idx := strings.LastIndex(modelName, "/"); idx >= 0 {
		modelName = modelName[idx+1:]
	}
	if len(modelName) > 20 {
		modelName = modelName[:20]
	}

	elapsed := time.Since(m.sessionStart)
	modeInd := map[string]string{"chat": "C", "plan": "P", "build": "B"}[m.mode]
	if modeInd == "" { modeInd = "C" }
	left := fmt.Sprintf(" [%s] %s %s | %s", modeInd, ind, title, modelName)
	agents := 0
	for _, t := range llm.ListBackgroundTasks() {
		if t.Status == "running" {
			agents++
		}
	}
	agentStr := ""
	if agents > 0 {
		agentStr = fmt.Sprintf(" %d agents", agents)
	}
	right := fmt.Sprintf("%d tok %s%s ", m.tokens, formatDuration(elapsed), agentStr)

	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		// Narrow screen: drop right side
		if lipgloss.Width(left) > w-2 {
			left = left[:w-2]
		}
		return statusBarStyle.Width(w).Render(left)
	}

	bar := left + strings.Repeat(" ", gap) + right
	return statusBarStyle.Width(w).Render(bar)
}

func boolToInt(b bool) int { if b { return 1 }; return 0 }

func (m *model) updateViewport() {
	w := m.vp.Width
	if w < 20 {
		w = 80
	}
	// Recreate renderer if width changed
	if m.glamRenderer == nil || w != m.glamWidth {
		m.glamRenderer, _ = glamour.NewTermRenderer(glamour.WithStandardStyle(styles.DarkStyle), glamour.WithWordWrap(w-4))
		m.glamWidth = w
		m.cachedMsgCount = 0 // force re-render
	}
	// Cache rendered messages - invalidate on message count or tool detail toggle
	cacheKey := len(m.msgs)*2 + boolToInt(m.showToolDetail)
	if cacheKey != m.cachedMsgCount {
		m.cachedRender = renderMessages(m.msgs, w, m.showToolDetail, m.expandedTools, m.glamRenderer)
		m.cachedMsgCount = cacheKey
	}
	content := m.cachedRender
	if m.streaming && m.streamBuf != "" {
		display := filterToolMarkup(m.streamBuf)
		if m.glamRenderer != nil {
			if rendered, err := m.glamRenderer.Render(display); err == nil && strings.TrimSpace(rendered) != "" {
				display = strings.TrimRight(rendered, "\n ")
			}
		} else if w > 10 {
			display = wrapText(display, w-4)
		}
		lines := strings.Split(display, "\n")
		var sb strings.Builder
		for i, line := range lines {
			if i == len(lines)-1 {
				sb.WriteString(assistantGutter.Render("") + strings.TrimRight(line, " ") + "\n")
			} else {
				sb.WriteString(assistantGutter.Render("") + line + "\n")
			}
		}
		content += sb.String()
	}
	m.vp.SetContent(content)
	m.vp.GotoBottom()
}

func (m *model) addSystemMsg(text string) {
	m.msgs = append(m.msgs, chatMsg{role: "system", content: text})
	m.updateViewport()
}

// Async commands

func (m *model) loadModels() tea.Cmd {
	return func() tea.Msg {
		models, err := m.backend.ListModels()
		if err != nil {
			return errMsg(err)
		}
		return modelsMsg(models)
	}
}

func (m *model) loadSessions() tea.Cmd {
	return func() tea.Msg {
		convs, err := m.backend.ListConversations(20)
		if err != nil {
			return errMsg(err)
		}
		return sessionsMsg(convs)
	}
}

func (m *model) loadConv(id string) tea.Cmd {
	return func() tea.Msg {
		m.convID = id
		msgs, err := m.backend.GetMessages(id)
		if err != nil {
			return errMsg(err)
		}
		return convLoadMsg(msgs)
	}
}

func (m *model) newConversation() tea.Cmd {
	return func() tea.Msg {
		id, err := m.backend.CreateConversation("")
		if err != nil {
			return errMsg(err)
		}
		return newConvMsg(id)
	}
}

// toggleToolAtLine toggles expand/collapse for tool results at a viewport line
func (m *model) toggleToolAtLine(y int) {
	// Map viewport line to message index (approximate)
	line := y + m.vp.YOffset
	currentLine := 0
	for i, msg := range m.msgs {
		if msg.role == "tool_result" {
			if currentLine <= line && line < currentLine+2 {
				if m.expandedTools[i] {
					delete(m.expandedTools, i)
				} else {
					m.expandedTools[i] = true
				}
				m.updateViewport()
				return
			}
		}
		// Rough line count per message
		currentLine += strings.Count(msg.content, "\n") + 2
	}
}

// openEditor spawns $EDITOR for multi-line input
func (m *model) openEditor() tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nano"
	}
	tmpFile := "/tmp/ax-input.md"
	os.WriteFile(tmpFile, []byte(""), 0644)
	c := exec.Command(editor, tmpFile)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return errMsg(err)
		}
		data, err := os.ReadFile(tmpFile)
		if err != nil {
			return errMsg(err)
		}
		os.Remove(tmpFile)
		return editorMsg(string(data))
	})
}
