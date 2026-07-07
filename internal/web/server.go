package web

import (
	"context"
	crand "crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sync"

	"github.com/coder/websocket"
	"github.com/xnet-admin-1/ax/internal/agent"
	"github.com/xnet-admin-1/ax/internal/engine"
	"github.com/xnet-admin-1/ax/internal/gateway"
	"github.com/xnet-admin-1/ax/internal/mcp"
)

type Server struct {
	DB       *sql.DB
	Gateway  *gateway.Router
	Hub      *Hub
	Handlers *Handlers
	WebFS    fs.FS
	Password string
	Port     int
	Bind     string
	Token    string // active session token
	McpMgr   *mcp.Manager

	// Handoff state
	handoffAgent string
	handoffSaved struct {
		prompt string
		tools  []string
		model  string
	}

	// Active streaming sessions (convID → cancel func)
	mu       sync.Mutex
	sessions map[string]context.CancelFunc
}

func NewServer(db *sql.DB, gw *gateway.Router, webFS fs.FS, bind string, port int, password string) *Server {
	hub := NewHub()
	agentMgr := agent.NewManager(db, gw)
	mcpMgr := mcp.NewManager(db)
	mcpMgr.ConnectEnabled()
	return &Server{
		DB:       db,
		Gateway:  gw,
		Hub:      hub,
		Handlers: &Handlers{DB: db, Gateway: gw, AgentMgr: agentMgr},
		WebFS:    webFS,
		Password: password,
		Port:     port,
		Bind:     bind,
		McpMgr:   mcpMgr,
		sessions: make(map[string]context.CancelFunc),
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Generate session token
	if s.Password != "" {
		b := make([]byte, 32)
		crand.Read(b)
		s.Token = fmt.Sprintf("%x", b)
	}

	// Login endpoint (no auth required)
	mux.HandleFunc("POST /api/login", s.handleLogin)

	// API routes (auth required)
	mux.HandleFunc("GET /api/conversations", s.requireAuth(s.Handlers.ListConversations))
	mux.HandleFunc("GET /api/conversations/{id}/messages", s.requireAuth(s.Handlers.GetMessages))
	mux.HandleFunc("DELETE /api/conversations/{id}", s.requireAuth(s.Handlers.DeleteConversation))
	mux.HandleFunc("PUT /api/conversations/{id}", s.requireAuth(s.Handlers.RenameConversation))
	mux.HandleFunc("GET /api/models", s.requireAuth(s.Handlers.ListModels))
	mux.HandleFunc("POST /api/model", s.requireAuth(s.Handlers.SetModel))
	mux.HandleFunc("GET /api/settings", s.requireAuth(s.Handlers.ListSettings))
	mux.HandleFunc("POST /api/settings", s.requireAuth(s.Handlers.UpdateSetting))
	mux.HandleFunc("DELETE /api/settings", s.requireAuth(s.Handlers.DeleteSetting))
	mux.HandleFunc("GET /api/providers", s.requireAuth(s.Handlers.ListProviders))
	mux.HandleFunc("POST /api/providers", s.requireAuth(s.Handlers.AddProvider))
	mux.HandleFunc("PUT /api/providers/{name}", s.requireAuth(s.Handlers.EditProvider))
	mux.HandleFunc("DELETE /api/providers/{name}", s.requireAuth(s.Handlers.DeleteProvider))
	mux.HandleFunc("POST /api/providers/{name}/toggle", s.requireAuth(s.Handlers.ToggleProvider))
	mux.HandleFunc("POST /api/providers/{name}/discover", s.requireAuth(s.Handlers.DiscoverModels))
	mux.HandleFunc("GET /api/tools", s.requireAuth(s.Handlers.ListTools))
	mux.HandleFunc("PUT /api/tools/{name}", s.requireAuth(s.Handlers.ToggleTool))
	mux.HandleFunc("GET /api/agents/tasks", s.requireAuth(s.Handlers.ListTasks))
	mux.HandleFunc("POST /api/agents/spawn", s.requireAuth(s.Handlers.SpawnAgent))
	mux.HandleFunc("POST /api/agents/cancel/{id}", s.requireAuth(s.Handlers.CancelTask))
	mux.HandleFunc("GET /api/agents/tasks/{id}/log", s.requireAuth(s.Handlers.GetTaskLog))
	mux.HandleFunc("GET /api/agents/roster", s.requireAuth(s.Handlers.GetRoster))
	mux.HandleFunc("POST /api/agents/roster", s.requireAuth(s.Handlers.SaveRosterItem))
	mux.HandleFunc("DELETE /api/agents/roster/{name}", s.requireAuth(s.Handlers.DeleteRosterItem))
	mux.HandleFunc("POST /api/agents/handoff", s.requireAuth(s.handleHandoff))
	mux.HandleFunc("POST /api/agents/return", s.requireAuth(s.handleReturn))
	mux.HandleFunc("GET /api/agents/handoff", s.requireAuth(s.getHandoff))

	// WebSocket
	mux.HandleFunc("/ws", s.handleWS)

	// Static files
	mux.Handle("/", http.FileServer(http.FS(s.WebFS)))

	addr := fmt.Sprintf("%s:%d", s.Bind, s.Port)
	log.Printf("ax serve: listening on http://%s", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.Password == "" {
		// No password set, return token directly
		json.NewEncoder(w).Encode(map[string]string{"token": "open"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", 400)
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Password), []byte(s.Password)) != 1 {
		http.Error(w, "unauthorized", 401)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"token": s.Token})
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Password == "" {
			next(w, r)
			return
		}
		token := r.Header.Get("Authorization")
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if token != s.Token {
			http.Error(w, "unauthorized", 401)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("ws: accept error: %v", err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient(s.Hub, conn, cancel)
	s.Hub.Register(client)

	go client.WritePump(ctx)
	client.ReadPump(ctx, s.handleMessage)
}

func (s *Server) handleMessage(client *Client, data []byte) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return
	}

	switch env.Type {
	case MsgChatSend:
		var msg ChatSendMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		go s.handleChatSend(client, msg)

	case MsgChatCancel:
		var msg ChatCancelMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		s.mu.Lock()
		if cancel, ok := s.sessions[msg.ConversationID]; ok {
			cancel()
			delete(s.sessions, msg.ConversationID)
		}
		s.mu.Unlock()

	case "agent.subscribe":
		var msg struct {
			Type   string `json:"type"`
			TaskID string `json:"taskId"`
		}
		if json.Unmarshal(data, &msg) != nil || msg.TaskID == "" {
			return
		}
		go s.streamTaskEvents(client, msg.TaskID)

	case "agent.unsubscribe":
		// Client navigated away — subscription goroutine will exit on its own
	}
}

func (s *Server) streamTaskEvents(client *Client, taskID string) {
	if s.Handlers.AgentMgr == nil { return }
	t := s.Handlers.AgentMgr.GetTask(taskID)
	if t == nil { return }
	// Send existing log as catch-up
	for _, ev := range t.GetLog() {
		client.Send(map[string]any{"type": "agent.event", "taskId": taskID, "event": map[string]string{"type": ev.Type, "text": ev.Text}})
	}
	if t.Status == "done" || t.Status == "error" {
		client.Send(map[string]any{"type": "agent.status", "taskId": taskID, "status": t.Status, "result": t.Result})
		return
	}
	// Stream live events
	for ev := range t.Events {
		client.Send(map[string]any{"type": "agent.event", "taskId": taskID, "event": map[string]string{"type": ev.Type, "text": ev.Text}})
		if ev.Type == "done" || ev.Type == "error" {
			client.Send(map[string]any{"type": "agent.status", "taskId": taskID, "status": t.Status, "result": t.Result})
			return
		}
	}
}

func (s *Server) handleHandoff(w http.ResponseWriter, r *http.Request) {
	var body struct{ Agent string `json:"agent"` }
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Agent == "" {
		http.Error(w, "invalid body", 400); return
	}
	roster := s.Handlers.AgentMgr.GetRoster()
	for _, a := range roster {
		if a.Name == body.Agent {
			s.handoffAgent = a.Name
			json.NewEncoder(w).Encode(map[string]string{"agent": a.Name, "status": "active"})
			return
		}
	}
	http.Error(w, "agent not found", 404)
}
func (s *Server) handleReturn(w http.ResponseWriter, r *http.Request) {
	s.handoffAgent = ""
	w.WriteHeader(200)
}
func (s *Server) getHandoff(w http.ResponseWriter, r *http.Request) {
	if s.handoffAgent != "" {
		json.NewEncoder(w).Encode(map[string]any{"active": true, "agent": s.handoffAgent})
	} else {
		json.NewEncoder(w).Encode(map[string]any{"active": false})
	}
}

func (s *Server) handleChatSend(client *Client, msg ChatSendMsg) {
	convID := msg.ConversationID

	// Create conversation if needed
	if convID == "" {
		convID = newID()
		title := msg.Content
		if len(title) > 50 {
			title = title[:50]
		}
		s.DB.Exec("INSERT INTO conversations(id, title, updated_at) VALUES(?,?,strftime('%s','now'))", convID, title)
		client.Send(ConvCreatedMsg{
			Type: MsgConvCreated,
			Conversation: ConvDTO{
				ID:    convID,
				Title: title,
			},
		})
	}

	// Create backend and start chat
	backend := engine.NewLocal(s.DB, s.Gateway)
	backend.AgentMgr = agent.NewManager(s.DB, s.Gateway)
	backend.McpMgr = s.McpMgr
	if msg.Mode != "" {
		backend.Mode = msg.Mode
	}

	// Send stream.start
	client.Send(StreamStartMsg{
		Type:           MsgStreamStart,
		ConversationID: convID,
	})

	// Start chat with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.sessions[convID] = cancel
	s.mu.Unlock()

	ch, err := backend.Chat(convID, msg.Content)
	if err != nil {
		client.Send(StreamErrorMsg{
			Type:           MsgStreamError,
			ConversationID: convID,
			Error:          err.Error(),
		})
		return
	}

	// Bridge events to WS (blocks until done)
	done := make(chan struct{})
	go func() {
		BridgeEvents(ch, client, convID)
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		// Cancelled
	}

	s.mu.Lock()
	delete(s.sessions, convID)
	s.mu.Unlock()
}

// newID generates a UUID
func newID() string {
	b := make([]byte, 16)
	crand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
