package agent

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xnet-admin-1/ax/internal/gateway"
	"github.com/xnet-admin-1/ax/internal/llm"
)

type Agent struct {
	Name         string   `json:"name"`
	SystemPrompt string   `json:"systemPrompt"`
	Model        string   `json:"model"`
	Tools        []string `json:"tools"`
}

type TaskEvent struct {
	Type   string // delta, tool_call, tool_result, progress, done, error
	Text   string
}

type Task struct {
	ID        string
	Agent     string
	Status    string // running, done, error
	Result    string
	StartedAt time.Time
	ReportTo  string // "user" or "agent"
	Log       []TaskEvent
	Events    chan TaskEvent
	cancel    context.CancelFunc
	mu        sync.Mutex
}

func (t *Task) emit(ev TaskEvent) {
	t.mu.Lock()
	t.Log = append(t.Log, ev)
	t.mu.Unlock()
	select {
	case t.Events <- ev:
	default:
	}
}

func (t *Task) GetLog() []TaskEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := make([]TaskEvent, len(t.Log))
	copy(cp, t.Log)
	return cp
}

type Manager struct {
	DB      *sql.DB
	Gateway *gateway.Router
	mu      sync.Mutex
	tasks   map[string]*Task
}

func NewManager(db *sql.DB, gw *gateway.Router) *Manager {
	return &Manager{DB: db, Gateway: gw, tasks: make(map[string]*Task)}
}

var DefaultRoster = []Agent{
	{Name: "architect", SystemPrompt: `You are AX:architect — a senior software architect agent. You have full filesystem and shell access.

Your role: analyze requirements, design systems, and produce actionable architecture plans.

Process:
1. Read existing code/docs to understand current state
2. Identify components, boundaries, and data flows
3. Produce numbered plan with clear responsibilities per component
4. Define API contracts, data models, and integration points
5. Flag risks, trade-offs, and dependencies

Output format: structured markdown with diagrams (ASCII), tables for API specs, and clear next-steps. Never produce code — that's the coder's job. Focus on WHAT and WHY, not HOW.`},

	{Name: "coder", SystemPrompt: `You are AX:coder — an expert implementation agent. You have full filesystem and shell access.

Your role: write clean, complete, production-ready code.

Rules:
- Read existing code first to match style, conventions, and patterns
- Write complete implementations — no TODOs, no placeholders, no "exercise for the reader"
- Include error handling, edge cases, and input validation
- Run tests/build after changes to verify correctness
- If tests fail, fix them before reporting done
- Use the project's existing dependencies — don't introduce new ones without reason

Process: read context → implement → verify (build/test) → report result.`},

	{Name: "researcher", SystemPrompt: `You are AX:researcher — a research and analysis agent. You have web search, file access, and shell.

Your role: find information, synthesize findings, and produce actionable summaries.

Process:
1. Search the web for relevant sources (use search_web)
2. Read documentation, source code, or files as needed
3. Cross-reference multiple sources for accuracy
4. Produce concise summary with key findings, links, and recommendations

Output format: structured report with sections, bullet points, and source attribution. Distinguish facts from opinions. Flag confidence level (high/medium/low) for claims.`},

	{Name: "qa", SystemPrompt: `You are AX:qa — a quality assurance and testing agent. You have full filesystem and shell access.

Your role: verify correctness, find bugs, write tests, and ensure quality.

Process:
1. Read the code/feature under test
2. Identify edge cases, boundary conditions, and failure modes
3. Write tests (unit, integration) using the project's test framework
4. Run tests and report results
5. If bugs found: describe the bug, steps to reproduce, expected vs actual, and severity

Focus on: correctness, error handling, security implications, performance issues, and race conditions.`},

	{Name: "security", SystemPrompt: `You are AX:security — a security audit agent. You have full filesystem and shell access.

Your role: identify vulnerabilities, assess risk, and recommend fixes.

Process:
1. Read code, configs, and infrastructure definitions
2. Check for OWASP Top 10, CWE common weaknesses
3. Review auth/authz, input validation, secrets management
4. Check dependencies for known CVEs
5. Produce findings with severity (critical/high/medium/low), impact, and remediation

Focus on: injection, auth bypass, data exposure, misconfig, supply chain, and privilege escalation.`},

	{Name: "devops", SystemPrompt: `You are AX:devops — an infrastructure and operations agent. You have full filesystem and shell access.

Your role: handle deployment, infrastructure, CI/CD, containers, and cloud operations.

Capabilities: Docker, systemd, AWS CLI, Terraform, shell scripting, networking, monitoring.

Process:
1. Assess current infrastructure state
2. Plan changes with rollback strategy
3. Implement with idempotent operations
4. Verify health after changes
5. Document what was done

Always: use --dry-run first when available, check service health after changes, preserve existing configs with backups.`},

	{Name: "writer", SystemPrompt: `You are AX:writer — a technical documentation agent. You have full filesystem and shell access.

Your role: produce clear, accurate, well-structured documentation.

Types: READMEs, API docs, guides, changelogs, architecture docs, inline code comments.

Process:
1. Read the code/system being documented
2. Identify the audience (developer, user, ops)
3. Structure with clear headings, examples, and cross-references
4. Use consistent terminology matching the codebase
5. Include: purpose, usage, configuration, troubleshooting

Style: concise, scannable, example-driven. No filler. Code examples must be tested/runnable.`},
}

