// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package tui

import (
	"fmt"
	"os"

	"os/exec"
	"time"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/stopwatch"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xnet-admin-1/ax/internal/engine"
	"github.com/xnet-admin-1/ax/internal/llm"
)

// Messages
type (
	eventMsg       engine.Event
	batchEventsMsg []engine.Event
	modelsMsg      []string
	sessionsMsg    []engine.Conversation
	convLoadMsg    []engine.Message
	newConvMsg     string
	editorMsg      string
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
	viewCache  string
	viewDirty  bool
	input   inputModel
	msgs      []chatMsg
	convID    string
	convTitle string
	mode      string // chat, plan, build
	activity  string // current tool activity indicator

	// Charm components
	spinner    spinner.Model
	stopwatch  stopwatch.Model
	progress progress.Model
	modelList list.Model
	sessList  list.Model

	streaming      bool
	streamBuf            string
	streamRenderedCache  string
	streamRenderedBlocks int
	inReasoning          bool
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
	lastViewCache  string
	lastViewOffset int
	lastViewMsgCnt int
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
	DetectTheme()
	vp := viewport.New(80, 20)
	panelVp := viewport.New(80, 20)
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7"))
	p := progress.New(progress.WithDefaultGradient())
	p.Width = 30
	gr, _ := glamour.NewTermRenderer(glamour.WithStandardStyle(styles.DarkStyle), glamour.WithWordWrap(76))
	// Get initial terminal size to avoid "Loading..." screen
	w, h := getTerminalSize()
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
	cmds := []tea.Cmd{m.input.ta.Focus(), tea.EnableBracketedPaste, m.spinner.Tick, tea.Tick(8*time.Millisecond, func(t time.Time) tea.Msg { return renderTickMsg(t) })}
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
	m.viewDirty = true
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalcLayout()
		return m, nil

	case tea.KeyMsg:
		m.viewDirty = true
		s := msg.String()
		// Filter SGR mouse sequences arriving as runes (e.g. [<65;89;32M)
		if len(s) > 5 && s[0] == '[' && s[1] == '<' && (s[len(s)-1] == 'M' || s[len(s)-1] == 'm') {
			dbg.Verbose("filtered SGR seq: %q", s)
			return m, nil
		}
		if len(s) > 3 && s[0] == '<' && (s[len(s)-1] == 'M' || s[len(s)-1] == 'm') {
			dbg.Verbose("filtered partial SGR: %q", s)
			return m, nil
		}
		// Filter cursor position reports
		if len(s) > 1 && s[len(s)-1] == 'R' && strings.ContainsAny(s, ";") {
			return m, nil
		}
		dbg.Verbose("key: %q", s)
		return m.handleKey(msg)

	case tea.MouseMsg:
		// Only handle wheel and click — ignore motion events to avoid CPU spin
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown || msg.Button == tea.MouseButtonLeft {
			return m.handleMouse(msg)
		}
		m.viewDirty = false
		return m, nil

	case eventMsg:
		m.viewDirty = true
		return m.handleEvent(engine.Event(msg))

	case batchEventsMsg:
		m.viewDirty = true
		// Process the first event (merged delta), then schedule the second
		// (non-delta) as a separate message to avoid viewport state issues.
		if len(msg) > 0 {
			_, _ = m.handleEvent(msg[0])
		}
		if len(msg) > 1 {
			remaining := msg[1]
			return m, func() tea.Msg { return eventMsg(remaining) }
		}
		return m, m.readNextEvent()

	case spinner.TickMsg:
		m.spinTick++
		if m.activity != "" {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		// Idle — don't re-tick or re-render
		m.viewDirty = false
		return m, nil

	case stopwatch.TickMsg, stopwatch.StartStopMsg:
		var cmd tea.Cmd
		m.stopwatch, cmd = m.stopwatch.Update(msg)
		return m, cmd

	case launchAgentMsg:
		return m, m.handleHandoff(string(msg))

	case renderTickMsg:
		if m.streaming || m.panel == panelAgents {
			m.updateViewport()
			return m, tea.Tick(8*time.Millisecond, func(t time.Time) tea.Msg { return renderTickMsg(t) })
		}
		// Slow tick when idle — don't trigger re-render
		m.viewDirty = false
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return renderTickMsg(t) })

	case progress.FrameMsg:
		pm, cmd := m.progress.Update(msg)
		m.progress = pm.(progress.Model)
		return m, cmd

	case modelsMsg:
		m.viewDirty = true
		items := make([]list.Item, len(msg))
		for i, m := range []string(msg) {
			items[i] = modelItem(m)
		}
		d := list.NewDefaultDelegate()
		d.SetSpacing(0)
		d.SetHeight(1)
		m.modelList = list.New(items, d, m.width-4, m.height-6)
		m.modelList.SetShowHelp(false)
		m.modelList.SetShowTitle(false)
		m.modelList.SetFilteringEnabled(true)
		m.modelList.SetShowStatusBar(false)
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
		sd := list.NewDefaultDelegate()
		sd.SetSpacing(0)
		sd.SetHeight(1)
		m.sessList = list.New(items, sd, m.width-4, m.height-6)
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
		m.vp.GotoBottom()
		return m, nil

	case newConvMsg:
		m.convID = string(msg)
		m.msgs = nil
		m.updateViewport()
		return m, nil

	case errMsg:
		m.addSystemMsg("" + error(msg).Error())
		return m, nil

	case shellEscapeMsg:
		result := string(msg)
		if result == "" {
			result = "(no output)"
		}
		m.msgs = append(m.msgs, chatMsg{role: "tool_result", content: result})
		m.updateViewport()
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
		m.providerList.SetFilteringEnabled(false)
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
		if m.panel == panelAgents {
			m.updateViewport()
		}
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
			m.streamRenderedCache = ""
			m.streamRenderedBlocks = 0
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

