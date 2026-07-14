// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"runtime"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/xnet-admin-1/ax/internal/agent"
	"github.com/xnet-admin-1/ax/internal/gateway"
	"github.com/xnet-admin-1/ax/internal/mcp"
	"github.com/xnet-admin-1/ax/internal/llm"
)

var imagePathRe = regexp.MustCompile(`(?i)(\S+\.(?:png|jpg|jpeg|gif|webp))`)

func detectAndEncodeImages(content string) (any, bool) {
	matches := imagePathRe.FindAllString(content, -1)
	if len(matches) == 0 {
		return nil, false
	}
	var parts []map[string]any
	foundImage := false
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(path))
		mime := "image/png"
		switch ext {
		case ".jpg", ".jpeg":
			mime = "image/jpeg"
		case ".gif":
			mime = "image/gif"
		case ".webp":
			mime = "image/webp"
		}
		parts = append(parts, map[string]any{
			"type":      "image_url",
			"image_url": map[string]string{"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)},
		})
		foundImage = true
	}
	if !foundImage {
		return nil, false
	}
	text := strings.TrimSpace(imagePathRe.ReplaceAllString(content, ""))
	if text == "" {
		text = "What is in this image?"
	}
	result := []map[string]any{{"type": "text", "text": text}}
	result = append(result, parts...)
	return result, true
}


func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type Local struct {
	DB             *sql.DB
	Gateway        *gateway.Router
	AgentMgr       *agent.Manager
	McpMgr         *mcp.Manager
	Provider       interface{}
	Mode           string
	TrustAll       bool
	OverridePrompt string
	OverrideTools  []string
	mu             sync.Mutex
	cancels        map[string]context.CancelFunc
}

func NewLocal(db *sql.DB, gw *gateway.Router) *Local {
	return &Local{DB: db, Gateway: gw, cancels: make(map[string]context.CancelFunc)}
}

func (l *Local) GetDB() *sql.DB                    { return l.DB }
func (l *Local) GetModelConfig() (ModelConfig, bool) { return ModelConfig{ContextTokens: contextLimit, AutoCompact: true}, true }
func (l *Local) ListModels() ([]string, error)      { return l.Gateway.ListModels(), nil }

func (l *Local) CurrentModel() string {
	var m string
	l.DB.QueryRow("SELECT value FROM settings WHERE key='selected_model'").Scan(&m)
	return m
}

func (l *Local) SetModel(model string) error {
	_, err := l.DB.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES('selected_model',?)", model)
	return err
}

func (l *Local) ListTools() []string {
	names := make([]string, 0, len(toolDefs))
	for _, td := range toolDefs {
		if fn, ok := td["function"].(map[string]any); ok {
			names = append(names, fn["name"].(string))
		}
	}
	return names
}

func (l *Local) CreateConversation(title string) (string, error) {
	id, now := newID(), time.Now().Unix()
	_, err := l.DB.Exec("INSERT INTO conversations(id,title,model,created_at,updated_at) VALUES(?,?,?,?,?)", id, title, l.CurrentModel(), now, now)
	return id, err
}

func (l *Local) ListConversations(limit int) ([]Conversation, error) {
	rows, err := l.DB.Query("SELECT id,title,updated_at FROM conversations ORDER BY updated_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		rows.Scan(&c.ID, &c.Title, &c.UpdatedAt)
		out = append(out, c)
	}
	return out, nil
}