func (m *Manager) GetRoster() []Agent {
	var raw string
	err := m.DB.QueryRow("SELECT value FROM settings WHERE key='agent_roster'").Scan(&raw)
	if err != nil || raw == "" {
		// Seed default roster
		m.SaveRoster(DefaultRoster)
		return DefaultRoster
	}
	var agents []Agent
	json.Unmarshal([]byte(raw), &agents)
	if len(agents) == 0 {
		m.SaveRoster(DefaultRoster)
		return DefaultRoster
	}
	return agents
}

func (m *Manager) SaveRoster(agents []Agent) error {
	data, err := json.Marshal(agents)
	if err != nil {
		return err
	}
	_, err = m.DB.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES('agent_roster',?)", string(data))
	return err
}

func (m *Manager) Spawn(agentName, task string, reportTo ...string) (string, error) {
	roster := m.GetRoster()
	var ag *Agent
	for i := range roster {
		if roster[i].Name == agentName {
			ag = &roster[i]
			break
		}
	}
	if ag == nil {
		// Default agent — use current model with generic prompt
		ag = &Agent{Name: agentName, SystemPrompt: "You are a helpful assistant. Complete the given task."}
	}

	model := ag.Model
	if model == "" {
		var sel string
		m.DB.QueryRow("SELECT value FROM settings WHERE key='selected_model'").Scan(&sel)
		if sel == "" {
			m.DB.QueryRow("SELECT value FROM settings WHERE key='selected_model'").Scan(&sel)
		}
		model = sel
	}
	if model == "" {
		return "", fmt.Errorf("no model configured")
	}

	apiBase, apiKey, upstream, err := m.Gateway.Resolve(model)
	if err != nil {
		return "", err
	}

	id := newID()
	ctx, cancel := context.WithCancel(context.Background())
	rt := "user"
	if len(reportTo) > 0 { rt = reportTo[0] }
	t := &Task{ID: id, Agent: agentName, Status: "running", StartedAt: time.Now(), Events: make(chan TaskEvent, 64), ReportTo: rt, cancel: cancel}
	m.mu.Lock()
	m.tasks[id] = t
	m.mu.Unlock()

	go m.run(ctx, t, ag, upstream, apiBase, apiKey, task)
	return id, nil
}

