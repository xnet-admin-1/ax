// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1

package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/xnet-admin-1/ax/internal/engine"
	"github.com/xnet-admin-1/ax/internal/llm"
	"github.com/xnet-admin-1/ax/internal/mcp"
)

// --- Feature 2: Streaming Spinner ---

type cliSpinner struct {
	mu      sync.Mutex
	active  bool
	stop    chan struct{}
	stopped chan struct{}
}

func newSpinner() *cliSpinner {
	return &cliSpinner{stop: make(chan struct{}), stopped: make(chan struct{})}
}

func (s *cliSpinner) Start() {
	s.mu.Lock()
	s.active = true
	s.mu.Unlock()
	go func() {
		defer close(s.stopped)
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				fmt.Fprintf(os.Stderr, "\r\033[K")
				return
			case <-ticker.C:
				s.mu.Lock()
				if s.active {
					fmt.Fprintf(os.Stderr, "\r%s Processing...", frames[i%len(frames)])
					i++
				}
				s.mu.Unlock()
			}
		}
	}()
}

func (s *cliSpinner) Pause() {
	s.mu.Lock()
	s.active = false
	s.mu.Unlock()
	fmt.Fprintf(os.Stderr, "\r\033[K")
}

func (s *cliSpinner) Resume() {
	s.mu.Lock()
	s.active = true
	s.mu.Unlock()
}

func (s *cliSpinner) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	<-s.stopped
}

// --- Feature 1: Tool Execution with confirmation ---

func confirmToolExecution(name, args string, trustAll bool, trust bool) bool {
	if trustAll {
		return true
	}
	if trust {
		// --trust shows the tool but auto-approves
		fmt.Fprintf(os.Stderr, "  → %s(%s)\n", name, truncArgs(args, 80))
		return true
	}
	// Interactive confirmation
	fmt.Fprintf(os.Stderr, "\n🔧 Tool: %s\n   Args: %s\n   Execute? [y/N]: ", name, truncArgs(args, 120))
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
}

