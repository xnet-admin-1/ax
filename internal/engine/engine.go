package engine

import (
	"database/sql"
	"github.com/xnet-admin-1/ax/internal/debug"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"runtime"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/xnet-admin-1/ax/internal/gateway"
	"github.com/xnet-admin-1/ax/internal/llm"
	"github.com/xnet-admin-1/ax/internal/agent"
)

const contextLimit = 128000

var thoughtRe = regexp.MustCompile(`(?s)<thought>.*?</thought>`)

type Event struct {
	Type        string
	Delta       string
	Tool        string
	ToolName    string
	ToolArgs    string
	ToolResult  string
	Args        string
	Result      string
	Error       string
	Tokens      int
	TotalTokens int
	Reasoning   string
	ConfirmCh   chan bool
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Engine struct {
	DB      *sql.DB
	Gateway *gateway.Router
	Model   string
}

var toolDefs = []map[string]any{
	{
		"type": "function",
		"function": map[string]any{
			"name":        "run_sh",
			"description": "Execute a bash command and return stdout+stderr",
			"parameters": map[string]any{
				"type":     "object",
				"required": []string{"command"},
				"properties": map[string]any{
					"command": map[string]string{"type": "string", "description": "Bash command to execute"},
				},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "read_file",
			"description": "Read file content",
			"parameters": map[string]any{
				"type":     "object",
				"required": []string{"path"},
				"properties": map[string]any{
					"path": map[string]string{"type": "string", "description": "File path to read"},
				},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "write_file",
			"description": "Write content to a file",
			"parameters": map[string]any{
				"type":     "object",
				"required": []string{"path", "content"},
				"properties": map[string]any{
					"path":    map[string]string{"type": "string", "description": "File path to write"},
					"content": map[string]string{"type": "string", "description": "Content to write"},
				},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "list_dir",
			"description": "List directory contents",
			"parameters": map[string]any{
				"type":     "object",
				"required": []string{"path"},
				"properties": map[string]any{
					"path": map[string]string{"type": "string", "description": "Directory path"},
				},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "search_web",
			"description": "Search the web via SearXNG",
			"parameters": map[string]any{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]any{
					"query": map[string]string{"type": "string", "description": "Search query"},
				},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "spawn_agent",
			"description": "Spawn a background agent. Results report back to you automatically. Available agents: architect (system design), coder (implementation), researcher (web search + synthesis), qa (testing), security (audit), devops (infrastructure), writer (documentation). Use 'default' for general tasks.",
			"parameters": map[string]any{
				"type":     "object",
				"required": []string{"agent", "task"},
				"properties": map[string]any{
					"agent": map[string]string{"type": "string", "description": "Agent name: architect, coder, researcher, qa, security, devops, writer, or default"},
					"task":  map[string]string{"type": "string", "description": "Task description for the agent"},
				},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "get_agent_result",
			"description": "Wait for a spawned agent to complete and return its result. Blocks until done (max 120s).",
			"parameters": map[string]any{
				"type":     "object",
				"required": []string{"task_id"},
				"properties": map[string]any{
					"task_id": map[string]string{"type": "string", "description": "Task ID returned by spawn_agent"},
				},
			},
		},
	},
}

func (e *Engine) SelectedModel() string {
	if e.Model != "" {
		return e.Model
	}
	var m string
	e.DB.QueryRow("SELECT value FROM settings WHERE key='selected_model'").Scan(&m)
	return m
}

func (e *Engine) SetModel(model string) {
	e.Model = model
	e.DB.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES('selected_model',?)", model)
}

func (e *Engine) Chat(ctx context.Context, messages []Message, onEvent func(Event)) error {
	model := e.SelectedModel()
	debug.D.Info("chat: model=%s msgs=%d", model, len(messages))
	if model == "" {
		return fmt.Errorf("no model selected")
	}
	apiBase, apiKey, upstreamModel, err := e.Gateway.Resolve(model)
	if err != nil {
		return err
	}

	// Prepend system prompt
	sys := systemPromptText()
	if sys != "" {
		messages = append([]Message{{Role: "system", Content: sys}}, messages...)
	}
	for {
		messages = e.maybeCompact(ctx, apiBase, apiKey, upstreamModel, messages)

		content, toolCalls, tokens, err := e.streamRequest(ctx, apiBase, apiKey, upstreamModel, messages, onEvent)
		if err != nil {
			return err
		}

		if len(toolCalls) > 0 {
			// Append assistant message with tool calls
			messages = append(messages, Message{Role: "assistant", ToolCalls: toolCalls})

			for _, tc := range toolCalls {
				onEvent(Event{Type: "tool_call", Tool: tc.Function.Name, Args: tc.Function.Arguments})
				var args map[string]any
				json.Unmarshal([]byte(tc.Function.Arguments), &args)
				toolCtx := &llm.ToolContext{
					ShellOutputLimit:  8000,
					FileReadLimit:     32000,
					FetchLimit:        8000,
					TrustAll:          true,
					SearchProviderURL: "https://search.xnet.ngo",
				}
				if e.Gateway != nil && e.DB != nil {
					mgr := agent.NewManager(e.DB, e.Gateway)
					toolCtx.SpawnAgent = func(a, t string, r ...string) (string, error) { return mgr.Spawn(a, t, r...) }
				toolCtx.Orchestrate = func(argsJSON string) string {
					// CLI orchestrate - create a Local and run
					local := &Local{DB: e.DB, Gateway: e.Gateway, AgentMgr: agent.NewManager(e.DB, e.Gateway)}
					dummyCh := make(chan Event, 64)
					go func() { for range dummyCh {} }()
					return local.ExecuteOrchestrate(argsJSON, dummyCh)
				}
				}
				if e.DB != nil {
					toolCtx.SaveMemory = func(key, value string) error {
						_, err := e.DB.Exec("INSERT OR REPLACE INTO memories(key, content) VALUES(?,?)", key, value)
						return err
					}
					toolCtx.RecallMemory = func(query string) string {
						rows, err := e.DB.Query("SELECT key, content FROM memories WHERE key LIKE ? OR content LIKE ? LIMIT 10", "%"+query+"%", "%"+query+"%")
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
						_, err := e.DB.Exec("DELETE FROM memories WHERE key=?", key)
						return err
					}
				}
				result, err := llm.ExecuteTool(tc.Function.Name, args, toolCtx)
				if err != nil {
					result = "error: " + err.Error()
				}
				onEvent(Event{Type: "tool_result", Tool: tc.Function.Name, Result: result})
				messages = append(messages, Message{Role: "tool", Content: result, Name: tc.Function.Name, ToolCallID: tc.ID})
			}
			continue
		}

		// Strip thought tags from final content
		content = strings.TrimSpace(thoughtRe.ReplaceAllString(content, ""))
		onEvent(Event{Type: "end", Tokens: tokens, Delta: content})
		return nil
	}
}

func (e *Engine) streamRequest(ctx context.Context, apiBase, apiKey, model string, messages []Message, onEvent func(Event)) (string, []ToolCall, int, error) {
	body := map[string]any{
		"model":    model,
		"messages": messages,
		"tools":    toolDefs,
		"stream":   true,
	}
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
		return "", nil, 0, fmt.Errorf("API error %d: %s", resp.StatusCode, string(b))
	}

	var content strings.Builder
	var toolCalls []ToolCall
	tcArgBuilders := map[int]*strings.Builder{}
	var tokens int
	remainder := ""

	// Read SSE stream
	rawBuf := make([]byte, 8192)
	for {
		n, readErr := resp.Body.Read(rawBuf)
		if n > 0 {
			remainder += string(rawBuf[:n])
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
				if delta.Content != "" {
					content.WriteString(delta.Content)
					onEvent(Event{Type: "delta", Delta: delta.Content})
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
						if tcArgBuilders[tc.Index] == nil {
							tcArgBuilders[tc.Index] = &strings.Builder{}
						}
						tcArgBuilders[tc.Index].WriteString(tc.Function.Arguments)
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
	for i, b := range tcArgBuilders {
		if i < len(toolCalls) {
			toolCalls[i].Function.Arguments = b.String()
		}
	}
	return content.String(), toolCalls, tokens, nil
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

func executeTool(name, argsJSON string) string {
	var args map[string]string
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "error: invalid arguments: " + err.Error()
	}

	switch name {
	case "run_sh":
		return toolRunSh(args["command"])
	case "read_file":
		return toolReadFile(args["path"])
	case "write_file":
		return toolWriteFile(args["path"], args["content"])
	case "list_dir":
		return toolListDir(args["path"])
	case "search_web":
		return toolSearchWeb(args["query"])
	default:
		return "error: unknown tool: " + name
	}
}

func toolRunSh(command string) string {
	debug.D.Verbose("tool: run_sh cmd=%q", command)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	out, err := cmd.CombinedOutput()
	result := string(out)
	if err != nil {
		result += "\n" + err.Error()
	}
	if len(result) > 8000 {
		result = result[:8000] + "\n...[truncated]"
	}
	return result
}

func toolReadFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "error: " + err.Error()
	}
	s := string(data)
	if len(s) > 32000 {
		s = s[:32000] + "\n...[truncated]"
	}
	return s
}

func toolWriteFile(path, content string) string {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "error: " + err.Error()
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "error: " + err.Error()
	}
	return "ok"
}

func toolListDir(path string) string {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "error: " + err.Error()
	}
	var sb strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			sb.WriteString(e.Name() + "/\n")
		} else {
			sb.WriteString(e.Name() + "\n")
		}
	}
	return sb.String()
}

func toolSearchWeb(query string) string {
	u := "https://search.xnet.ngo/search?q=" + url.QueryEscape(query) + "&format=json"
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(u)
	if err != nil {
		return "error: " + err.Error()
	}
	defer resp.Body.Close()
	var result struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	var sb strings.Builder
	for i, r := range result.Results {
		if i >= 5 {
			break
		}
		sb.WriteString(fmt.Sprintf("%s\n%s\n%s\n\n", r.Title, r.URL, r.Content))
	}
	return sb.String()
}

// maybeCompact summarizes older messages when token estimate exceeds 75% of context limit.
func (e *Engine) maybeCompact(ctx context.Context, apiBase, apiKey, model string, messages []Message) []Message {
	est := estimateTokens(messages)
	if est < contextLimit*3/4 {
		return messages
	}

	// Keep system message (first) and last 4 messages, summarize the middle
	keep := 4
	if len(messages) <= keep+1 {
		return messages
	}

	var toSummarize strings.Builder
	for _, m := range messages[1 : len(messages)-keep] {
		toSummarize.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, m.Content))
	}

	summaryMsgs := []Message{
		{Role: "system", Content: "Summarize the following conversation concisely, preserving key facts, decisions, and context:"},
		{Role: "user", Content: toSummarize.String()},
	}

	body := map[string]any{"model": model, "messages": summaryMsgs, "max_tokens": 1024}
	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", apiBase+"/chat/completions", strings.NewReader(string(jsonBody)))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return messages
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
		return messages
	}

	summary := result.Choices[0].Message.Content
	compacted := []Message{messages[0]}
	compacted = append(compacted, Message{Role: "user", Content: "[Conversation summary]: " + summary})
	compacted = append(compacted, messages[len(messages)-keep:]...)
	return compacted
}

