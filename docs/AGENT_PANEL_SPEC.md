# AX Web — Agent Panel Spec

## Overview

A collapsible right-side panel (300px) providing full agent management: roster CRUD, spawn, live monitoring, and handoff control. Parity with the TUI `/spawn`, `/monitor`, and agent builder panels.

---

## 1. Agent Detail View (Roster Editor)

### Data Model (existing `agent.Agent` struct)
```go
type Agent struct {
    Name          string   `json:"name"`
    SystemPrompt  string   `json:"systemPrompt"`
    Model         string   `json:"model"`          // provider/model override
    Tools         []string `json:"tools"`           // tool allowlist
    MaxTokens     int      `json:"maxTokens"`
    ContextTokens int      `json:"contextTokens"`
    Temperature   float64  `json:"temperature"`
    TopP          float64  `json:"topP"`
    TopK          int      `json:"topK"`
}
```

### UI: Roster Tab → Agent Detail

When user clicks an agent name or "Edit":
- Full-page form within the panel body
- Fields:
  - **Name** (readonly for builtins, editable for custom)
  - **Model** — dropdown of available models (empty = use default)
  - **System Prompt** — tall textarea (min 6 rows, resize:vertical)
  - **Temperature** — range slider 0.0–2.0, step 0.1, with numeric display
  - **Top P** — range slider 0.0–1.0, step 0.05
  - **Top K** — number input
  - **Max Context** — number input (default 32000)
  - **Tools** — checklist of all available tools (checked = allowed)
- Save button → `POST /api/agents/roster`
- Back button → return to roster list

### API
- `GET /api/agents/roster` — returns `[]Agent` with all fields
- `POST /api/agents/roster` — upsert (full Agent object)
- `DELETE /api/agents/roster/{name}` — remove

---

## 2. Live Task Log

### Data Flow

The `Task` struct already has:
```go
Log    []TaskEvent     // Append-only log of all events
Events chan TaskEvent  // Live channel (buffered, non-blocking emit)
```

`TaskEvent` types: `delta`, `tool_call`, `tool_result`, `progress`, `done`, `error`

### WebSocket Approach

Add new WS message types for streaming task events to the web:

```json
// Server → Client: task event
{"type": "agent.event", "taskId": "abc123", "event": {"type": "delta", "text": "..."}}

// Server → Client: task status change
{"type": "agent.status", "taskId": "abc123", "status": "done", "result": "..."}
```

### Implementation

When the agent panel monitor tab is open and a task is selected:
1. Client sends `{"type": "agent.subscribe", "taskId": "abc123"}`
2. Server starts a goroutine reading from `task.Events` channel and forwarding to the client
3. Client renders events in a scrollable log view:
   - `delta` → assistant text (appended, markdown rendered)
   - `tool_call` → tool chip (name + args)
   - `tool_result` → collapsible result text
   - `done` → final result displayed
   - `error` → red error message
4. Client sends `{"type": "agent.unsubscribe", "taskId": "abc123"}` on navigate away

### REST Fallback (polling)
- `GET /api/agents/tasks/{id}/log` — returns full `[]TaskEvent` for catch-up

### UI: Monitor Tab → Task Detail

- Header: agent name + status badge + elapsed time
- Scrollable log area showing conversation:
  - System prompt (collapsed)
  - User task (the spawn prompt)
  - Assistant responses (streaming via WS)
  - Tool calls as chips
  - Tool results (expandable)
- Bottom bar: Cancel button (if running), Back button

---

## 3. Agent Handoff

### Mechanism

Handoff temporarily replaces the main chat's:
- System prompt → agent's prompt
- Model → agent's model override
- Tool list → agent's tool allowlist

When user "/return"s, everything reverts.

### Web Implementation

**Handoff initiation:**
- In the agent roster, each agent gets a "Handoff" button
- Or a dropdown in the chat header: "Switch to: [agent list]"
- Clicking sends: `POST /api/agents/handoff` with `{"agent": "coder"}`

**Server side:**
- Sets `local.OverridePrompt`, `local.OverrideTools`, `local.Model`
- Returns the handoff state

**UI state:**
- Chat header shows: `AX · coder` (agent name instead of model)
- A "Return" button appears in the header
- Status bar/indicator shows active handoff

**Return:**
- `POST /api/agents/return` — clears overrides
- UI reverts to normal state

### API
- `POST /api/agents/handoff` — `{"agent": "name"}` → starts handoff
- `POST /api/agents/return` — ends handoff
- `GET /api/agents/handoff` — returns current handoff state (or null)

---

## 4. Report-To Toggle

### Purpose

