// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package tui

import (
	"os"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xnet-admin-1/ax/internal/engine"
)

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
		return m, tea.Batch(m.readNextEvent(), m.spinner.Tick, m.stopwatch.Reset(), m.stopwatch.Start())
	case "tool_result":
		content := ev.ToolResult
		if content == "" {
			content = "(ok)"
		}
		elapsed := m.stopwatch.Elapsed()
		if elapsed > 0 {
			content = fmt.Sprintf("[%s] %s", elapsed.Round(time.Millisecond), content)
		}
		m.msgs = append(m.msgs, chatMsg{role: "tool_result", content: content})
		m.activity = ""
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
		m.addSystemMsg(fmt.Sprintf("[!] Dangerous: %s (%s) — y/n?", ev.ToolName, ev.ToolResult))
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

