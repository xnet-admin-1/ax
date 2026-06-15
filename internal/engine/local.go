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

	"github.com/xnet-admin-1/ax/internal/gateway"
	"github.com/xnet-admin-1/ax/internal/agent"
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
		if toolID.Valid {
			m.ToolCallID = toolID.String
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
	// Prepend system prompt
	sys := l.systemPrompt()
	if sys != "" {
		messages = append([]Message{{Role: "system", Content: sys}}, messages...)
	}
	for {
		content, toolCalls, tokens, err := l.stream(ctx, apiBase, apiKey, model, messages, ch)
		if err != nil {
			ch <- Event{Type: "error", Error: err.Error()}
			return
		}
		if len(toolCalls) == 0 {
			l.DB.Exec("INSERT INTO messages(conv_id,role,content,created_at) VALUES(?,?,?,?)", convID, "assistant", content, time.Now().Unix())
			// Auto-title: if this is the first assistant response in a new conversation
			l.maybeAutoTitle(convID, apiBase, apiKey, model, ch)
			ch <- Event{Type: "end", Tokens: tokens}
			return
		}
		messages = append(messages, Message{Role: "assistant", ToolCalls: toolCalls})
		for _, tc := range toolCalls {
			ch <- Event{Type: "tool_call", ToolName: tc.Function.Name, ToolArgs: tc.Function.Arguments}
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
			}
			if l.AgentMgr == nil && l.DB != nil && l.Gateway != nil {
				l.AgentMgr = agent.NewManager(l.DB, l.Gateway)
			}
			if l.AgentMgr != nil {
				toolCtx.SpawnAgent = func(a, t string, r ...string) (string, error) { return l.AgentMgr.Spawn(a, t, r...) }
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
			var result string
			var err error
			if tc.Function.Name == "orchestrate" {
				result = l.ExecuteOrchestrate(tc.Function.Arguments, ch)
			} else {
				result, err = llm.ExecuteTool(tc.Function.Name, args, toolCtx)
			}
			if err != nil {
				result = "error: " + err.Error()
			}
			ch <- Event{Type: "tool_result", ToolName: tc.Function.Name, ToolResult: result}
			messages = append(messages, Message{Role: "tool", Content: result, Name: tc.Function.Name, ToolCallID: tc.ID})
			l.DB.Exec("INSERT INTO messages(conv_id,role,content,tool_id,created_at) VALUES(?,?,?,?,?)", convID, "tool", result, tc.ID, time.Now().Unix())
		}
	}
}

func (l *Local) stream(ctx context.Context, apiBase, apiKey, model string, messages []Message, ch chan Event) (string, []ToolCall, int, error) {
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
			if m.Content != "" {
				msg["content"] = m.Content
			}
		}
		if len(m.ToolCalls) > 0 {
			msg["tool_calls"] = m.ToolCalls
		}
		if m.ToolCallID != "" {
			msg["tool_call_id"] = m.ToolCallID
			if m.Name != "" {
				msg["name"] = m.Name
			} else {
				msg["name"] = "tool"
			}
		}
		bodyMsgs = append(bodyMsgs, msg)
	}
	body := map[string]any{"model": model, "messages": bodyMsgs, "tools": toolDefs, "stream": true}
	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", apiBase+"/chat/completions", strings.NewReader(string(jsonBody)))
	if err != nil {
		return "", nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return "", nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", nil, 0, fmt.Errorf("API error %d: %s", resp.StatusCode, b)
	}
	var content strings.Builder
	var toolCalls []ToolCall
	var inThought bool
	tcArgs := map[int]*strings.Builder{}
	var tokens int
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
				delta := chunk.Choices[0].Delta
				if delta.ReasoningContent != "" {
					content.WriteString(delta.ReasoningContent)
					ch <- Event{Type: "delta", Reasoning: delta.ReasoningContent}
				}
				if delta.Content != "" {
					content.WriteString(delta.Content)
					// Parse <thought> tags inline - split into reasoning vs content
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
							} else {
								ch <- Event{Type: "delta", Delta: text}
								text = ""
							}
						} else {
							closeIdx := strings.Index(text, "</thought>")
							if closeIdx < 0 {
								closeIdx = strings.Index(text, "</think>")
							}
							if closeIdx >= 0 {
								closeLen := 10 // len("</thought>")
								if strings.HasPrefix(text[closeIdx:], "</think>") {
									closeLen = 8
								}
								ch <- Event{Type: "delta", Reasoning: text[:closeIdx]}
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
			return "", nil, 0, readErr
		}
	}
done:
	for i, b := range tcArgs {
		if i < len(toolCalls) {
			toolCalls[i].Function.Arguments = b.String()
		}
	}
	return content.String(), toolCalls, tokens, nil
}

func (l *Local) maybeAutoTitle(convID, apiBase, apiKey, model string, ch chan Event) {
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

## Response Style
- Be concise and direct
- Show results, not process
- For code: show the relevant output, not every step
- For errors: explain what went wrong and fix it
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
