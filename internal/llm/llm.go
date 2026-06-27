package llm

import (
	"context"
	"github.com/xnet-admin-1/ax/internal/debug"
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
	{Name: "list_dir", Description: "List directory entries", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "Directory path"},
		}, "required": []string{"path"}}},
	{Name: "search_web", Description: "Search the web", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Search query"},
		}, "required": []string{"query"}}},
	{Name: "spawn_agent", Description: "Spawn a background agent to work on a task autonomously. Returns task ID.", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"agent": map[string]any{"type": "string", "description": "Agent name from roster (or 'default' for general agent)"},
			"task":  map[string]any{"type": "string", "description": "Task description for the agent to complete"},
		}, "required": []string{"task"}}},
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
		if !ctx.TrustAll {
			if dangerous, reason := IsDangerous(command); dangerous {
				if ctx.ConfirmDangerous != nil {
					if !ctx.ConfirmDangerous(command, reason) {
						return "", fmt.Errorf("user denied")
					}
				}
			}
		}
		c, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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