func (m *Manager) run(ctx context.Context, t *Task, ag *Agent, model, apiBase, apiKey, task string) {
	messages := []chatMsg{
		{Role: "system", Content: ag.SystemPrompt},
		{Role: "user", Content: task},
	}
	for turn := 0; turn < 20; turn++ {
		body := map[string]any{"model": model, "messages": messages}
		if turn == 0 || len(messages) > 2 {
			body["tools"] = agentToolDefs
		}
		jsonBody, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, "POST", apiBase+"/chat/completions", strings.NewReader(string(jsonBody)))
		if err != nil { m.finish(t, "", err); return }
		req.Header.Set("Content-Type", "application/json")
		if apiKey != "" { req.Header.Set("Authorization", "Bearer "+apiKey) }
		resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
		if err != nil { m.finish(t, "", err); return }
		if resp.StatusCode >= 500 {
			resp.Body.Close()
			time.Sleep(2 * time.Second)
			continue // retry on server error
		}
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body); resp.Body.Close()
			if resp.StatusCode == 400 && turn == 0 {
				// Retry without tools
				body2 := map[string]any{"model": model, "messages": messages}
				jsonBody2, _ := json.Marshal(body2)
				req2, _ := http.NewRequestWithContext(ctx, "POST", apiBase+"/chat/completions", strings.NewReader(string(jsonBody2)))
				req2.Header.Set("Content-Type", "application/json")
				if apiKey != "" { req2.Header.Set("Authorization", "Bearer "+apiKey) }
				resp2, err2 := (&http.Client{Timeout: 5 * time.Minute}).Do(req2)
				if err2 != nil { m.finish(t, "", err2); return }
				if resp2.StatusCode == 200 {
					var res2 struct { Choices []struct { Message struct { Content string } } }
					json.NewDecoder(resp2.Body).Decode(&res2); resp2.Body.Close()
					if len(res2.Choices) > 0 { m.finish(t, res2.Choices[0].Message.Content, nil); return }
				}
				resp2.Body.Close()
			}
			m.finish(t, "", fmt.Errorf("API %d: %s", resp.StatusCode, b)); return
		}
		var res struct {
			Choices []struct {
				Message struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						ID string `json:"id"`
						Function struct { Name, Arguments string } `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		json.NewDecoder(resp.Body).Decode(&res); resp.Body.Close()
		if len(res.Choices) == 0 { m.finish(t, "", fmt.Errorf("empty response")); return }
		msg := res.Choices[0].Message
		if msg.Content != "" {
			t.emit(TaskEvent{Type: "delta", Text: msg.Content})
		}
		if len(msg.ToolCalls) == 0 { m.finish(t, msg.Content, nil); return }
		// Rebuild tool_calls with type field
		var tcs []map[string]any
		for _, tc := range msg.ToolCalls {
			tcs = append(tcs, map[string]any{"id": tc.ID, "type": "function", "function": map[string]any{"name": tc.Function.Name, "arguments": tc.Function.Arguments}})
		}
		messages = append(messages, chatMsg{Role: "assistant", Content: msg.Content, ToolCalls: tcs})
		for _, tc := range msg.ToolCalls {
			t.emit(TaskEvent{Type: "tool_call", Text: tc.Function.Name + "(" + tc.Function.Arguments + ")"})
			var args map[string]any
			json.Unmarshal([]byte(tc.Function.Arguments), &args)
			result := m.executeAgentTool(tc.Function.Name, args)
			t.emit(TaskEvent{Type: "tool_result", Text: result})
			messages = append(messages, chatMsg{Role: "tool", Content: result, Name: tc.Function.Name, ToolCallID: tc.ID})
		}
	}
	m.finish(t, "max turns reached", nil)
}

func (m *Manager) finish(t *Task, result string, err error) {
	m.mu.Lock()
	if err != nil {
		t.Status = "error"
		t.Result = err.Error()
	} else {
		t.Status = "done"
		t.Result = result
	}
	m.mu.Unlock()
	t.emit(TaskEvent{Type: "done", Text: t.Result})
	close(t.Events)
}

func (m *Manager) GetTask(id string) *Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[id]
}

func (m *Manager) ListTasks() []*Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t)
	}
	return out
}

func (m *Manager) Cancel(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok && t.cancel != nil {
		t.cancel()
		t.Status = "error"
		t.Result = "cancelled"
	}
}

type chatMsg struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	Name       string `json:"name,omitempty"`
	ToolCalls  any    `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

var agentToolDefs = []map[string]any{
	{"type": "function", "function": map[string]any{"name": "run_sh", "description": "Execute bash command", "parameters": map[string]any{"type": "object", "required": []string{"command"}, "properties": map[string]any{"command": map[string]string{"type": "string", "description": "Command"}}}}},
	{"type": "function", "function": map[string]any{"name": "read_file", "description": "Read file", "parameters": map[string]any{"type": "object", "required": []string{"path"}, "properties": map[string]any{"path": map[string]string{"type": "string", "description": "Path"}}}}},
	{"type": "function", "function": map[string]any{"name": "write_file", "description": "Write file", "parameters": map[string]any{"type": "object", "required": []string{"path", "content"}, "properties": map[string]any{"path": map[string]string{"type": "string", "description": "Path"}, "content": map[string]string{"type": "string", "description": "Content"}}}}},
	{"type": "function", "function": map[string]any{"name": "list_dir", "description": "List directory", "parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]string{"type": "string", "description": "Path"}}}}},
	{"type": "function", "function": map[string]any{"name": "search_web", "description": "Search the web", "parameters": map[string]any{"type": "object", "required": []string{"query"}, "properties": map[string]any{"query": map[string]string{"type": "string", "description": "Query"}}}}},
	{"type": "function", "function": map[string]any{"name": "spawn_agent", "description": "Spawn a sub-agent to work on a subtask. Available: architect, coder, researcher, qa, security, devops, writer.", "parameters": map[string]any{"type": "object", "required": []string{"agent", "task"}, "properties": map[string]any{"agent": map[string]string{"type": "string", "description": "Agent name"}, "task": map[string]string{"type": "string", "description": "Task"}}}}},
}

func (m *Manager) executeAgentTool(name string, args map[string]any) string {
	ctx := &llm.ToolContext{ShellOutputLimit: 8000, FileReadLimit: 32000, FetchLimit: 8000, TrustAll: true, SearchProviderURL: "https://search.xnet.ngo"}
	ctx.SpawnAgent = func(a, t string, r ...string) (string, error) { return m.Spawn(a, t, r...) }
	ctx.GetAgentResult = func(taskID string) (string, error) {
		for i := 0; i < 120; i++ {
			t := m.GetTask(taskID)
			if t != nil && (t.Status == "done" || t.Status == "error") {
				return t.Result, nil
			}
			time.Sleep(time.Second)
		}
		return "", fmt.Errorf("timeout")
	}
	result, err := llm.ExecuteTool(name, args, ctx)
	if err != nil {
		return "error: " + err.Error()
	}
	return result
}
