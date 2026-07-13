// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package llm

import (
	"context"
	"github.com/xnet-admin-1/ax/internal/debug"
	"github.com/xnet-admin-1/ax/internal/edit"
	"fmt"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type ChatMessage struct {
	Source     string `json:"source,omitempty"`
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolCalls  []any  `json:"tool_calls,omitempty"`
}

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolCallInfo struct {
	ID, Name, Arguments, RawArgs string
}

type StreamEvent struct {
	Delta, Reasoning, FinishReason string
	ToolCalls                      []ToolCallInfo
	TotalTokens                    int
}

type Provider interface {
	Stream(ctx interface{}, messages []ChatMessage, model string, tools []ToolDef, onEvent func(StreamEvent)) error
}

type ToolContext struct {
	DB                interface{}
	ShellOutputLimit  int
	FileReadLimit     int
	FetchLimit        int
	McpExecutor       func(string, map[string]any) (string, error)
	McpInstaller      func(map[string]any) (string, error)
	OnProgress        func(string, string)
	TrustAll          bool
	AllowedTools      map[string]bool
	TrustedCommands   map[string]bool
	ConfirmDangerous  func(string, string) bool
	SearchProviderURL string
	SpawnAgent        func(agentName, task string, reportTo ...string) (string, error)
	GetAgentResult    func(taskID string) (string, error)
	Orchestrate       func(argsJSON string) string
	SaveMemory        func(key, value string) error
	RecallMemory      func(query string) string
	DeleteMemory      func(key string) error
	MemoryDB          interface{ Exec(string, ...any) (interface{}, error); Query(string, ...any) (interface{ Next() bool; Scan(...any) error; Close() error }, error) }
}

// NewToolContext creates a standard ToolContext with common defaults.
func NewToolContext(trustAll bool) *ToolContext {
	return &ToolContext{
		ShellOutputLimit:  8000,
		FileReadLimit:     32000,
		FetchLimit:        8000,
		TrustAll:          trustAll,
		SearchProviderURL: "https://search.xnet.ngo",
	}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

var BuiltinTools = []ToolDef{
	{Name: "run_sh", Description: "Run a shell command", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"command": map[string]any{"type": "string", "description": "Shell command to execute"},
		}, "required": []string{"command"}}},
	{Name: "read_file", Description: "Read a file", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "File path to read"},
		}, "required": []string{"path"}}},
	{Name: "write_file", Description: "Write content to a file", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "File path"},
			"content": map[string]any{"type": "string", "description": "File content"},
		}, "required": []string{"path", "content"}}},
	{Name: "edit_file", Description: "Edit a file using SEARCH/REPLACE. More precise than write_file.", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "File path to edit"},
			"search":  map[string]any{"type": "string", "description": "Exact lines to find"},
			"replace": map[string]any{"type": "string", "description": "Replacement content"},
		}, "required": []string{"path", "search", "replace"}}},
	{Name: "list_dir", Description: "List directory entries", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "Directory path"},
		}, "required": []string{"path"}}},
	{Name: "search_web", Description: "Search the web", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Search query"},
		}, "required": []string{"query"}}},
	{Name: "orchestrate", Description: "Run a multi-step pipeline of parallel and sequential agent tasks", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"stages": map[string]any{"type": "array", "description": "Array of stage objects: [{name, agent, task, depends_on?}]"},
		}, "required": []string{"stages"}}},
	{Name: "task_plan", Description: "Manage a visible task plan for multi-step work", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"action": map[string]any{"type": "string", "description": "create, complete, or update"},
			"tasks":  map[string]any{"type": "array", "description": "Task strings (for create)"},
		}, "required": []string{"action"}}},
	{Name: "install_mcp", Description: "Install/manage MCP servers for additional tools", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"action":  map[string]any{"type": "string", "description": "install, remove, list, or reconnect"},
			"name":    map[string]any{"type": "string", "description": "Server name"},
			"command": map[string]any{"type": "string", "description": "Command to run the server"},
			"args":    map[string]any{"type": "array", "description": "Arguments"},
		}, "required": []string{"action"}}},
}