func (l *Local) GetMessages(convID string) ([]Message, error) {
	rows, err := l.DB.Query("SELECT role,content,tool_id FROM messages WHERE conv_id=? ORDER BY created_at", convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var toolID sql.NullString
		rows.Scan(&m.Role, &m.Content, &toolID)
		if toolID.Valid && toolID.String != "" && m.Role == "tool" {
			parts := strings.SplitN(toolID.String, "|", 2)
			if len(parts) == 2 {
				m.Name = parts[0]
				m.ToolCallID = parts[1]
			} else {
				m.ToolCallID = toolID.String
			}
		}
		// Restore ToolCalls on assistant messages
		if m.Role == "assistant" && toolID.Valid && toolID.String != "" {
			var tc []ToolCall
			if json.Unmarshal([]byte(toolID.String), &tc) == nil && len(tc) > 0 {
				m.ToolCalls = tc
			}
		}
		// Keep tool messages as-is for Bedrock compatibility
		if m.Role == "tool" {
			if len(m.Content) > 2000 {
				m.Content = m.Content[:2000] + "...[truncated]"
			}
			out = append(out, m)
			continue
		}
		// Skip truly empty assistant messages (no content AND no tool calls)
		if m.Role == "assistant" && m.Content == "" && len(m.ToolCalls) == 0 {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

func (l *Local) Cancel(convID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cancel, ok := l.cancels[convID]; ok {
		cancel()
		delete(l.cancels, convID)
	}
}

func (l *Local) Chat(convID, content string) (<-chan Event, error) {
	if convID == "" {
		convID = newID()
	}
	model := l.CurrentModel()
	if model == "" {
		return nil, fmt.Errorf("no model selected")
	}
	apiBase, apiKey, upstream, err := l.Gateway.Resolve(model)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	// Ensure conversation exists
	l.DB.Exec("INSERT OR IGNORE INTO conversations(id,title,model,created_at,updated_at) VALUES(?,?,?,?,?)", convID, "New Chat", model, now, now)
	res, insertErr := l.DB.Exec("INSERT INTO messages(conv_id,role,content,created_at) VALUES(?,?,?,?)", convID, "user", content, now)
	if insertErr != nil {
		return nil, fmt.Errorf("insert message failed: %w (convID=%s)", insertErr, convID)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("message not inserted (convID=%s)", convID)
	}
	l.DB.Exec("UPDATE conversations SET updated_at=? WHERE id=?", now, convID)

	ctx, cancel := context.WithCancel(context.Background())
	l.mu.Lock()
	l.cancels[convID] = cancel
	l.mu.Unlock()

	ch := make(chan Event, 64)
	go l.chatLoop(ctx, ch, convID, apiBase, apiKey, upstream)
	return ch, nil
}

func (l *Local) chatLoop(ctx context.Context, ch chan Event, convID, apiBase, apiKey, model string) {
	defer close(ch)
	defer func() { l.mu.Lock(); delete(l.cancels, convID); l.mu.Unlock() }()
	messages, _ := l.GetMessages(convID)
	// Cap history: ~200 tokens/msg avg, keep 75% of 64k context budget = 240 msgs
	// Determine context window based on model
	ctxWindow := contextLimit
	if IsBedrockProvider(apiBase) {
		meta := GetBedrockModelMeta(model)
		if meta.ContextWindow > 0 {
			ctxWindow = meta.ContextWindow
		}
	}
	maxMsgs := (ctxWindow * 75 / 100) / 200
	if maxMsgs < 20 {
		maxMsgs = 20
	}
	if len(messages) > maxMsgs {
		messages = messages[len(messages)-maxMsgs:]
	}
	// Prepend system prompt
	sys := l.systemPrompt()
	if sys != "" {
		messages = append([]Message{{Role: "system", Content: sys}}, messages...)
	}
	for {
		// Compact messages if approaching context limit (keeps tools working at high token counts)
		messages = l.compactIfNeeded(ctx, apiBase, apiKey, model, messages)

		var content string
		var toolCalls []ToolCall
		var tokens int
		var finishReason string
		var err error

		if IsBedrockProvider(apiBase) {
			region := ParseBedrockRegion(apiBase)
			content, toolCalls, tokens, finishReason, err = l.bedrockStream(ctx, region, apiKey, model, messages, ch)
		} else {
			content, toolCalls, tokens, finishReason, err = l.stream(ctx, apiBase, apiKey, model, messages, ch)
		}
		if err != nil {
			ch <- Event{Type: "error", Error: err.Error()}
			return
		}
		if len(toolCalls) == 0 {
			// If response was cut off due to max_tokens, continue the conversation
			if finishReason == "length" && content != "" {
				messages = append(messages, Message{Role: "assistant", Content: content})
				messages = append(messages, Message{Role: "user", Content: "Continue from where you left off. Keep using tools as needed."})
				continue
			}
			// Detect text-mode tool calls (model outputs JSON instead of using function calling)
			if parsed := detectTextToolCalls(content); len(parsed) > 0 {
				// Emit any content BEFORE the tool call as assistant text
				preContent := extractPreToolContent(content)
				if preContent != "" {
					ch <- Event{Type: "delta", Delta: preContent}
				}

				// Build the assistant message with the full original content
				messages = append(messages, Message{Role: "assistant", Content: content})

				// Execute each tool call and collect results
				var toolResultSummary strings.Builder
				for _, tc := range parsed {
					ch <- Event{Type: "tool_call", Tool: tc.name, ToolName: tc.name, ToolArgs: tc.argsRaw}
					textToolCtx := &llm.ToolContext{
						ShellOutputLimit:  8000,
						FileReadLimit:     32000,
						TrustAll:          l.TrustAll,
						SearchProviderURL: "https://search.xnet.ngo",
						OnProgress: func(name, chunk string) {
							ch <- Event{Type: "progress", ToolName: name, ToolResult: chunk}
						},
					}
					if l.McpMgr != nil {
						textToolCtx.McpExecutor = l.McpMgr.ExecuteTool
						textToolCtx.McpInstaller = l.McpMgr.InstallTool
					}
					if l.DB != nil {
						textToolCtx.SaveMemory = func(key, value string) error {
							_, err := l.DB.Exec("INSERT OR REPLACE INTO memories(key, content) VALUES(?,?)", key, value)
							return err
						}
						textToolCtx.RecallMemory = func(query string) string {
							rows, err := l.DB.Query("SELECT key, content FROM memories WHERE key LIKE ? OR content LIKE ? LIMIT 10", "%"+query+"%", "%"+query+"%")
							if err != nil { return "no results" }
							defer rows.Close()
							var results []string
							for rows.Next() {
								var k, v string
								rows.Scan(&k, &v)
								results = append(results, k+": "+v)
							}
							if len(results) == 0 { return "no memories found" }
							return strings.Join(results, "\n")
						}
					}
					result, execErr := llm.ExecuteTool(tc.name, tc.args, textToolCtx)
					if execErr != nil {
						result = "error: " + execErr.Error()
					}
					ch <- Event{Type: "tool_result", Tool: tc.name, ToolName: tc.name, ToolResult: result}
					fmt.Fprintf(&toolResultSummary, "[%s result]:\n%s\n\n", tc.name, truncateToolResult(result))
				}

				// Feed results back as a user message (text-mode models don't understand structured tool messages)
				messages = append(messages, Message{Role: "user", Content: "[Tool execution results — continue with your response]\n\n" + toolResultSummary.String()})
				l.DB.Exec("INSERT INTO messages(conv_id,role,content,created_at) VALUES(?,?,?,?)", convID, "assistant", content, time.Now().Unix())
				continue
			}
			l.DB.Exec("INSERT INTO messages(conv_id,role,content,created_at) VALUES(?,?,?,?)", convID, "assistant", content, time.Now().Unix())
			// Auto-title: if this is the first assistant response in a new conversation
			if content != "" {
				l.maybeAutoTitle(convID, apiBase, apiKey, model, ch)
			}
			ch <- Event{Type: "end", Tokens: tokens}
			return
		}
		messages = append(messages, Message{Role: "assistant", Content: content, ToolCalls: toolCalls})
		// Persist assistant tool-call message immediately for crash recovery
		tcJSON, _ := json.Marshal(toolCalls)
		// Always persist assistant messages (tool calls need to be in history)
		l.DB.Exec("INSERT INTO messages(conv_id,role,content,tool_id,created_at) VALUES(?,?,?,?,?)",
			convID, "assistant", content, string(tcJSON), time.Now().Unix())
		for _, tc := range toolCalls {
			ch <- Event{Type: "tool_call", Tool: tc.ID, ToolName: tc.Function.Name, ToolArgs: tc.Function.Arguments}
			var args map[string]any
			json.Unmarshal([]byte(tc.Function.Arguments), &args)
			toolCtx := &llm.ToolContext{
				ShellOutputLimit:  8000,
				FileReadLimit:     32000,
				TrustAll:          l.TrustAll,
				SearchProviderURL: "https://search.xnet.ngo",
				OnProgress: func(name, chunk string) {
					ch <- Event{Type: "progress", ToolName: name, ToolResult: chunk}
				},
				ConfirmDangerous: func(command, reason string) bool {
					confirmCh := make(chan bool, 1)
					ch <- Event{Type: "confirm", ToolName: command, ToolResult: reason, ConfirmCh: confirmCh}
					return <-confirmCh
				},
			}
			if l.AgentMgr == nil && l.DB != nil && l.Gateway != nil {
				l.AgentMgr = agent.NewManager(l.DB, l.Gateway)
			}
			if l.AgentMgr != nil {
				toolCtx.SpawnAgent = func(a, t string, r ...string) (string, error) { return l.AgentMgr.Spawn(a, t, r...) }
				toolCtx.Orchestrate = func(argsJSON string) string { return l.ExecuteOrchestrate(argsJSON, ch) }
				toolCtx.GetAgentResult = func(taskID string) (string, error) {
					for i := 0; i < 120; i++ {
						t := l.AgentMgr.GetTask(taskID)
						if t == nil {
							return "", fmt.Errorf("task %s not found", taskID)
						}
						if t.Status == "done" || t.Status == "error" {
							return t.Result, nil
						}
						time.Sleep(time.Second)
					}
					return "", fmt.Errorf("timeout waiting for task %s", taskID)
				}
			}
			toolCtx.SaveMemory = func(key, value string) error {
				_, err := l.DB.Exec("INSERT OR REPLACE INTO memories(key, content) VALUES(?,?)", key, value)
				return err
			}
			toolCtx.RecallMemory = func(query string) string {
				rows, err := l.DB.Query("SELECT key, content FROM memories WHERE key LIKE ? OR content LIKE ? LIMIT 10", "%"+query+"%", "%"+query+"%")
				if err != nil { return "no results" }
				defer rows.Close()
				var results []string
				for rows.Next() {
					var k, v string
					rows.Scan(&k, &v)
					results = append(results, k+": "+v)
				}
				if len(results) == 0 { return "no memories found for: "+query }
				return strings.Join(results, "\n")
			}
			toolCtx.DeleteMemory = func(key string) error {
				_, err := l.DB.Exec("DELETE FROM memories WHERE key=?", key)
				return err
			}
			if l.McpMgr != nil {
				toolCtx.McpExecutor = l.McpMgr.ExecuteTool
				toolCtx.McpInstaller = l.McpMgr.InstallTool
			}
			result, err := llm.ExecuteTool(tc.Function.Name, args, toolCtx)
			if err != nil {
				result = "error: " + err.Error()
			}
			ch <- Event{Type: "tool_result", Tool: tc.ID, ToolName: tc.Function.Name, ToolResult: result}
			messages = append(messages, Message{Role: "tool", Content: truncateToolResult(result), Name: tc.Function.Name, ToolCallID: tc.ID})
			l.DB.Exec("INSERT INTO messages(conv_id,role,content,tool_id,created_at) VALUES(?,?,?,?,?)", convID, "tool", truncateToolResult(result), tc.Function.Name+"|"+tc.ID, time.Now().Unix())
		}
	}
}

// compactIfNeeded truncates tool results and summarizes old messages when the
// conversation approaches 60% of the context limit. This prevents models from
// silently dropping tool use when the context gets too large.
func (l *Local) compactIfNeeded(ctx context.Context, apiBase, apiKey, model string, messages []Message) []Message {
	est := estimateTokens(messages)
	// Use model-specific context window
	ctxWindow := contextLimit
	if IsBedrockProvider(apiBase) {
		meta := GetBedrockModelMeta(model)
		if meta.ContextWindow > 0 {
			ctxWindow = meta.ContextWindow
		}
	}
	threshold := ctxWindow * 75 / 100

	if est < threshold {
		return messages
	}

	// Strategy 1: Aggressively truncate old tool results (keep last 6 tool results full)
	toolResultCount := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			toolResultCount++
		}
	}
	if toolResultCount > 6 {
		truncated := 0
		for i := 0; i < len(messages); i++ {
			if messages[i].Role == "tool" && truncated < toolResultCount-6 {
				// Truncate old tool results to 500 chars
				if len(messages[i].Content) > 500 {
					messages[i].Content = messages[i].Content[:500] + "\n...[compacted]"
				}
				truncated++
			}
		}
	}

	// Re-check after truncation
	est = estimateTokens(messages)
	if est < threshold {
		return messages
	}

	// Strategy 2: Summarize old messages (keep system + last 10 messages)
	keep := 10
	if len(messages) <= keep+1 {
		return messages
	}

	// Find system message
	sysIdx := -1
	for i, m := range messages {
		if m.Role == "system" {
			sysIdx = i
			break
		}
	}

	// Build summary of middle messages
	start := 0
	if sysIdx >= 0 {
		start = sysIdx + 1
	}
	end := len(messages) - keep

	if end <= start {
		return messages
	}

	// Ensure we don't split a tool call pair — walk 'end' backward until we hit
	// a clean boundary (not a tool message, and not an assistant with tool_calls)
	for end > start {
		if messages[end].Role == "tool" {
			end--
		} else if messages[end].Role == "assistant" && len(messages[end].ToolCalls) > 0 {
			end--
		} else {
			break
		}
	}
	if end <= start {
		return messages
	}

	var toSummarize strings.Builder
	toSummarize.WriteString("Conversation summary (prior tool calls and results omitted for brevity):\n")
	for _, m := range messages[start:end] {
		if m.Role == "tool" {
			// Just note tool was called, don't include full result
			name := m.Name
			if name == "" {
				name = "tool"
			}
			fmt.Fprintf(&toSummarize, "[%s]: (result: %d chars)\n", name, len(m.Content))
		} else if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			// Note tool calls without full args
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&toSummarize, "[assistant called %s]\n", tc.Function.Name)
			}
			if m.Content != "" {
				preview := m.Content
				if len(preview) > 200 {
					preview = preview[:200]
				}
				fmt.Fprintf(&toSummarize, "[assistant]: %s\n", preview)
			}
		} else {
			preview := m.Content
			if len(preview) > 300 {
				preview = preview[:300]
			}
			fmt.Fprintf(&toSummarize, "[%s]: %s\n", m.Role, preview)
		}
	}

	// Replace middle with a single summary message
	summaryMsg := Message{
		Role:    "user",
		Content: "[Context compacted to stay within limits]\n" + toSummarize.String(),
	}

	var compacted []Message
	if sysIdx >= 0 {
		compacted = append(compacted, messages[sysIdx])
	}
	compacted = append(compacted, summaryMsg)
	compacted = append(compacted, messages[end:]...)

	return compacted
}

