package web

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/xnet-admin-1/ax/internal/agent"
	"github.com/xnet-admin-1/ax/internal/gateway"
)

type Handlers struct {
	DB      *sql.DB
	Gateway *gateway.Router
	AgentMgr *agent.Manager
}

// ListConversations returns all conversations
func (h *Handlers) ListConversations(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.Query("SELECT id, title, updated_at FROM conversations ORDER BY updated_at DESC LIMIT 50")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	var convs []ConvDTO
	for rows.Next() {
		var c ConvDTO
		rows.Scan(&c.ID, &c.Title, &c.UpdatedAt)
		c.CreatedAt = c.UpdatedAt
		convs = append(convs, c)
	}
	if convs == nil {
		convs = []ConvDTO{}
	}
	json.NewEncoder(w).Encode(convs)
}

// GetMessages returns messages for a conversation
func (h *Handlers) GetMessages(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	if convID == "" {
		http.Error(w, "missing id", 400)
		return
	}

	rows, err := h.DB.Query("SELECT id, role, content, created_at FROM messages WHERE conv_id=? ORDER BY created_at", convID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	var msgs []MsgDTO
	for rows.Next() {
		var m MsgDTO
		rows.Scan(&m.ID, &m.Role, &m.Content, &m.Timestamp)
		m.ConversationID = convID
		msgs = append(msgs, m)
	}
	if msgs == nil {
		msgs = []MsgDTO{}
	}
	json.NewEncoder(w).Encode(msgs)
}

// ListModels returns available models
func (h *Handlers) ListModels(w http.ResponseWriter, r *http.Request) {
	models := h.Gateway.ListModels()
	if models == nil {
		models = []string{}
	}
	json.NewEncoder(w).Encode(models)
}

// SetModel sets the active model
func (h *Handlers) SetModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", 400)
		return
	}
	h.DB.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES('selected_model',?)", body.Model)
	w.WriteHeader(200)
}

// TruncateMessages deletes messages after a given index (for edit/resend)
func (h *Handlers) TruncateMessages(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	if convID == "" {
		http.Error(w, "missing id", 400)
		return
	}
	var body struct {
		After int `json:"after"` // keep this many messages, delete the rest
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.After < 0 {
		http.Error(w, "invalid body", 400)
		return
	}
	// Get the created_at of the Nth message to use as cutoff
	var cutoff int64
	err := h.DB.QueryRow("SELECT created_at FROM messages WHERE conv_id=? ORDER BY created_at LIMIT 1 OFFSET ?", convID, body.After-1).Scan(&cutoff)
	if err != nil {
		// If offset is beyond message count, nothing to delete
		w.WriteHeader(200)
		return
	}
	h.DB.Exec("DELETE FROM messages WHERE conv_id=? AND created_at > ?", convID, cutoff)
	w.WriteHeader(200)
}

// DeleteConversation deletes a conversation and its messages
func (h *Handlers) DeleteConversation(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	if convID == "" {
		http.Error(w, "missing id", 400)
		return
	}
	h.DB.Exec("DELETE FROM messages WHERE conv_id=?", convID)
	h.DB.Exec("DELETE FROM conversations WHERE id=?", convID)
	w.WriteHeader(200)
}

// RenameConversation updates a conversation title
func (h *Handlers) RenameConversation(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	if convID == "" {
		http.Error(w, "missing id", 400)
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", 400)
		return
	}
	h.DB.Exec("UPDATE conversations SET title=? WHERE id=?", body.Title, convID)
	w.WriteHeader(200)
}

// ListSettings returns all settings
func (h *Handlers) ListSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.Query("SELECT key, value FROM settings ORDER BY key")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	settings := make(map[string]string)
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		settings[k] = v
	}
	json.NewEncoder(w).Encode(settings)
}

// UpdateSetting sets a single setting
func (h *Handlers) UpdateSetting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		http.Error(w, "invalid body", 400)
		return
	}
	h.DB.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES(?,?)", body.Key, body.Value)
	w.WriteHeader(200)
}

// DeleteSetting removes a setting
func (h *Handlers) DeleteSetting(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "missing key", 400)
		return
	}
	h.DB.Exec("DELETE FROM settings WHERE key=?", key)
	w.WriteHeader(200)
}

