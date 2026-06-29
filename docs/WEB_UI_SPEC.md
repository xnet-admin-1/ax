# AX Web UI — Build Spec

## Overview

Add a `ax serve` mode that starts an HTTP server with a single-page web interface. The web client connects via WebSocket for real-time streaming. The backend reuses ax's existing engine (same tools, same LLM routing, same DB) — the web UI is just a new transport layer.

## Architecture

```
┌─────────────────────────────────┐
│  Web SPA (index.html)           │  ← embed.FS, served at /
├─────────────────────────────────┤
│  HTTP + WebSocket Server        │  ← internal/web/server.go
├─────────────────────────────────┤
│  ax Engine (existing)           │  ← internal/engine/local.go
│  • Gateway (provider routing)   │
│  • LLM (tool execution)        │
│  • Agent (spawn/orchestrate)    │
│  • DB (conversations, messages) │
└─────────────────────────────────┘
```

### Key Principle

The web server is a **thin WebSocket bridge** over the existing `engine.Local` backend. No duplication of chat logic, tool execution, or provider routing. The same code path that powers the TUI handles web requests.

---

## Package Layout

```
cmd/ax/
  serve.go              ← New file: serve command, flag parsing, server start
internal/web/
  server.go             ← HTTP router, WebSocket upgrader, auth middleware
  hub.go                ← WebSocket connection hub (broadcast)
  client.go             ← Per-connection reader/writer goroutines
  protocol.go           ← Message type constants and structs
  handlers.go           ← REST API handlers (conversations, models, settings)
  bridge.go             ← Maps engine.Event ↔ WS protocol messages
  embed.go              ← //go:embed for web assets
web/
  index.html            ← Single-file SPA (HTML + CSS + JS inlined)
  login.html            ← Auth page
  favicon.svg           ← AX logo
```

---

## WebSocket Protocol

### Connection

```
ws://host:port/ws?token=<session_token>
```

### Client → Server

| Type | Payload | Description |
|------|---------|-------------|
| `chat.send` | `{conversationId, content, mode, images[]}` | Send message, start streaming |
| `chat.cancel` | `{conversationId}` | Cancel generation |
| `chat.regenerate` | `{conversationId}` | Regenerate last response |

### Server → Client

| Type | Payload | Description |
|------|---------|-------------|
| `conversation.created` | `{id, title, createdAt}` | New conversation |
| `conversation.updated` | `{id, title}` | Title changed (auto-title) |
| `message.created` | `{id, conversationId, role, content, timestamp}` | Message persisted |
| `stream.start` | `{conversationId, messageId}` | Streaming begins |
| `stream.delta` | `{conversationId, delta}` | Text chunk |
| `stream.reasoning` | `{conversationId, delta}` | Thinking content |
| `stream.tool_call` | `{conversationId, toolCall: {id, name, arguments}}` | Tool invoked |
| `stream.tool_result` | `{conversationId, toolCallId, result, isError}` | Tool completed |
| `stream.end` | `{conversationId, usage: {totalTokens}}` | Done |
| `stream.error` | `{conversationId, error}` | Error |

---

## REST API