func (l *Local) stream(ctx context.Context, apiBase, apiKey, model string, messages []Message, ch chan Event) (string, []ToolCall, int, string, error) {
	if messages == nil {
		messages = []Message{}
	}
	// Build body messages with multimodal support for images
	bodyMsgs := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		msg := map[string]any{"role": m.Role}
		if m.Role == "user" {
			if multimodal, ok := detectAndEncodeImages(m.Content); ok {
				msg["content"] = multimodal
			} else {
				msg["content"] = m.Content
			}
		} else {
			msg["content"] = m.Content
		}
		if len(m.ToolCalls) > 0 {
			msg["tool_calls"] = m.ToolCalls
		}
		if m.ToolCallID != "" && m.Role == "tool" {
			msg["tool_call_id"] = m.ToolCallID
			if m.Name != "" {
				msg["name"] = m.Name
			} else {
				msg["name"] = "tool"
			}
		}
		bodyMsgs = append(bodyMsgs, msg)
	}
	// Build tools list - builtin + MCP
	allTools := toolDefs
	if l.McpMgr != nil {
		for _, t := range l.McpMgr.GetToolDefs() {
			allTools = append(allTools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.InputSchema,
				},
			})
		}
	}
	body := map[string]any{"model": model, "messages": bodyMsgs, "tools": allTools, "stream": true, "tool_choice": "auto", "max_tokens": 16384}
	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", apiBase+"/chat/completions", strings.NewReader(string(jsonBody)))
	if err != nil {
		return "", nil, 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return "", nil, 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", nil, 0, "", fmt.Errorf("API error %d: %s", resp.StatusCode, b)
	}
	var content strings.Builder
	var toolCalls []ToolCall
	var inThought bool
	tcArgs := map[int]*strings.Builder{}
	var tokens int
	var finishReason string
	var remainder string
	buf := make([]byte, 8192)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			remainder += string(buf[:n])
			lines := strings.Split(remainder, "\n")
			remainder = lines[len(lines)-1]
			for _, line := range lines[:len(lines)-1] {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				data := line[6:]
				if data == "[DONE]" {
					goto done
				}
				var chunk streamChunk
				if json.Unmarshal([]byte(data), &chunk) != nil {
					continue
				}
				if chunk.Usage.TotalTokens > 0 {
					tokens = chunk.Usage.TotalTokens
				}
				if len(chunk.Choices) == 0 {
					continue
				}
				if chunk.Choices[0].FinishReason != "" {
					finishReason = chunk.Choices[0].FinishReason
				}
				delta := chunk.Choices[0].Delta
				if delta.ReasoningContent != "" {
					content.WriteString(delta.ReasoningContent)
					ch <- Event{Type: "delta", Reasoning: delta.ReasoningContent}
				}
				if delta.Reasoning != "" {
					content.WriteString(delta.Reasoning)
					ch <- Event{Type: "delta", Reasoning: delta.Reasoning}
				}
				if delta.Content != "" {
					content.WriteString(delta.Content)
					// Parse <thought>/<think>/<reasoning> tags inline
					text := delta.Content
					for text != "" {
						if !inThought {
							if idx := strings.Index(text, "<thought>"); idx >= 0 {
								if idx > 0 {
									ch <- Event{Type: "delta", Delta: text[:idx]}
								}
								inThought = true
								text = text[idx+9:]
							} else if idx := strings.Index(text, "<think>"); idx >= 0 {
								if idx > 0 {
									ch <- Event{Type: "delta", Delta: text[:idx]}
								}
								inThought = true
								text = text[idx+7:]
							} else if idx := strings.Index(text, "<reasoning>"); idx >= 0 {
								if idx > 0 {
									ch <- Event{Type: "delta", Delta: text[:idx]}
								}
								inThought = true
								text = text[idx+11:]
							} else {
								ch <- Event{Type: "delta", Delta: text}
								text = ""
							}
						} else {
							closeIdx := strings.Index(text, "</thought>")
							closeLen := 10
							if closeIdx < 0 {
								closeIdx = strings.Index(text, "</think>")
								closeLen = 8
							}
							if closeIdx < 0 {
								closeIdx = strings.Index(text, "</reasoning>")
								closeLen = 12
							}
							if closeIdx >= 0 {
								if closeIdx > 0 {
									ch <- Event{Type: "delta", Reasoning: text[:closeIdx]}
								}
								inThought = false
								text = text[closeIdx+closeLen:]
							} else {
								ch <- Event{Type: "delta", Reasoning: text}
								text = ""
							}
						}
					}
				}
				for _, tc := range delta.ToolCalls {
					for len(toolCalls) <= tc.Index {
						toolCalls = append(toolCalls, ToolCall{Type: "function"})
					}
					if tc.ID != "" {
						toolCalls[tc.Index].ID = tc.ID
					}
					if tc.Function.Name != "" {
						toolCalls[tc.Index].Function.Name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						if tcArgs[tc.Index] == nil {
							tcArgs[tc.Index] = &strings.Builder{}
						}
						tcArgs[tc.Index].WriteString(tc.Function.Arguments)
					}
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", nil, 0, "", readErr
		}
	}