// ListProviders returns all providers
func (h *Handlers) ListProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.Query("SELECT name, api_key, api_base, enabled, models FROM providers ORDER BY name")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	type provDTO struct {
		Name    string   `json:"name"`
		APIBase string   `json:"apiBase"`
		HasKey  bool     `json:"hasKey"`
		Enabled bool     `json:"enabled"`
		Models  []string `json:"models"`
	}
	var out []provDTO
	for rows.Next() {
		var name, key, base, models string
		var enabled int
		rows.Scan(&name, &key, &base, &enabled, &models)
		p := provDTO{Name: name, APIBase: base, HasKey: key != "", Enabled: enabled != 0}
		json.Unmarshal([]byte(models), &p.Models)
		out = append(out, p)
	}
	if out == nil {
		out = []provDTO{}
	}
	json.NewEncoder(w).Encode(out)
}

// AddProvider creates or updates a provider
func (h *Handlers) AddProvider(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		APIBase string `json:"apiBase"`
		APIKey  string `json:"apiKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "invalid body", 400)
		return
	}
	h.DB.Exec("INSERT OR REPLACE INTO providers(name, api_key, api_base, enabled, models) VALUES(?,?,?,1,COALESCE((SELECT models FROM providers WHERE name=?), '[]'))",
		body.Name, body.APIKey, body.APIBase, body.Name)
	w.WriteHeader(200)
}

// EditProvider updates a provider's key and base
func (h *Handlers) EditProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		APIBase string `json:"apiBase"`
		APIKey  string `json:"apiKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", 400)
		return
	}
	h.DB.Exec("UPDATE providers SET api_base=?, api_key=? WHERE name=?", body.APIBase, body.APIKey, name)
	w.WriteHeader(200)
}

// DeleteProvider removes a provider
func (h *Handlers) DeleteProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	h.DB.Exec("DELETE FROM providers WHERE name=?", name)
	w.WriteHeader(200)
}

// ToggleProvider enables/disables a provider
func (h *Handlers) ToggleProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	h.DB.Exec("UPDATE providers SET enabled = CASE WHEN enabled=1 THEN 0 ELSE 1 END WHERE name=?", name)
	w.WriteHeader(200)
}

// DiscoverModels fetches models from a provider's /models endpoint
func (h *Handlers) DiscoverModels(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	models, err := h.Gateway.DiscoverModels(name)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Save to DB
	modelsJSON, _ := json.Marshal(models)
	h.DB.Exec("UPDATE providers SET models=? WHERE name=?", string(modelsJSON), name)
	json.NewEncoder(w).Encode(models)
}

// ListTools returns all available tools with their trust status
func (h *Handlers) ListTools(w http.ResponseWriter, r *http.Request) {
	type toolDTO struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Source      string `json:"source"`
		Trusted     bool   `json:"trusted"`
	}

	// Get builtin tools from gateway's toolDefs
	builtinTools := []struct{ name, desc string }{
		{"run_sh", "Execute shell commands"},
		{"read_file", "Read file contents"},
		{"write_file", "Write/create files"},
		{"edit_file", "SEARCH/REPLACE editing"},
		{"list_dir", "List directory contents"},
		{"search_web", "Web search via SearXNG"},
		{"orchestrate", "Multi-agent pipeline"},
		{"save_memory", "Persist key-value memory"},
		{"recall_memory", "Retrieve stored memory"},
		{"delete_memory", "Remove memory"},
	}

	var tools []toolDTO
	for _, t := range builtinTools {
		trusted := true
		var val string
		if h.DB.QueryRow("SELECT value FROM settings WHERE key=?", "tool_trust_"+t.name).Scan(&val) == nil {
			trusted = val != "0"
		}
		tools = append(tools, toolDTO{Name: t.name, Description: t.desc, Source: "builtin", Trusted: trusted})
	}
	json.NewEncoder(w).Encode(tools)
}

