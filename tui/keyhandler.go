// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/atotto/clipboard"
)

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

	// Debug panel intercepts keys
	if m.panel == panelDebug {
		if h, cmd := m.handleDebugKey(msg.String()); h {
			return m, cmd
		}
	}

	// Theme panel intercepts keys
	if m.panel == panelTheme {
		if h, cmd := m.handleThemeKey(msg.String()); h {
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
	case "W":
		// removed - was scroll
	case "S":
		// removed - was scroll
	case "shift+up":
		if m.panel != panelNone {
			m.panelVp.LineUp(1)
		} else {
			m.vp.LineUp(1)
		}
		return m, nil
	case "shift+down":
		if m.panel != panelNone {
			m.panelVp.LineDown(1)
		} else {
			m.vp.LineDown(1)
		}
		return m, nil
	case "shift+left":
		m.input.HistoryUp()
		return m, nil
	case "shift+right":
		m.input.HistoryDown()
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
	case "ctrl+a":
		// Copy all chat to clipboard
		var all strings.Builder
		for _, msg := range m.msgs {
			all.WriteString(msg.role + ": " + msg.content + "\n\n")
		}
		clipboard.WriteAll(all.String())
		m.addSystemMsg("All chat copied to clipboard")
		return m, nil
	case "ctrl+v":
		// Paste from clipboard into input
		if text, err := clipboard.ReadAll(); err == nil && text != "" {
			m.input.ta.InsertString(text)
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
	case "e":
		if m.panel == panelProvider {
			return m, m.handleProviderEdit()
		}
		if m.panel == panelMemory {
			return m, m.handleMemoryDelete()
		}
		if m.panel == panelRemote {
			return m, m.handleRemoteDelete()
		}
		if m.panel == panelAgents { if h, c := m.handleAgentsKeyStr("d"); h { return m, c } }
	case "down":
		if m.panel == panelAgents { if h, c := m.handleAgentsKeyStr("down"); h { return m, c } }
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
	}

	m.ctrlCPressed = false
	if m.panel != panelNone {
		// Route unhandled keys to the panel's list for filtering/navigation
		switch m.panel {
		case panelModels:
			m.modelList, _ = m.modelList.Update(msg)
		case panelSessions:
			m.sessList, _ = m.sessList.Update(msg)
		case panelSettings:
			m.settingsList, _ = m.settingsList.Update(msg)
		case panelConfig:
			m.configList, _ = m.configList.Update(msg)
		case panelProvider:
			m.providerList, _ = m.providerList.Update(msg)
		case panelMemory:
			m.memoryList, _ = m.memoryList.Update(msg)
		case panelSpawn:
			m.spawnList, _ = m.spawnList.Update(msg)
		case panelAgentBuilder:
			m.agentBuilderList, _ = m.agentBuilderList.Update(msg)
		case panelAgents:
			m.agentsList, _ = m.agentsList.Update(msg)
		}
		return m, nil
	}
	cmd := m.input.Update(msg)
	return m, cmd
}