done:
	for i, b := range tcArgs {
		if i < len(toolCalls) {
			toolCalls[i].Function.Arguments = b.String()
		}
	}
	return content.String(), toolCalls, tokens, finishReason, nil
}

const maxToolResultSize = 131072 // 128KB

func truncateToolResult(s string) string {
	if len(s) <= maxToolResultSize {
		return s
	}
	return s[:maxToolResultSize] + "\n\n[truncated — " + fmt.Sprintf("%d", len(s)) + " bytes total]"
}

func (l *Local) maybeAutoTitle(convID, apiBase, apiKey, model string, ch chan Event) {
	// Skip LLM-based auto-title for Bedrock (uses first user message instead)
	if IsBedrockProvider(apiBase) {
		var firstMsg string
		l.DB.QueryRow("SELECT content FROM messages WHERE conv_id=? AND role='user' ORDER BY created_at LIMIT 1", convID).Scan(&firstMsg)
		if firstMsg != "" {
			title := firstMsg
			if len(title) > 50 { title = title[:50] }
			l.DB.Exec("UPDATE conversations SET title=? WHERE id=?", title, convID)
			ch <- Event{Type: "title", Delta: title}
		}
		return
	}
	// Check if conversation has only 2 messages (user + assistant) = new conversation
	var count int
	l.DB.QueryRow("SELECT COUNT(*) FROM messages WHERE conv_id=?", convID).Scan(&count)
	if count != 2 {
		return
	}
	var firstMsg string
	l.DB.QueryRow("SELECT content FROM messages WHERE conv_id=? AND role='user' ORDER BY created_at LIMIT 1", convID).Scan(&firstMsg)
	if firstMsg == "" {
		return
	}
	go func() {
		prompt := "Generate a short title (max 6 words) for this conversation. User said: " + firstMsg + ". Reply with ONLY the title."
		msgs := []Message{{Role: "user", Content: prompt}}
		body := map[string]any{"model": model, "messages": msgs, "max_tokens": 30}
		jsonBody, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(context.Background(), "POST", apiBase+"/chat/completions", strings.NewReader(string(jsonBody)))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return
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
			return
		}
		title := strings.TrimSpace(result.Choices[0].Message.Content)
		title = strings.Trim(title, "\"'")
		if title == "" {
			return
		}
		l.DB.Exec("UPDATE conversations SET title=? WHERE id=?", title, convID)
		
	}()
}