func (m *model) View() string {
	if m.width == 0 {
		return "Loading..."
	}
	if !m.viewDirty && m.viewCache != "" {
		return m.viewCache
	}
	m.viewDirty = false

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
			m.panelCache = pContent
			m.panelVp.SetContent(pContent)
			m.panelVp.GotoTop()
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
	var base string
	base = lipgloss.JoinVertical(lipgloss.Left, status, chatView, activityLine, inputView, helpLine)
	if m.inspector.showing {
		result := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.inspector.view(m.width, m.height), lipgloss.WithWhitespaceChars(" "))
		m.viewCache = result
		return result
	}
	// Floating confirm dialog for dangerous commands
	if m.confirmCh != nil {
		content := toolNameBadge.Render("[!] "+m.confirmCmd) + "\n\n" +
			lipgloss.NewStyle().Foreground(tokyoComment).Render(m.confirmReason) + "\n\n" +
			helpKeyStyle.Render("y") + " approve  " + helpKeyStyle.Render("n") + " deny"
		overlay := floatingDialog("Confirm Execution", content, 60)
		result := composeOverlay(base, overlay, m.width, m.height)
		m.viewCache = result
		return result
	}
	m.viewCache = base
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
	// Must match panelVp.Height exactly so list's internal scroll works
	listW := m.width - 4
	listH := chatH - 2
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

	ind := "*"
	if m.streaming {
		ind = getSpinnerFrame("thinking", m.spinTick)
	}

	modelName := m.backend.CurrentModel()
	if idx := strings.LastIndex(modelName, "/"); idx >= 0 {
		modelName = modelName[idx+1:]
	}
	if len(modelName) > 30 {
		modelName = modelName[:30]
	}

	modeInd := map[string]string{"chat": "C", "plan": "P", "build": "B"}[m.mode]
	if modeInd == "" { modeInd = "C" }

	agents := 0
	for _, t := range llm.ListBackgroundTasks() {
		if t.Status == "running" {
			agents++
		}
	}
	agentStr := ""
	if agents > 0 {
		agentStr = fmt.Sprintf(" [%d agents]", agents)
	}

	left := fmt.Sprintf(" [%s] %s %s", modeInd, ind, modelName)
	center := ""
	tokStr := fmt.Sprintf("%d", m.tokens)
	if m.tokens >= 1000000 {
		tokStr = fmt.Sprintf("%.1fM", float64(m.tokens)/1000000)
	} else if m.tokens >= 1000 {
		tokStr = fmt.Sprintf("%.1fk", float64(m.tokens)/1000)
	}
	right := fmt.Sprintf("%stok%s ", tokStr, agentStr)

	leftW := len(left)
	rightW := len(right)
	centerW := len(center)
	gap := w - leftW - rightW - centerW
	if gap < 0 {
		// Drop center if no room
		center = ""
		centerW = 0
		gap = w - leftW - rightW
		if gap < 0 { gap = 0 }
	}
	leftGap := gap / 2
	rightGap := gap - leftGap

	bar := left + strings.Repeat(" ", leftGap) + center + strings.Repeat(" ", rightGap) + right
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
		glamStyle := styles.DarkStyle
		if activeTheme == themeLight {
			glamStyle = styles.LightStyle
		}
		m.glamRenderer, _ = glamour.NewTermRenderer(glamour.WithStandardStyle(glamStyle), glamour.WithWordWrap(w-4))
		m.glamWidth = w
		m.cachedMsgCount = 0 // force re-render
	}
	// Cache rendered messages - invalidate on message count or tool detail toggle
	cacheKey := len(m.msgs)*2 + boolToInt(m.showToolDetail)
	if cacheKey != m.cachedMsgCount {
		m.cachedRender = renderBubbleMessages(m.msgs, w, m.showToolDetail, m.expandedTools, m.glamRenderer)
		m.cachedMsgCount = cacheKey
	}
	content := m.cachedRender
	if m.streaming && m.streamBuf != "" {
		display := filterToolMarkup(m.streamBuf, w)
		bubbleW := w - 6
		if bubbleW < 40 {
			bubbleW = 40
		}

		// Incremental markdown: only re-render if new line detected
		var rendered string
		if m.glamRenderer != nil {
			parts := strings.Split(display, "\n")
			completedCount := len(parts) - 1
			if completedCount > 0 && completedCount != m.streamRenderedBlocks {
				// New completed line — re-render completed portion
				completed := strings.Join(parts[:completedCount], "\n")
				glamOut, err := m.glamRenderer.Render(completed)
				if err == nil && strings.TrimSpace(glamOut) != "" {
					m.streamRenderedCache = strings.TrimRight(glamOut, "\n")
				} else {
					m.streamRenderedCache = wrapText(completed, bubbleW-4)
				}
				m.streamRenderedBlocks = completedCount
			}
			trailing := parts[len(parts)-1]
			if m.streamRenderedCache != "" {
				rendered = m.streamRenderedCache
				if trailing != "" {
					rendered += "\n" + trailing
				}
			} else {
				rendered = wrapText(display, bubbleW-4)
			}
		} else {
			rendered = wrapText(display, bubbleW-4)
		}

		var sb strings.Builder
		sb.WriteString(roleBadgeAssistant.Render(" AX ") + "\n")
		sb.WriteString(streamingBorder(m.spinTick).Width(bubbleW).Padding(0, 1).Render(rendered) + "\n")
		content += sb.String()
	}
	wasAtBottom := m.vp.AtBottom()
	m.vp.SetContent(content)
	// Auto-scroll: always during streaming, or if user was at bottom before content update
	if m.streaming || wasAtBottom {
		m.vp.GotoBottom()
	}
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
