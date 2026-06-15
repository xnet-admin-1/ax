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
)

type Agent struct {
	Name         string   `json:"name"`
	SystemPrompt string   `json:"systemPrompt"`
	Model        string   `json:"model"`
	Tools        []string `json:"tools"`
}

type Task struct {
	ID        string
	Agent     string
	Status    string // running, done, error
	Result    string
	StartedAt time.Time
	cancel    context.CancelFunc
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

func (m *Manager) Spawn(agentName, task string) (string, error) {
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
	t := &Task{ID: id, Agent: agentName, Status: "running", StartedAt: time.Now(), cancel: cancel}
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
	body := map[string]any{"model": model, "messages": messages, "stream": true}
	jsonBody, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", apiBase+"/chat/completions", strings.NewReader(string(jsonBody)))
	if err != nil {
		m.finish(t, "", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		m.finish(t, "", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		m.finish(t, "", fmt.Errorf("API %d: %s", resp.StatusCode, b))
		return
	}

	var content strings.Builder
	buf := make([]byte, 8192)
	var remainder string
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
				var chunk struct {
					Choices []struct {
						Delta struct {
							Content string `json:"content"`
						} `json:"delta"`
					} `json:"choices"`
				}
				if json.Unmarshal([]byte(data), &chunk) != nil {
					continue
				}
				if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
					content.WriteString(chunk.Choices[0].Delta.Content)
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			m.finish(t, content.String(), readErr)
			return
		}
	}
done:
	m.finish(t, content.String(), nil)
}

func (m *Manager) finish(t *Task, result string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		t.Status = "error"
		t.Result = err.Error()
	} else {
		t.Status = "done"
		t.Result = result
	}
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
	Role    string `json:"role"`
	Content string `json:"content"`
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