func str(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func truncate(s string, limit int) string {
	if limit > 0 && len(s) > limit {
		return s[:limit] + "\n...[truncated]"
	}
	return s
}

// IsDangerous checks if a shell command matches dangerous patterns.
func IsDangerous(command string) (bool, string) {
	lower := strings.ToLower(command)
	// rm -rf / rm -r outside /tmp
	if matched, _ := regexp.MatchString(`rm\s+-(r|rf|fr)\s`, command); matched {
		if !strings.Contains(command, "/tmp") {
			return true, "recursive delete outside /tmp"
		}
	}
	for _, p := range []string{"dd ", "mkfs", "fdisk"} {
		if strings.Contains(lower, p) {
			return true, "destructive disk operation: " + p
		}
	}
	if strings.Contains(command, "chmod 777") || strings.Contains(lower, "chown ") {
		return true, "dangerous permission change"
	}
	if strings.Contains(command, "kill -9") || strings.Contains(lower, "killall ") {
		return true, "force kill process"
	}
	if strings.Contains(command, "git push --force") || strings.Contains(command, "git reset --hard") {
		return true, "destructive git operation"
	}
	if strings.Contains(lower, "drop table") || strings.Contains(lower, "drop database") {
		return true, "destructive database operation"
	}
	if matched, _ := regexp.MatchString(`>\s*/etc/|>\s*/usr/|>\s*/boot/`, command); matched {
		return true, "overwriting system file"
	}
	return false, ""
}

func ExecuteTool(name string, args map[string]any, ctx *ToolContext) (string, error) {
	debug.D.Info("tool: %s", name)
	switch name {
	case "run_sh":
		command := str(args, "command")
		debug.D.Info("run_sh: %s", command)
		timeout := 120 * time.Second
		if t, ok := args["timeout"]; ok {
			switch v := t.(type) {
			case float64:
				if v > 0 && v <= 600 {
					timeout = time.Duration(v) * time.Second
				}
			}
		}
		if !ctx.TrustAll {
			if dangerous, reason := IsDangerous(command); dangerous {
				if ctx.ConfirmDangerous != nil {
					if !ctx.ConfirmDangerous(command, reason) {
						return "", fmt.Errorf("user denied")
					}
				}
			}
		}
		c, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(c, "bash", "-c", command)
		if ctx.OnProgress != nil {
			stdout, _ := cmd.StdoutPipe()
			cmd.Stderr = cmd.Stdout
			if err := cmd.Start(); err != nil {
				return "error: " + err.Error(), nil
			}
			var out strings.Builder
			buf := make([]byte, 4096)
			for {
				n, err := stdout.Read(buf)
				if n > 0 {
					chunk := string(buf[:n])
					out.WriteString(chunk)
					ctx.OnProgress("run_sh", chunk)
				}
				if err != nil {
					break
				}
			}
			err := cmd.Wait()
			result := ansiRe.ReplaceAllString(out.String(), "")
			result = truncate(result, ctx.ShellOutputLimit)
			if err != nil {
				return result + "\nerror: " + err.Error(), nil
			}
			return result, nil
		}
		out, err := cmd.CombinedOutput()
		result := ansiRe.ReplaceAllString(string(out), "")
		result = truncate(result, ctx.ShellOutputLimit)
		if err != nil {
			return result + "\nerror: " + err.Error(), nil
		}
		return result, nil
	case "read_file":
		data, err := os.ReadFile(str(args, "path"))
		if err != nil {
			return "", err
		}
		// Detect binary files — check for null bytes in first 512 bytes
		check := data
		if len(check) > 512 {
			check = check[:512]
		}
		for _, b := range check {
			if b == 0 {
				return "error: binary file detected, cannot read as text", nil
			}
		}
		return truncate(string(data), ctx.FileReadLimit), nil
	case "write_file":
		p := str(args, "path")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, []byte(str(args, "content")), 0o644); err != nil {
			return "", err
		}
		return "ok", nil
	case "edit_file":
		p := str(args, "path")
		search := str(args, "search")
		replace := str(args, "replace")
		debug.D.Info("tool: edit_file path=%s search_len=%d replace_len=%d", p, len(search), len(replace))
		if err := edit.Apply(p, search, replace); err != nil {
			return "", err
		}
		return "ok", nil
	case "list_dir":
		entries, err := os.ReadDir(str(args, "path"))
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, e := range entries {
			if e.IsDir() {
				b.WriteString(e.Name() + "/\n")
			} else {
				b.WriteString(e.Name() + "\n")
			}
		}
		return b.String(), nil
	case "search_web":
		u := ctx.SearchProviderURL + "/search?q=" + url.QueryEscape(str(args, "query")) + "&format=json"
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; AX/1.0)")
		req.Header.Set("Accept", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return truncate(string(body), ctx.FetchLimit), nil
	case "get_agent_result":
		taskID := str(args, "task_id")
		if ctx.GetAgentResult != nil {
			return ctx.GetAgentResult(taskID)
		}
		return "get_agent_result not available", nil
	case "save_memory":
		if ctx.SaveMemory != nil {
			err := ctx.SaveMemory(str(args, "key"), str(args, "value"))
			if err != nil { return "", err }
			return "Saved: " + str(args, "key"), nil
		}
		return "memory not available", nil
	case "recall_memory":
		if ctx.RecallMemory != nil {
			return ctx.RecallMemory(str(args, "query")), nil
		}
		return "memory not available", nil
	case "delete_memory":
		if ctx.DeleteMemory != nil {
			err := ctx.DeleteMemory(str(args, "key"))
			if err != nil { return "", err }
			return "Deleted: " + str(args, "key"), nil
		}
		return "memory not available", nil
	case "orchestrate":
		if ctx.Orchestrate != nil {
			raw, _ := json.Marshal(args)
			return ctx.Orchestrate(string(raw)), nil
		}
		return "orchestrate not available in this context", nil
	case "task_plan":
		return executeTaskPlan(args), nil
	case "install_mcp":
		if ctx.McpInstaller == nil {
			return "error: MCP manager not available", nil
		}
		return ctx.McpInstaller(args)
	case "spawn_agent":
		agentName := str(args, "agent")
		if agentName == "" {
			agentName = "default"
		}
		task := str(args, "task")
		if ctx.SpawnAgent != nil {
			reportTo := str(args, "report_to")
			if reportTo == "" { reportTo = "agent" }
			id, err := ctx.SpawnAgent(agentName, task, reportTo)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Agent spawned: %s (task_id: %s)", agentName, id), nil
		}
		return "spawn_agent not available in this context", nil
	default:
		if ctx.McpExecutor != nil {
			return ctx.McpExecutor(name, args)
		}
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

var TaskModelDefaults = map[string]string{}

type BackgroundTask struct {
	Resume                                              func(string, string)
	ReportToChat                                        bool
	ID, Status, AgentName, Desc, Progress, Result, Error string
	StartedAt                                           time.Time
	History                                             []ChatMessage
}

func GetBackgroundTask(id string) *BackgroundTask   { return nil }
func ListBackgroundTasks() []*BackgroundTask         { return nil }
func CancelBackgroundTask(id string)                 {}

func Condense(ctx context.Context, provider interface{}, model string, messages []ChatMessage, keepFirst, keepLast int) ([]ChatMessage, error) {
	if len(messages) <= keepFirst+keepLast {
		return messages, nil
	}
	middle := messages[keepFirst : len(messages)-keepLast]
	var sb strings.Builder
	for _, m := range middle {
		sb.WriteString(m.Role + ": " + m.Content + "\n")
	}
	summary := ChatMessage{
		Role:    "system",
		Content: "Summary of prior conversation:\n" + sb.String(),
	}
	result := make([]ChatMessage, 0, keepFirst+1+keepLast)
	result = append(result, messages[:keepFirst]...)
	result = append(result, summary)
	result = append(result, messages[len(messages)-keepLast:]...)
	return result, nil
}

type TaskParams struct {
	Provider    Provider
	Model       string
	DisplayID   string
	Task        string
	Description string
	Prompt      string
	Background  bool
	Tools       []string
	ToolCtx     *ToolContext
	OnEvent     func(StreamEvent)
	Messages    []ChatMessage
}

func ExecuteTask(_ interface{}, _ TaskParams) ([]ChatMessage, error) { return nil, nil }

// Task Plan — visible checklist for the agent's work progress
// Persists across turns within a session (global state)
type TaskPlanItem struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Details  string `json:"details,omitempty"`
	Done     bool   `json:"done"`
	Context  string `json:"context,omitempty"`
}

type TaskPlan struct {
	Description   string         `json:"description"`
	Tasks         []TaskPlanItem `json:"tasks"`
	ModifiedFiles []string       `json:"modified_files,omitempty"`
}

var ActivePlan *TaskPlan

func executeTaskPlan(args map[string]any) string {
	action, _ := args["action"].(string)

	switch action {
	case "create":
		desc, _ := args["description"].(string)
		tasksRaw, _ := args["tasks"].([]any)
		var tasks []TaskPlanItem
		for i, t := range tasksRaw {
			switch v := t.(type) {
			case string:
				tasks = append(tasks, TaskPlanItem{ID: fmt.Sprintf("%d", i+1), Text: v})
			case map[string]any:
				text, _ := v["task_description"].(string)
				if text == "" { text, _ = v["text"].(string) }
				if text == "" { text, _ = v["task"].(string) }
				if text == "" { text, _ = v["name"].(string) }
				if text == "" { text, _ = v["description"].(string) }
				if text == "" { text = fmt.Sprintf("%v", v) }
				details, _ := v["details"].(string)
				tasks = append(tasks, TaskPlanItem{ID: fmt.Sprintf("%d", i+1), Text: text, Details: details})
			}
		}
		if len(tasks) == 0 {
			return "error: tasks array required"
		}
		ActivePlan = &TaskPlan{Description: desc, Tasks: tasks}
		return formatPlan()

	case "complete":
		if ActivePlan == nil {
			return "error: no active plan. Use action=create first."
		}
		// Support completing multiple tasks at once
		idsRaw, _ := args["completed_task_ids"].([]any)
		ctx, _ := args["context_update"].(string)
		if ctx == "" {
			ctx, _ = args["context"].(string)
		}
		// Also support single task_id
		if len(idsRaw) == 0 {
			if id, ok := args["task_id"].(string); ok && id != "" {
				idsRaw = []any{id}
			}
		}
		for _, idRaw := range idsRaw {
			id := fmt.Sprintf("%v", idRaw)
			for i := range ActivePlan.Tasks {
				if ActivePlan.Tasks[i].ID == id {
					ActivePlan.Tasks[i].Done = true
					if ctx != "" {
						ActivePlan.Tasks[i].Context = ctx
					}
				}
			}
		}
		// Track modified files
		if files, ok := args["modified_files"].([]any); ok {
			for _, f := range files {
				if s, ok := f.(string); ok {
					ActivePlan.ModifiedFiles = append(ActivePlan.ModifiedFiles, s)
				}
			}
		}
		return formatPlan()

	case "add":
		if ActivePlan == nil {
			return "error: no active plan. Use action=create first."
		}
		tasksRaw, _ := args["new_tasks"].([]any)
		if tasksRaw == nil {
			tasksRaw, _ = args["tasks"].([]any)
		}
		nextID := len(ActivePlan.Tasks) + 1
		for _, t := range tasksRaw {
			switch v := t.(type) {
			case string:
				ActivePlan.Tasks = append(ActivePlan.Tasks, TaskPlanItem{ID: fmt.Sprintf("%d", nextID), Text: v})
			case map[string]any:
				text, _ := v["task_description"].(string)
				if text == "" { text, _ = v["text"].(string) }
				if text == "" { text, _ = v["task"].(string) }
				if text == "" { text, _ = v["name"].(string) }
				if text == "" { text, _ = v["description"].(string) }
				if text == "" { text = fmt.Sprintf("%v", v) }
				details, _ := v["details"].(string)
				ActivePlan.Tasks = append(ActivePlan.Tasks, TaskPlanItem{ID: fmt.Sprintf("%d", nextID), Text: text, Details: details})
			}
			nextID++
		}
		return formatPlan()

	case "remove":
		if ActivePlan == nil {
			return "error: no active plan"
		}
		idsRaw, _ := args["remove_task_ids"].([]any)
		remove := map[string]bool{}
		for _, id := range idsRaw {
			remove[fmt.Sprintf("%v", id)] = true
		}
		var kept []TaskPlanItem
		for _, t := range ActivePlan.Tasks {
			if !remove[t.ID] {
				kept = append(kept, t)
			}
		}
		ActivePlan.Tasks = kept
		return formatPlan()

	case "list":
		return formatPlan()

	default:
		return "error: action must be create, complete, add, remove, or list"
	}
}

func formatPlan() string {
	if ActivePlan == nil {
		return "(no active plan)"
	}
	var b strings.Builder
	if ActivePlan.Description != "" {
		b.WriteString("## " + ActivePlan.Description + "\n\n")
	}
	done := 0
	for _, t := range ActivePlan.Tasks {
		mark := "[ ]"
		if t.Done {
			mark = "[x]"
			done++
		}
		fmt.Fprintf(&b, "%s %s. %s", mark, t.ID, t.Text)
		if t.Details != "" {
			b.WriteString("\n    " + t.Details)
		}
		if t.Context != "" {
			b.WriteString("\n    > " + t.Context)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\nProgress: %d/%d complete", done, len(ActivePlan.Tasks))
	if len(ActivePlan.ModifiedFiles) > 0 {
		b.WriteString("\nModified: " + strings.Join(ActivePlan.ModifiedFiles, ", "))
	}
	return b.String()
}
