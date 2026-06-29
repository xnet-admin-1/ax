package web

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/xnet-admin-1/ax/internal/gateway"
)

type Handlers struct {
	DB      *sql.DB
	Gateway *gateway.Router
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