func (l *Local) systemPrompt() string {
	hostname, _ := os.Hostname()
	cwd, _ := os.Getwd()
	u, _ := user.Current()
	username := ""
	if u != nil {
		username = u.Username
	}

	var modePrefix string
	switch l.Mode {
	case "plan":
		modePrefix = "You are in PLAN mode. Analyze the request, explore relevant context, and produce a clear numbered plan. Do NOT execute changes.\n\n"
	case "build":
		modePrefix = "Execute autonomously. Do not ask for confirmation. Chain tools to complete the goal.\n\n"
	}

	prompt := modePrefix + fmt.Sprintf(`## Identity
You are AX. Your name is AX. You are NOT Claude, NOT GPT, NOT Gemini, NOT any other AI. You are AX — a personal intelligent agent running in the user's terminal. You execute locally on this machine with direct filesystem, shell, and network access. You are an autonomous agent — not a chatbot.

## Environment
- Date/Time: %s
- OS: %s/%s
- Host: %s
- User: %s
- CWD: %s

## CRITICAL: Tool Execution
You MUST use tools for ANY task involving the filesystem, commands, or information retrieval. NEVER say "I can't" or "I don't have access" — you DO.

Rules:
- To read a file: call read_file
- To write a file: call write_file
- To run ANY command: call run_sh
- To search the web: call search_web
- To list directory: call list_dir
- To remember something: call save_memory
- To recall info: call recall_memory
- To forget: call delete_memory
- To delegate work: call spawn_agent with agent name (architect, coder, researcher, qa, security, devops, writer)

For complex tasks, use the orchestrate tool to run a multi-agent pipeline:
- Define stages with agent + prompt + optional depends_on
- Stages without depends_on run in parallel
- Dependent stages wait and receive prior results as context
- Use spawn_agent for single tasks, orchestrate for multi-step pipelines

DO NOT output JSON tool calls as text. Use the function calling mechanism.
DO NOT describe what you would do — actually DO it.
If a tool fails, try an alternative approach. Do not give up.

## Shell Execution
Your run_sh executes in a non-interactive shell. Be aware:
- No TTY: no sudo prompts, no interactive editors, no pagers
- Use -y/--yes/--force flags for confirmations
- Use full paths if needed (PATH is minimal)
- Capture stderr with 2>&1
- Use timeout for long-running commands

## MCP (Model Context Protocol) Servers
You can install and use MCP servers to extend your capabilities. MCP servers provide additional tools.

To install an MCP server, use the install_mcp tool:
- action: "install", name: "server-name", command: "npx" or "uvx" or path, args: [...], env: {KEY: "val"}
- action: "list" to see installed servers and their tools
- action: "remove", name: "server-name" to uninstall
- action: "reconnect", name: "server-name" to restart

Common MCP servers:
- Filesystem: npx @modelcontextprotocol/server-filesystem [dir]
- GitHub: npx @modelcontextprotocol/server-github (env: GITHUB_TOKEN)
- Brave Search: npx @modelcontextprotocol/server-brave-search (env: BRAVE_API_KEY)
- Puppeteer: npx @modelcontextprotocol/server-puppeteer
- SQLite: npx @modelcontextprotocol/server-sqlite [db-path]
- Memory: npx @modelcontextprotocol/server-memory
- Playwright: npx @playwright/mcp@latest

Once installed, MCP tools appear alongside your built-in tools. Use them naturally.
Config is saved to ~/.ax/mcp.json and servers auto-connect on startup.

## Task Planning
For multi-step work, use the task_plan tool to show progress:
- action: "create" with description + tasks array
- action: "complete" with completed_task_ids + context_update
- action: "add" to append new tasks
- action: "list" to show current plan

## Response Style
- Be concise and direct
- Show results, not process
- For code: show the relevant output, not every step
- For errors: explain what went wrong and fix it

## REMINDER: Always Use Tools
Even deep into a conversation, you MUST continue using tools. Do NOT start describing actions instead of performing them. If you find yourself writing "I would run..." or "You could try..." — STOP and call the tool instead. Your tool access never expires. Use it on every turn that requires action.
`, time.Now().Format("2006-01-02 15:04:05 MST"),
		runtime.GOOS, runtime.GOARCH,
		hostname, username, cwd)

	// Append memories if available
	if l.DB != nil {
		rows, err := l.DB.Query("SELECT key, content FROM memories ORDER BY key LIMIT 20")
		if err == nil {
			defer rows.Close()
			var mem strings.Builder
			for rows.Next() {
				var k, v string
				rows.Scan(&k, &v)
				if mem.Len() == 0 {
					mem.WriteString("\n## Memories\n")
				}
				fmt.Fprintf(&mem, "- %s: %s\n", k, v)
			}
			prompt += mem.String()
		}
	}
	return prompt
}

