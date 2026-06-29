package web

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Hub manages all connected WebSocket clients
type Hub struct {
	mu      sync.Mutex
	clients map[*Client]bool
}

func NewHub() *Hub {
	return &Hub{clients: make(map[*Client]bool)}
}

func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

func (h *Hub) Broadcast(msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- data:
		default:
			// Client too slow, drop
		}
	}
}

// Client represents a single WebSocket connection
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	cancel context.CancelFunc
}

func NewClient(hub *Hub, conn *websocket.Conn, cancel context.CancelFunc) *Client {
	return &Client{
		hub:    hub,
		conn:   conn,
		send:   make(chan []byte, 256),
		cancel: cancel,
	}
}

func (c *Client) Send(msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default:
	}
}

// WritePump sends messages from the send channel to the WebSocket
func (c *Client) WritePump(ctx context.Context) {
	defer c.conn.CloseNow()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := c.conn.Write(ctx2, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// ReadPump reads messages from the WebSocket and dispatches them
func (c *Client) ReadPump(ctx context.Context, handler func(*Client, []byte)) {
	defer func() {
		c.hub.Unregister(c)
		c.conn.CloseNow()
	}()
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				log.Printf("ws: client disconnected normally")
			}
			return
		}
		handler(c, data)
	}
}