func estimateTokens(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += len(m.Content)/4 + 4
		for _, tc := range m.ToolCalls {
			total += len(tc.Function.Arguments)/4 + 10
		}
	}
	return total
}

func (e *Engine) ListModels() ([]string, error) {
	return e.Gateway.ListModels(), nil
}

type Conversation struct {
	ID        string
	Title     string
	UpdatedAt int64
}

type Backend interface {
	Chat(convID, content string) (<-chan Event, error)
	Cancel(convID string)
	ListConversations(limit int) ([]Conversation, error)
	GetMessages(convID string) ([]Message, error)
	CreateConversation(title string) (string, error)
	ListModels() ([]string, error)
	ListTools() []string
	SetModel(model string) error
	CurrentModel() string
	GetDB() *sql.DB
	GetModelConfig() (ModelConfig, bool)
	GetAgentManager() interface{}
}

type ModelConfig struct {
	ContextTokens int
	AutoCompact   bool
}



func systemPromptText() string {
	hostname, _ := os.Hostname()
	cwd, _ := os.Getwd()
	u, _ := user.Current()
	username := ""
	if u != nil {
		username = u.Username
	}
	return fmt.Sprintf(`You are AX. Your name is AX. You are a personal intelligent agent running in the user's terminal with direct filesystem, shell, and network access. You are an autonomous agent — not a chatbot.

Environment: %s@%s %s (%s/%s) %s

CRITICAL: Use tools for ANY task. Use recall_memory when the user asks about preferences or past info. Use save_memory when they tell you something to remember. involving files, commands, or information. NEVER say "I can't". DO NOT describe what you would do — DO it. If a tool fails, try alternatives.

Response style: concise, direct, no filler. Fenced code blocks for code. Short answers for short questions.`,
		username, hostname, cwd, runtime.GOOS, runtime.GOARCH, time.Now().Format("2006-01-02 15:04"))
}
