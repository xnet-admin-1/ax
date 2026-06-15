package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type panelType int

const (
	panelNone panelType = iota
	panelHelp
	panelModels
	panelSessions
	panelTools
	panelUsage
	panelKnowledge
	panelSettings
	panelMCP
	panelConfig // kept for backward compat, unused
	panelCompact
	panelProvider
	panelMemory
	panelRemote
	panelVectors
	panelAgents
	panelSpawn
	panelAgentBuilder
	panelTree
)

func (m *model) handleCommand(input string) tea.Cmd {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}
	cmd := parts[0]

	switch cmd {
	case "/help":
		m.togglePanel(panelHelp)
	case "/model":
		if len(parts) > 1 {
			m.backend.SetModel(parts[1])
			m.addSystemMsg("Model → " + parts[1])
		} else {
			m.panel = panelModels
			return m.loadModels()
		}
	case "/new":
		return m.newConversation()
	case "/list":
		m.panel = panelTree
		return m.loadTree()
		return m.loadSessions()
	case "/knowledge":
		if len(parts) > 1 {
			sub := parts[1]
			switch sub {
			case "add":
				if len(parts) < 3 {
					m.addSystemMsg("Usage: /knowledge add <path>")
					return nil
				}
				return m.knowledgeAdd(strings.Join(parts[2:], " "))
			case "search":
				if len(parts) < 3 {
					m.addSystemMsg("Usage: /knowledge search <query>")
					return nil
				}
				return m.searchKnowledge(strings.Join(parts[2:], " "))
			case "list":
				return m.knowledgeList()
			case "rm":
				if len(parts) < 3 {
					m.addSystemMsg("Usage: /knowledge rm <path>")
					return nil
				}
				return m.knowledgeDelete(strings.Join(parts[2:], " "))
			default:
				return m.searchKnowledge(strings.Join(parts[1:], " "))
			}
		}
		m.togglePanel(panelKnowledge)
	case "/tools":
		m.panel = panelTools
		return m.loadToolsPanel()
	case "/usage":
		m.togglePanel(panelUsage)
	case "/compact":
		m.panel = panelCompact
		return nil
	case "/clear":
		m.msgs = nil
		m.updateViewport()
	case "/settings":
		m.panel = panelSettings
		return m.loadSettingsPanel()
	case "/mcp":
		m.panel = panelMCP
		return m.loadMCPPanel()
	case "/provider":
		m.panel = panelProvider
		return m.loadProviderPanelFixed()
	case "/memory":
		m.panel = panelMemory
		return m.loadMemoryPanel()
	case "/remote":
		m.panel = panelRemote
		return m.loadRemotePanel()
	case "/vectors":
		m.panel = panelVectors
		return m.loadVectorsPanel()
	case "/export":
		return m.exportConversation()
	case "/agent":
		m.panel = panelAgentBuilder
		return m.loadAgentBuilderPanel()
	case "/monitor":
		m.panel = panelAgents
		return m.loadAgentsPanel()
	case "/attach":
		if len(parts) > 1 {
			path := strings.Join(parts[1:], " ")
			m.attachPath = path
			m.addSystemMsg("Attached: " + path + " (will include in next message)")
		} else {
			m.addSystemMsg("Usage: /attach <filepath>")
		}
	case "/status":
		return m.checkStatus()
	case "/fork":
		return m.forkConversation()
	case "/handoff":
		parts := strings.SplitN(input, " ", 2)
		if len(parts) < 2 || parts[1] == "" {
			m.addSystemMsg("Usage: /handoff <agent_name>")
			return nil
		}
		return m.handleHandoff(strings.TrimSpace(parts[1]))
	case "/return":
		m.handleReturn()
		return nil
	case "/chain":
		// /chain agent1,agent2,agent3 task text
		chainParts := strings.SplitN(input, " ", 3)
		if len(chainParts) < 3 {
			m.addSystemMsg("Usage: /chain agent1,agent2,agent3 task text")
			return nil
		}
		agents := strings.Split(chainParts[1], ",")
		return m.chainAgents(agents, chainParts[2])
	case "/spawn":
		// /spawn agent1,agent2,agent3 task text = parallel spawn
		spawnParts := strings.SplitN(input, " ", 3)
		if len(spawnParts) == 3 && strings.Contains(spawnParts[1], ",") {
			agents := strings.Split(spawnParts[1], ",")
			return m.spawnParallel(agents, spawnParts[2])
		}
		m.panel = panelSpawn
		return m.loadSpawnPanel()
	case "/mode":
		if len(parts) > 1 {
			switch parts[1] {
			case "chat", "plan", "build":
				m.mode = parts[1]
			default:
				m.addSystemMsg("Unknown mode: " + parts[1] + " (chat/plan/build)")
				return nil
			}
		} else {
			switch m.mode {
			case "chat":
				m.mode = "plan"
			case "plan":
				m.mode = "build"
			default:
				m.mode = "chat"
			}
		}
		m.addSystemMsg("Mode: " + m.mode)
	case "/exit":
		return tea.Quit
}
	return nil
}