All prefixed with `/api/`. Requires `Authorization: Bearer <token>` header (from login).

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/login` | `{password}` → `{token}` |
| GET | `/api/conversations` | List all conversations |
| POST | `/api/conversations` | Create new conversation |
| DELETE | `/api/conversations/:id` | Delete conversation |
| GET | `/api/conversations/:id/messages` | Get messages for conversation |
| GET | `/api/models` | List available models |
| POST | `/api/model` | `{model}` — set active model |
| GET | `/api/settings` | Get settings |
| POST | `/api/settings` | Update settings |

---

## Authentication

- Password set via `ax serve --password <pw>` or stored in DB (`settings_kv key='web_password'`)
- Login returns a JWT-like session token (random 32-byte hex, stored in memory)
- Token passed as query param on WS connect and Bearer header on REST
- No auth if no password is set (local-only mode)

---

## Bridge Layer (internal/web/bridge.go)

Maps between ax's `engine.Event` channel and WebSocket protocol:

```go
func bridgeEvents(ch <-chan engine.Event, client *Client, convID string) {
    for ev := range ch {
        switch ev.Type {
        case "delta":
            client.Send(StreamDelta{ConversationId: convID, Delta: ev.Delta})
        case "tool_call":
            client.Send(StreamToolCall{ConversationId: convID, ToolCall: ...})
        case "tool_result":
            client.Send(StreamToolResult{ConversationId: convID, ...})
        case "end":
            client.Send(StreamEnd{ConversationId: convID, Usage: ...})
        case "error":
            client.Send(StreamError{ConversationId: convID, Error: ev.Error})
        }
    }
}
```

---

## Web Frontend (web/index.html)

Single HTML file with inlined CSS and JS. No build step. CDN deps:
- `marked.js` — markdown rendering
- `highlight.js` — syntax highlighting (subset: go, python, javascript, bash, json, yaml)

### Layout

```
┌──────────────────────────────────────────────────┐
│ Sidebar (260px)        │  Main                    │
│ ┌────────────────────┐ │ ┌──────────────────────┐│
│ │ [+ New] [AX]       │ │ │ Title    [Model ▾]   ││
│ │                    │ │ ├──────────────────────┤│
│ │ Conversation 1     │ │ │                      ││
│ │ Conversation 2 *   │ │ │ Messages (scroll)    ││
│ │ Conversation 3     │ │ │                      ││
│ │                    │ │ ├──────────────────────┤│
│ │                    │ │ │ [CHAT|PLAN|BUILD]    ││
│ │ [Settings]         │ │ │ [textarea      ] [▶] ││
│ └────────────────────┘ │ └──────────────────────┘│
└──────────────────────────────────────────────────┘
```

### Features

1. **Conversation sidebar** — list, create, delete, auto-title updates
2. **Model selector** — dropdown, reads from `/api/models`
3. **Mode pills** — CHAT / PLAN / BUILD
4. **Streaming messages** — delta appended in real-time, markdown rendered
5. **Tool call display** — collapsible chips showing name + status + result
6. **Reasoning/thinking** — collapsible `<details>` block, shimmer while active
7. **Code blocks** — syntax highlighted, copy button
8. **Send/Stop button** — toggles during streaming
9. **Keyboard shortcuts** — Enter send, Shift+Enter newline, Escape cancel
10. **Settings modal** — model, provider, password
11. **Dark/Light theme** — toggle, persisted in localStorage
12. **Mobile responsive** — sidebar collapses at 768px

### Message Rendering

```javascript
function renderMessage(msg) {
  if (msg.role === 'user') {
    return `<div class="msg msg-user">${escapeHtml(msg.content)}</div>`;
  }
  // Assistant: render markdown
  return `<div class="msg msg-assistant">${marked.parse(msg.content)}</div>`;
}
```

Tool calls render as inline chips between message content:
```html
<div class="tool-chip done" onclick="toggleDetail(this)">
  <span class="icon">✓</span> run_sh
  <div class="tool-detail">$ ls -la\ntotal 42\n...</div>
</div>
```

---

## Implementation Plan

### Phase 1: Minimal viable serve (1 day)

1. Create `cmd/ax/serve.go` — parse flags, open DB, start HTTP server
2. Create `internal/web/server.go` — static file serving, WS upgrade
3. Create `internal/web/hub.go` + `client.go` — basic WS connection management
4. Create `internal/web/bridge.go` — event channel → WS messages
5. Create `internal/web/handlers.go` — conversation list, messages, models
6. Create `web/index.html` — minimal chat UI (send, stream, display)
7. Wire `engine.Local.Chat()` through WS

### Phase 2: Full feature parity (1 day)

8. Add authentication (password + token)
9. Add model switching
10. Add conversation management (create, delete, rename)
11. Add mode pills (CHAT/PLAN/BUILD)
12. Add tool call rendering (chips, expandable)
13. Add reasoning block (collapsible)
14. Add settings modal
15. Add theme toggle

### Phase 3: Polish (half day)

16. Mobile responsive layout
17. Keyboard shortcuts
18. Auto-reconnect on WS drop
19. Copy button on code blocks
20. Image support (paste/drop)

---

## Flags

```
ax serve                    # Start web server on :8080
ax serve -P 3000            # Custom port
ax serve -b 0.0.0.0         # Bind address
ax serve --password secret  # Set access password
ax serve -d                 # Debug logging
```

---

## Security

- Password auth with constant-time comparison
- Session tokens expire after 24h
- CORS disabled (same-origin only)
- CSP headers for embedded content
- No external requests from server (all API calls are client→server)
- WebSocket validates token on upgrade

---

## Dependencies

New:
- `github.com/coder/websocket` — WebSocket library (no CGo, production-grade)

Existing (reused):
- `database/sql` + go-sqlite3
- `embed` for web assets
- All existing internal packages

---

## File Size Budget

- `web/index.html` — target < 100KB (inlined CSS+JS)
- `web/login.html` — < 3KB
- CDN deps loaded at runtime (marked.js ~40KB, highlight.js ~30KB)
- Total binary size increase: ~100KB (embedded HTML)
