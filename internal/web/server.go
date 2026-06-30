package web

import (
	"context"
	crand "crypto/rand"
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

	// Active streaming sessions (convID → cancel func)
	mu       sync.Mutex
	sessions map[string]context.CancelFunc
}

func NewServer(db *sql.DB, gw *gateway.Router, webFS fs.FS, bind string, port int, password string) *Server {
	hub := NewHub()
	return &Server{
		DB:       db,
		Gateway:  gw,
		Hub:      hub,
		Handlers: &Handlers{DB: db, Gateway: gw},
		WebFS:    webFS,
		Password: password,
		Port:     port,
		Bind:     bind,
		sessions: make(map[string]context.CancelFunc),
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("GET /api/conversations", s.Handlers.ListConversations)
	mux.HandleFunc("GET /api/conversations/{id}/messages", s.Handlers.GetMessages)
	mux.HandleFunc("DELETE /api/conversations/{id}", s.Handlers.DeleteConversation)
	mux.HandleFunc("GET /api/models", s.Handlers.ListModels)
	mux.HandleFunc("POST /api/model", s.Handlers.SetModel)

	// WebSocket
	mux.HandleFunc("/ws", s.handleWS)

	// Static files
	mux.Handle("/", http.FileServer(http.FS(s.WebFS)))

	addr := fmt.Sprintf("%s:%d", s.Bind, s.Port)
	log.Printf("ax serve: listening on http://%s", addr)
	return http.ListenAndServe(addr, mux)
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