// panelView renders the active panel content
func (m *model) panelView(width int) string {
	switch m.panel {
	case panelHelp:
		return helpText
	case panelModels:
		return "Models  enter=select  esc=close\n\n" + m.modelList.View()
	case panelSessions:
		return "Sessions  enter=open  n=rename  d=delete  esc=close\n\n" + m.sessList.View()
	case panelTools:
		return m.toolsPanelView()
	case panelUsage:
		return m.usageView()
	case panelSettings:
		return m.settingsPanelView()
	case panelMCP:
		return m.mcpPanelView()
	case panelProvider:
		return m.providerPanelView()
	case panelMemory:
		return m.memoryPanelView()
	case panelRemote:
		return m.remotePanelViewFixed()
	case panelSpawn:
		return m.spawnPanelView()
	case panelAgentBuilder:
		return m.agentBuilderView()
	case panelAgents:
		return m.agentsTreeView()
	case panelVectors:
		return "Vectors  r=reindex  esc=close\n\n" + m.vectorsStats
	case panelCompact:
		return m.compactPanelView()
	case panelKnowledge:
		return "Knowledge Base\n\nUse /knowledge <query> to search"
	case panelTree:
		return m.treePanelView()
	}
	return ""
}



func (m *model) usageView() string {
	return fmt.Sprintf("Usage:\n\n  Messages: %d\n  Tokens:   %d\n  Model:    %s\n  Conv:     %s",
		len(m.msgs), m.tokens, m.backend.CurrentModel(), m.convID)
}



func (m *model) setConfig(key, value string) tea.Cmd {
	return func() tea.Msg {
		db := m.backend.GetDB()
		if db == nil {
			return nil
		}
		db.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES(?,?)", key, key, value)
		return knowledgeMsg("" + key + " → " + value)
	}
}

const helpText = `Commands:

  /help        Toggle this panel
  /model [id]  Switch model or open picker
  /new         New conversation
  /list        Conversations
  /knowledge   Search knowledge base
  /tools       Show available tools
  /usage       Show token usage
  /settings    Settings panel
  /mcp         MCP server management
  /settings    View/edit settings
  /compact     Compact context
  /clear       Clear chat display
  /provider    Manage LLM providers
  /memory      Persistent memories
  /remote      Remote server management
  /vectors     Vector store stats/reindex
  /attach      Attach file to next message
  /export      Export conversation to file
  /status      Check connectivity
  /fork        Fork current conversation
  /agent       Agent builder (create/edit)
  /monitor     Monitor background agents
  /handoff     Hand conversation to specialist
  /return      Return from handoff
  /chain       Chain agents sequentially (output→input)
  /spawn       Spawn agent(s) - comma-separate for parallel
  /mode        Cycle mode (chat/plan/build)
  /todos       List todo items
  /exit        Exit

Keybindings:

  ctrl+c       Cancel / exit (2x)
  ctrl+x       Cut input to clipboard
  ctrl+y       Copy last response to clipboard
  ctrl+d       Scroll down
  ctrl+u       Scroll up
  ctrl+n       New conversation
  ctrl+o       Toggle tool detail
  esc          Close panel
  enter        Send / select
  tab          Accept autocomplete
`

// Compact panel
var compactMethods = []string{"LLM Summary", "Keep Last N", "Sliding Window"}

func (m *model) compactPanelView() string {
	var b strings.Builder
	b.WriteString("Compact  up/down=select  enter=run  esc=close\n\n")
	for i, method := range compactMethods {
		cursor := "  "
		if i == m.compactIdx {
			cursor = "> "
		}
		b.WriteString("  " + cursor + method + "\n")
	}
	return b.String()
}

func (m *model) listTodos() tea.Cmd {
	return func() tea.Msg {
		db := m.backend.GetDB()
		if db == nil {
			return knowledgeMsg("No database available")
		}
		rows, err := db.Query("SELECT id,text,done FROM todos ORDER BY created_at DESC")
		if err != nil {
			return knowledgeMsg("Error: " + err.Error())
		}
		defer rows.Close()
		var b strings.Builder
		b.WriteString("Todos:\n")
		count := 0
		for rows.Next() {
			var id, text string
			var done int
			rows.Scan(&id, &text, &done)
			mark := "[ ]"
			if done == 1 {
				mark = "[x]"
			}
			fmt.Fprintf(&b, "  %s %s %s\n", mark, id, text)
			count++
		}
		if count == 0 {
			return knowledgeMsg("No todos.")
		}
		return knowledgeMsg(b.String())
	}
}

func (m *model) handleCompactEnter() tea.Cmd {
	switch m.compactIdx {
	case 0: // LLM Summary
		m.panel = panelNone
		m.addSystemMsg("Compacting (LLM summary)...")
		return m.compactContext()
	case 1: // Keep Last N
		m.panel = panelNone
		if len(m.msgs) > 10 {
			m.msgs = m.msgs[len(m.msgs)-10:]
			m.updateViewport()
		}
		m.addSystemMsg("Kept last 10 messages")
		return nil
	case 2: // Sliding Window
		m.panel = panelNone
		if len(m.msgs) > 20 {
			m.msgs = append(
				[]chatMsg{{role: "assistant", content: "_Context trimmed (sliding window)_"}},
				m.msgs[len(m.msgs)-16:]...,
			)
			m.updateViewport()
		}
		m.addSystemMsg("Sliding window applied")
		return nil
	}
	return nil
}