When spawning an agent, results always report to the user (displayed in agent panel monitor). The old "report to chat agent" behavior (feeding results back to the main LLM) has been removed to prevent confusing double-responses.

### UI

No toggle needed — all spawn results display in the Monitor tab. The user can manually copy/paste results into chat if needed.

### Implementation

- `Task.ReportTo` is always `"user"`
- `deliverPendingReports()` only displays results, never triggers new LLM calls
- Spawn API does not accept a `reportTo` field

---

## 5. Task Elapsed Time

### Implementation

Each task card in the monitor shows live elapsed time:
```
researcher · running · 0:45
```

### Frontend

- On mount/render, calculate `Date.now() - task.startedAt`
- Update every second via `setInterval` when monitor tab is active
- Display as `m:ss` or `h:mm:ss`

### API

- `GET /api/agents/tasks` response includes `startedAt` as Unix timestamp (ms)
- Client computes elapsed locally (no server polling needed for time)

---

## 6. Orchestrate Visualization

### Current State

The orchestrate tool emits progress events:
```
[stage_name] started (agent_name)
[stage_name] completed
Orchestrating N stages...
```

These arrive as `stream.tool_result` progress updates in the bridge.

### Enhanced Visualization

Display a stage graph in the chat (inline) when orchestrate is running:

```
┌─ research ──── ● running (0:12)
├─ design ────── ● running (0:08)
└─ implement ─── ○ waiting (depends: research, design)
```

### Implementation

**Parse orchestrate progress events:**
- When `stream.tool_call` for `orchestrate` arrives, extract the stages from args
- Create a visual stage tracker in the chat
- As `stream.tool_result` progress events arrive (e.g., `[research] completed`), update stage status

**Frontend component:**
- Inline div inserted after the orchestrate tool chip
- Each stage shows: name, agent, status (waiting/running/done/error), elapsed
- Parallel stages shown side-by-side or as a tree
- Updates live as progress events arrive

**Data structure in JS:**
```javascript
var orchestrateState = {
  stages: [
    {name: "research", agent: "researcher", status: "running", startedAt: timestamp},
    {name: "design", agent: "architect", status: "running", startedAt: timestamp},
    {name: "implement", agent: "coder", status: "waiting", dependsOn: ["research","design"]}
  ]
};
```

---

## 7. Agent Prompt Editing

### Current State (web)

Uses `prompt()` dialog — single line, no formatting, terrible UX.

### Target

Full inline editor within the roster detail view:
- Tall textarea (min-height: 200px, resize:vertical)
- Monospace font for prompt text
- Character count display
- Placeholder text showing example prompt structure
- Auto-save on blur (debounced 500ms)

### Template guidance

Show a hint above the textarea:
```
Structure: Identity → Capabilities → Rules → Response Style
```

### Implementation

Already handled by the Agent Detail View (section 1) — the textarea field for `systemPrompt` with:
- `min-height: 200px`
- `font-family: monospace`
- `resize: vertical`
- `onchange` → auto-save via `POST /api/agents/roster`

---

## WebSocket Protocol Additions

| Type | Direction | Payload |
|------|-----------|---------|
| `agent.subscribe` | Client → Server | `{taskId}` |
| `agent.unsubscribe` | Client → Server | `{taskId}` |
| `agent.event` | Server → Client | `{taskId, event: {type, text}}` |
| `agent.status` | Server → Client | `{taskId, status, result}` |

---

## REST API Summary

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/agents/roster` | List all agents with full config |
| POST | `/api/agents/roster` | Create/update agent |
| DELETE | `/api/agents/roster/{name}` | Delete agent |
| GET | `/api/agents/tasks` | List all tasks (with startedAt) |
| GET | `/api/agents/tasks/{id}/log` | Get full task log |
| POST | `/api/agents/spawn` | Spawn agent `{agent, task, reportTo}` |
| POST | `/api/agents/cancel/{id}` | Cancel running task |
| POST | `/api/agents/handoff` | Start handoff `{agent}` |
| POST | `/api/agents/return` | End handoff |
| GET | `/api/agents/handoff` | Get current handoff state |

---

## Implementation Order

1. **Agent Detail View** — roster editor with all fields (extends existing)
2. **Agent Prompt Editing** — already part of #1
3. **Report-To Toggle** — add radio to spawn form + pass in API
4. **Task Elapsed Time** — add `startedAt` to API, JS timer
5. **Live Task Log** — WS subscription + log rendering
6. **Agent Handoff** — API endpoints + UI state
7. **Orchestrate Visualization** — parse progress events + inline stage display

Estimated effort: 1 day for #1-4, 1 day for #5-7.