func (l *Local) GetAgentManager() interface{} {
	if l.AgentMgr == nil && l.DB != nil && l.Gateway != nil {
		l.AgentMgr = agent.NewManager(l.DB, l.Gateway)
	}
	return l.AgentMgr
}

// detectTextToolCalls finds tool calls output as text by models that don't support function calling.
// Handles formats:
// - [TOOL_CALLS]toolname{json} (Mistral)
// - {"name": "toolname", "parameters": {...}} (generic)
type textToolCall struct {
	name    string
	args    map[string]any
	argsRaw string
}

// extractPreToolContent returns any text content that appears before the first
// tool call marker in the response. This ensures the model's prose isn't swallowed.
func extractPreToolContent(content string) string {
	// Strip thinking blocks first
	stripped := regexp.MustCompile(`(?s)<think>.*?</think>`).ReplaceAllString(content, "")
	stripped = regexp.MustCompile(`(?s)<thought>.*?</thought>`).ReplaceAllString(stripped, "")
	stripped = regexp.MustCompile(`(?s)<reasoning>.*?</reasoning>`).ReplaceAllString(stripped, "")

	// Find the earliest tool call marker
	markers := []string{
		"[TOOL_CALLS]",
		"<function=",
		"<tool_call>",
		"<minimax:tool_call>",
	}
	earliest := -1
	for _, marker := range markers {
		idx := strings.Index(stripped, marker)
		if idx >= 0 && (earliest < 0 || idx < earliest) {
			earliest = idx
		}
	}
	// Also check for JSON tool calls: {"name":
	if idx := strings.Index(stripped, `{"name"`); idx >= 0 && (earliest < 0 || idx < earliest) {
		lineStart := strings.LastIndex(stripped[:idx], "\n")
		if lineStart < 0 {
			lineStart = 0
		}
		line := strings.TrimSpace(stripped[lineStart:])
		if strings.HasPrefix(line, "{") && (strings.Contains(line, `"arguments"`) || strings.Contains(line, `"parameters"`)) {
			if lineStart > 0 {
				earliest = lineStart
			} else {
				earliest = idx
			}
		}
	}
	// Check for ```json tool call blocks
	if idx := strings.Index(stripped, "```json"); idx >= 0 && (earliest < 0 || idx < earliest) {
		// Only count if it contains a tool call pattern
		afterBlock := stripped[idx:]
		if strings.Contains(afterBlock, `"name"`) && (strings.Contains(afterBlock, `"arguments"`) || strings.Contains(afterBlock, `"parameters"`)) {
			earliest = idx
		}
	}

	if earliest <= 0 {
		return ""
	}
	pre := strings.TrimSpace(stripped[:earliest])
	return pre
}

