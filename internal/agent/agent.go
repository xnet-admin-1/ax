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

func (m *Manager) GetRoster() []Agent {
	var raw string
	err := m.DB.QueryRow("SELECT value FROM settings_kv WHERE key='agent_roster'").Scan(&raw)
	if err != nil || raw == "" {
		return nil
	}
	var agents []Agent
	json.Unmarshal([]byte(raw), &agents)
	return agents
}

func (m *Manager) SaveRoster(agents []Agent) error {
	data, err := json.Marshal(agents)
	if err != nil {
		return err
	}
	_, err = m.DB.Exec("INSERT INTO settings_kv(key,value) VALUES('agent_roster',?) ON CONFLICT(key) DO UPDATE SET value=?", string(data), string(data))
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
		return "", fmt.Errorf("agent %q not found in roster", agentName)
	}

	model := ag.Model
	if model == "" {
		var sel string
		m.DB.QueryRow("SELECT value FROM settings_kv WHERE key='selected_model'").Scan(&sel)
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