func truncArgs(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// --- Chat Loop with tool execution ---

func runChatLoop(ctx context.Context, eng *engine.Engine, backend *engine.Local, msgs []engine.Message, f cliFlags, spinner *cliSpinner, resp *strings.Builder, toolCalls *[]engine.ToolCall, totalTokens *int) error {
	model := eng.SelectedModel()
	if model == "" {
		return fmt.Errorf("no model selected")
	}
	apiBase, apiKey, upstreamModel, err := eng.Gateway.Resolve(model)
	if err != nil {
		return err
	}

	// Prepend system prompt
	sys := cliSystemPrompt()
	if sys != "" {
		msgs = append([]engine.Message{{Role: "system", Content: sys}}, msgs...)
	}

	maxIterations := 25 // prevent infinite tool loops
	for iter := 0; iter < maxIterations; iter++ {
		var content strings.Builder
		var calls []engine.ToolCall
		var tokens int

		err := streamRequest(ctx, apiBase, apiKey, upstreamModel, msgs, spinner, &content, &calls, &tokens)
		if err != nil {
			return err
		}
		*totalTokens += tokens

		if len(calls) == 0 {
			// Final response
			text := content.String()
			// Strip thought/reasoning tags
			text = stripThought(text)
			resp.WriteString(text)
			return nil
		}

		// Tool calls detected
		spinner.Pause()
		msgs = append(msgs, engine.Message{Role: "assistant", Content: content.String(), ToolCalls: calls})

		for _, tc := range calls {
			if !confirmToolExecution(tc.Function.Name, tc.Function.Arguments, f.trustAll, f.trust) {
				// User denied
				msgs = append(msgs, engine.Message{
					Role: "tool", Content: "error: user denied execution",
					Name: tc.Function.Name, ToolCallID: tc.ID,
				})
				continue
			}

			// Execute the tool
			var args map[string]any
			json.Unmarshal([]byte(tc.Function.Arguments), &args)
			toolCtx := &llm.ToolContext{
				ShellOutputLimit:  8000,
				FileReadLimit:     32000,
				FetchLimit:        8000,
				TrustAll:          f.trustAll,
				SearchProviderURL: "https://search.xnet.ngo",
			}
			if backend.McpMgr != nil {
				toolCtx.McpExecutor = backend.McpMgr.ExecuteTool
				toolCtx.McpInstaller = backend.McpMgr.InstallTool
			}
			if backend.DB != nil {
				toolCtx.SaveMemory = func(key, value string) error {
					_, err := backend.DB.Exec("INSERT OR REPLACE INTO memories(key, content) VALUES(?,?)", key, value)
					return err
				}
				toolCtx.RecallMemory = func(query string) string {
					rows, err := backend.DB.Query("SELECT key, content FROM memories WHERE key LIKE ? OR content LIKE ? LIMIT 10", "%"+query+"%", "%"+query+"%")
					if err != nil {
						return "no results"
					}
					defer rows.Close()
					var results []string
					for rows.Next() {
						var k, v string
						rows.Scan(&k, &v)
						results = append(results, k+": "+v)
					}
					if len(results) == 0 {
						return "no memories found"
					}
					return strings.Join(results, "\n")
				}
			}

			result, execErr := llm.ExecuteTool(tc.Function.Name, args, toolCtx)
			if execErr != nil {
				result = "error: " + execErr.Error()
			}

			// Show tool result summary
			if f.trust || f.trustAll {
				summary := result
				if len(summary) > 200 {
					summary = summary[:200] + "..."
				}
				fmt.Fprintf(os.Stderr, "  ← %s\n", strings.ReplaceAll(summary, "\n", " "))
			}

			msgs = append(msgs, engine.Message{
				Role: "tool", Content: result,
				Name: tc.Function.Name, ToolCallID: tc.ID,
			})
		}
		spinner.Resume()
	}
	return fmt.Errorf("max tool iterations reached (%d)", maxIterations)
}

// --- Streaming Request ---

func streamRequest(ctx context.Context, apiBase, apiKey, model string, messages []engine.Message, spinner *cliSpinner, content *strings.Builder, toolCalls *[]engine.ToolCall, tokens *int) error {
	// Delegate to Engine's streamRequest via the Chat method with event handler
	// We build a simplified streaming call here
	eng := &engine.Engine{}
	_ = eng // Use direct HTTP like engine does

	body := map[string]any{
		"model":    model,
		"messages": messages,
		"tools":    getToolDefs(),
		"stream":   true,
	}
	jsonBody, _ := json.Marshal(body)

	req, err := newRequestWithContext(ctx, apiBase+"/chat/completions", apiKey, jsonBody)
	if err != nil {
		return err
	}

	resp, err := httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b := make([]byte, 2048)
		n, _ := resp.Body.Read(b)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(b[:n]))
	}

	// First delta received = stop spinner
	firstDelta := true
	tcArgBuilders := map[int]*strings.Builder{}
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
					*tokens = chunk.Usage.TotalTokens
				}
				if len(chunk.Choices) == 0 {
					continue
				}
				delta := chunk.Choices[0].Delta
				if delta.Content != "" {
					if firstDelta {
						spinner.Pause()
						firstDelta = false
					}
					content.WriteString(delta.Content)
				}
				for _, tc := range delta.ToolCalls {
					for len(*toolCalls) <= tc.Index {
						*toolCalls = append(*toolCalls, engine.ToolCall{Type: "function"})
					}
					if tc.ID != "" {
						(*toolCalls)[tc.Index].ID = tc.ID
					}
					if tc.Function.Name != "" {
						(*toolCalls)[tc.Index].Function.Name = tc.Function.Name
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
		if readErr != nil {
			break
		}
	}

done:
	for i, b := range tcArgBuilders {
		if i < len(*toolCalls) {
			(*toolCalls)[i].Function.Arguments = b.String()
		}
	}
	return nil
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
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

// --- Helpers ---

func loadConversation(database *sql.DB, convID string) []engine.Message {
	rows, err := database.Query("SELECT role,content FROM messages WHERE conv_id=? ORDER BY created_at", convID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var msgs []engine.Message
	for rows.Next() {
		var m engine.Message
		rows.Scan(&m.Role, &m.Content)
		if m.Role == "user" || m.Role == "assistant" {
			msgs = append(msgs, m)
		}
	}
	return msgs
}

func stripThought(s string) string {
	// Remove <thought>...</thought>, <think>...</think>, <reasoning>...</reasoning>
	for _, tag := range []string{"thought", "think", "reasoning"} {
		for {
			start := strings.Index(s, "<"+tag+">")
			if start < 0 {
				break
			}
			end := strings.Index(s, "</"+tag+">")
			if end < 0 {
				// Unclosed tag - remove from start to end
				s = s[:start]
				break
			}
			s = s[:start] + s[end+len("</"+tag+">")+0:]
		}
	}
	return strings.TrimSpace(s)
}

func cliSystemPrompt() string {
	return `You are AX, a terminal AI agent with direct filesystem, shell, and network access. Use tools to accomplish tasks. Be concise and direct.`
}

func getToolDefs() []map[string]any {
	// Return the standard tool definitions from engine
	return []map[string]any{
		{"type": "function", "function": map[string]any{"name": "run_sh", "description": "Execute a bash command", "parameters": map[string]any{"type": "object", "required": []string{"command"}, "properties": map[string]any{"command": map[string]string{"type": "string", "description": "Bash command to execute"}}}}},
		{"type": "function", "function": map[string]any{"name": "read_file", "description": "Read file content", "parameters": map[string]any{"type": "object", "required": []string{"path"}, "properties": map[string]any{"path": map[string]string{"type": "string", "description": "File path to read"}}}}},
		{"type": "function", "function": map[string]any{"name": "write_file", "description": "Write content to a file", "parameters": map[string]any{"type": "object", "required": []string{"path", "content"}, "properties": map[string]any{"path": map[string]string{"type": "string", "description": "File path"}, "content": map[string]string{"type": "string", "description": "Content to write"}}}}},
		{"type": "function", "function": map[string]any{"name": "edit_file", "description": "Edit a file using SEARCH/REPLACE", "parameters": map[string]any{"type": "object", "required": []string{"path", "search", "replace"}, "properties": map[string]any{"path": map[string]string{"type": "string", "description": "File path"}, "search": map[string]string{"type": "string", "description": "Exact lines to find"}, "replace": map[string]string{"type": "string", "description": "Replacement"}}}}},
		{"type": "function", "function": map[string]any{"name": "list_dir", "description": "List directory contents", "parameters": map[string]any{"type": "object", "required": []string{"path"}, "properties": map[string]any{"path": map[string]string{"type": "string", "description": "Directory path"}}}}},
		{"type": "function", "function": map[string]any{"name": "search_web", "description": "Search the web", "parameters": map[string]any{"type": "object", "required": []string{"query"}, "properties": map[string]any{"query": map[string]string{"type": "string", "description": "Search query"}}}}},
	}
}

// --- Feature 11: Dry Run ---

func runDryRun(f cliFlags, eng *engine.Engine, msgs []engine.Message, convID string, mcpMgr *mcp.Manager) {
	model := eng.SelectedModel()
	fmt.Println("=== DRY RUN ===")
	fmt.Println()

	// Model info
	if model == "" {
		fmt.Println("Model: (none selected — would fail)")
	} else {
		fmt.Printf("Model: %s\n", model)
		apiBase, _, upstream, err := eng.Gateway.Resolve(model)
		if err != nil {
			fmt.Printf("  ⚠ Resolution error: %v\n", err)
		} else {
			fmt.Printf("  Endpoint: %s\n", apiBase)
			fmt.Printf("  Upstream: %s\n", upstream)
		}
	}
	fmt.Println()

	// Conversation context
	if convID != "" {
		fmt.Printf("Conversation: %s (resuming)\n", convID)
	}
	fmt.Println()

	// Messages
	fmt.Printf("Messages (%d):\n", len(msgs))
	for i, m := range msgs {
		preview := m.Content
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", "\\n")
		fmt.Printf("  [%d] %s: %s\n", i+1, m.Role, preview)
	}
	fmt.Println()

	// Tools
	tools := getToolDefs()
	fmt.Printf("Tools (%d built-in):\n", len(tools))
	for _, t := range tools {
		fn := t["function"].(map[string]any)
		fmt.Printf("  • %s\n", fn["name"])
	}
	if mcpMgr != nil {
		mcpTools := mcpMgr.GetToolDefs()
		if len(mcpTools) > 0 {
			fmt.Printf("  + %d MCP tools:\n", len(mcpTools))
			for _, t := range mcpTools {
				fmt.Printf("    • %s (%s)\n", t.Name, t.ServerID)
			}
		}
	}
	fmt.Println()

	// Flags
	fmt.Println("Flags:")
	fmt.Printf("  trust: %v\n", f.trust)
	fmt.Printf("  trust-all: %v\n", f.trustAll)
	timeout := f.timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	fmt.Printf("  timeout: %v\n", timeout)
	fmt.Printf("  format: %s\n", f.format)
	if f.agents != "" {
		fmt.Printf("  agents: %s\n", f.agents)
	}
	fmt.Println()

	// Estimated tokens
	est := 0
	for _, m := range msgs {
		est += len(m.Content)/4 + 4
	}
	fmt.Printf("Estimated input tokens: ~%d\n", est)
	fmt.Println()
	fmt.Println("=== END DRY RUN (no API call made) ===")
}