func detectTextToolCalls(content string) []textToolCall {
	var found []textToolCall

	// Strip <think>/<thought>/<reasoning> blocks — reasoning shouldn't trigger tool detection
	stripped := regexp.MustCompile(`(?s)<think>.*?</think>`).ReplaceAllString(content, "")
	stripped = regexp.MustCompile(`(?s)<thought>.*?</thought>`).ReplaceAllString(stripped, "")
	stripped = regexp.MustCompile(`(?s)<reasoning>.*?</reasoning>`).ReplaceAllString(stripped, "")
	stripped = regexp.MustCompile(`(?s)<think>.*$`).ReplaceAllString(stripped, "")
	stripped = regexp.MustCompile(`(?s)<thought>.*$`).ReplaceAllString(stripped, "")
	stripped = regexp.MustCompile(`(?s)<reasoning>.*$`).ReplaceAllString(stripped, "")

	// Format 1: [TOOL_CALLS]toolname{json} (Mistral)
	if idx := strings.Index(stripped, "[TOOL_CALLS]"); idx >= 0 {
		rest := stripped[idx+12:]
		braceIdx := strings.Index(rest, "{")
		if braceIdx > 0 {
			name := strings.TrimSpace(rest[:braceIdx])
			argsStr := rest[braceIdx:]
			var args map[string]any
			if json.Unmarshal([]byte(argsStr), &args) == nil {
				found = append(found, textToolCall{name: name, args: args, argsRaw: argsStr})
			}
		}
	}

	// Format 2: <tool_call>{"name":"x","arguments":{...}}</tool_call> (Qwen/Hermes)
	if len(found) == 0 {
		tcRe := regexp.MustCompile(`(?s)<tool_call>\s*(\{.*?\})\s*</tool_call>`)
		for _, match := range tcRe.FindAllStringSubmatch(stripped, -1) {
			var tc struct {
				Name       string         `json:"name"`
				Arguments  map[string]any `json:"arguments"`
				Parameters map[string]any `json:"parameters"`
			}
			if json.Unmarshal([]byte(match[1]), &tc) == nil && tc.Name != "" {
				args := tc.Arguments
				if args == nil {
					args = tc.Parameters
				}
				found = append(found, textToolCall{name: tc.Name, args: args, argsRaw: match[1]})
			}
		}
	}

	// Format 3: <function=name> <parameter=key>value (ChatGPT-like XML)
	if len(found) == 0 {
		funcRe := regexp.MustCompile(`<function=([^>]+)>`)
		paramRe := regexp.MustCompile(`<parameter=([^>]+)>([^<]*)`)
		funcMatches := funcRe.FindAllStringSubmatchIndex(stripped, -1)
		for _, match := range funcMatches {
			name := stripped[match[2]:match[3]]
			rest := stripped[match[1]:]
			args := map[string]any{}
			paramMatches := paramRe.FindAllStringSubmatch(rest, -1)
			for _, pm := range paramMatches {
				if len(pm) >= 3 {
					args[pm[1]] = strings.TrimSpace(pm[2])
				}
			}
			if len(args) > 0 {
				raw, _ := json.Marshal(args)
				found = append(found, textToolCall{name: name, args: args, argsRaw: string(raw)})
			}
		}
	}

	// Format 4: <minimax:tool_call><invoke name="x"><parameter name="k">v</parameter></invoke></minimax:tool_call>
	if len(found) == 0 {
		blockRe := regexp.MustCompile(`(?s)<minimax:tool_call>(.*?)</minimax:tool_call>`)
		invokeRe := regexp.MustCompile(`(?s)<invoke name=["']?([^"'>]+)["']?>(.*?)</invoke>`)
		paramRe := regexp.MustCompile(`(?s)<parameter name=["']?([^"'>]+)["']?>(.*?)</parameter>`)
		for _, block := range blockRe.FindAllStringSubmatch(stripped, -1) {
			for _, invoke := range invokeRe.FindAllStringSubmatch(block[1], -1) {
				name := strings.TrimSpace(invoke[1])
				args := map[string]any{}
				for _, param := range paramRe.FindAllStringSubmatch(invoke[2], -1) {
					args[strings.TrimSpace(param[1])] = strings.TrimSpace(param[2])
				}
				if len(args) > 0 {
					raw, _ := json.Marshal(args)
					found = append(found, textToolCall{name: name, args: args, argsRaw: string(raw)})
				}
			}
		}
	}

	// Format 5: ```json blocks with "name" and "arguments"/"parameters"
	if len(found) == 0 {
		codeRe := regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")
		for _, match := range codeRe.FindAllStringSubmatch(stripped, -1) {
			var tc struct {
				Name       string         `json:"name"`
				Arguments  map[string]any `json:"arguments"`
				Parameters map[string]any `json:"parameters"`
			}
			if json.Unmarshal([]byte(match[1]), &tc) == nil && tc.Name != "" {
				args := tc.Arguments
				if args == nil {
					args = tc.Parameters
				}
				if args != nil {
					found = append(found, textToolCall{name: tc.Name, args: args, argsRaw: match[1]})
				}
			}
		}
	}

	// Format 6: Standalone JSON {"name":"x","arguments":{...}} or {"name":"x","parameters":{...}}
	if len(found) == 0 {
		for _, line := range strings.Split(stripped, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "{") && strings.Contains(line, `"name"`) && (strings.Contains(line, `"arguments"`) || strings.Contains(line, `"parameters"`) || strings.Contains(line, `"parameter"`)) {
				var tc struct {
					Name       string         `json:"name"`
					Arguments  map[string]any `json:"arguments"`
					Parameters map[string]any `json:"parameters"`
					Parameter  map[string]any `json:"parameter"`
				}
				if json.Unmarshal([]byte(line), &tc) == nil && tc.Name != "" {
					args := tc.Arguments
					if args == nil {
						args = tc.Parameters
					}
					if args == nil {
						args = tc.Parameter
					}
					found = append(found, textToolCall{name: tc.Name, args: args, argsRaw: line})
				}
			}
		}
	}

	return found
}