// ToggleTool toggles trust for a tool
func (h *Handlers) ToggleTool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Trusted bool `json:"trusted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", 400)
		return
	}
	val := "0"
	if body.Trusted {
		val = "1"
	}
	h.DB.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES(?,?)", "tool_trust_"+name, val)
	w.WriteHeader(200)
}

// ListTasks returns running/completed agent tasks
func (h *Handlers) ListTasks(w http.ResponseWriter, r *http.Request) {
	if h.AgentMgr == nil {
		json.NewEncoder(w).Encode([]any{})
		return
	}
	type taskDTO struct {
		ID        string `json:"id"`
		Agent     string `json:"agent"`
		Status    string `json:"status"`
		Desc      string `json:"desc"`
		Result    string `json:"result"`
		StartedAt int64  `json:"startedAt"`
	}
	tasks := h.AgentMgr.ListTasks()
	var out []taskDTO
	for _, t := range tasks {
		desc := t.ID[:8]
		if len(t.Log) > 0 {
			desc = t.Log[0].Text
			if len(desc) > 60 { desc = desc[:60] }
		}
		result := t.Result
		out = append(out, taskDTO{
			ID:        t.ID,
			Agent:     t.Agent,
			Status:    t.Status,
			Desc:      desc,
			Result:    result,
			StartedAt: t.StartedAt.UnixMilli(),
		})
	}
	if out == nil { out = []taskDTO{} }
	json.NewEncoder(w).Encode(out)
}

// SpawnAgent spawns a background agent task
func (h *Handlers) SpawnAgent(w http.ResponseWriter, r *http.Request) {
	if h.AgentMgr == nil {
		http.Error(w, "no agent manager", 500)
		return
	}
	var body struct {
		Agent    string `json:"agent"`
		Task     string `json:"task"`
		ReportTo string `json:"reportTo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Task == "" {
		http.Error(w, "invalid body", 400)
		return
	}
	if body.Agent == "" { body.Agent = "default" }
	if body.ReportTo == "" { body.ReportTo = "user" }
	id, err := h.AgentMgr.Spawn(body.Agent, body.Task, body.ReportTo)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"id": id})
}

// CancelTask cancels a running agent task
func (h *Handlers) CancelTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if h.AgentMgr != nil {
		h.AgentMgr.Cancel(id)
	}
	w.WriteHeader(200)
}

// GetRoster returns the agent roster
func (h *Handlers) GetRoster(w http.ResponseWriter, r *http.Request) {
	if h.AgentMgr == nil {
		json.NewEncoder(w).Encode([]any{})
		return
	}
	roster := h.AgentMgr.GetRoster()
	json.NewEncoder(w).Encode(roster)
}

// SaveRosterItem adds or updates an agent in the roster
func (h *Handlers) SaveRosterItem(w http.ResponseWriter, r *http.Request) {
	if h.AgentMgr == nil {
		http.Error(w, "no agent manager", 500)
		return
	}
	var ag agent.Agent
	if err := json.NewDecoder(r.Body).Decode(&ag); err != nil || ag.Name == "" {
		http.Error(w, "invalid body", 400)
		return
	}
	roster := h.AgentMgr.GetRoster()
	found := false
	for i, a := range roster {
		if a.Name == ag.Name {
			roster[i] = ag
			found = true
			break
		}
	}
	if !found {
		roster = append(roster, ag)
	}
	h.AgentMgr.SaveRoster(roster)
	w.WriteHeader(200)
}

// DeleteRosterItem removes an agent from the roster
func (h *Handlers) DeleteRosterItem(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if h.AgentMgr == nil {
		w.WriteHeader(200)
		return
	}
	roster := h.AgentMgr.GetRoster()
	var filtered []agent.Agent
	for _, a := range roster {
		if a.Name != name {
			filtered = append(filtered, a)
		}
	}
	h.AgentMgr.SaveRoster(filtered)
	w.WriteHeader(200)
}

// GetTaskLog returns the full event log for a task
func (h *Handlers) GetTaskLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if h.AgentMgr == nil {
		json.NewEncoder(w).Encode([]any{})
		return
	}
	t := h.AgentMgr.GetTask(id)
	if t == nil {
		http.Error(w, "task not found", 404)
		return
	}
	json.NewEncoder(w).Encode(t.GetLog())
}

// StartHandoff switches the active chat to use an agent's prompt/model/tools
func (h *Handlers) StartHandoff(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent string `json:"agent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Agent == "" {
		http.Error(w, "invalid body", 400)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"agent": body.Agent, "status": "active"})
}

// EndHandoff returns to the default agent
func (h *Handlers) EndHandoff(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
}

// GetHandoff returns current handoff state
func (h *Handlers) GetHandoff(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]any{"active": false})
}
