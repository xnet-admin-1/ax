# AX Web/Serve Mode — REST API Specification

## Overview

This spec defines the REST API endpoints for `ax serve` mode, providing full feature parity with the CLI's 11 enhancements. The web UI (defined in `docs/WEB_UI_SPEC.md`) uses WebSocket for real-time streaming; this API provides a request/response interface for programmatic access.

## Authentication

Scope-based API key authentication:

```bash
./ax --generate-key --scopes "chat,tools,agents,mcp"
```

Scopes: `chat`, `tools`, `agents`, `mcp`, `admin`

All endpoints require `Authorization: Bearer {API_KEY}`.

---

## Endpoints

### Chat

```
POST /api/v1/chat
{
  "prompt": "string (required)",
  "model": "string (optional)",
  "conversation_id": "string (optional)",
  "format": "json|yaml|markdown (default: json)",
  "timeout": "duration (default: 5m)",
  "trust": true,
  "tools": ["run_sh", "read_file"],
  "agents": ["coder", "qa"],
  "mcp_servers": ["filesystem"],
  "dry_run": false
}

Response:
{
  "response": "...",
  "conversation_id": "conv_xxxx",
  "tokens": {"prompt": 24, "completion": 56, "total": 80},
  "time_ms": 1245,
  "tools_used": ["run_sh"],
  "agents_used": ["coder"],
  "model": "bedrock-mantle/kimi-k2"
}
```

### Tool Execution

```
POST /api/v1/tools/execute
{
  "tool": "run_sh",
  "parameters": {"command": "ls -la"},
  "trust": true
}

Response:
{
  "result": "...",
  "tool_used": "run_sh",
  "time_ms": 45
}
```

Direct tool endpoints also available:
- `POST /api/v1/tools/run_sh`
- `POST /api/v1/tools/read_file`
- `POST /api/v1/tools/write_file`
- `POST /api/v1/tools/edit_file`
- `POST /api/v1/tools/list_dir`
- `POST /api/v1/tools/search_web`

### Agent Orchestration

```
POST /api/v1/agents/spawn
{
  "agents": ["coder", "qa", "security"],
  "task": "Review the authentication module",
  "trust": true
}

Response:
{
  "agent_results": {
    "coder": {"status": "completed", "response": "..."},
    "qa": {"status": "completed", "response": "..."},
    "security": {"status": "completed", "response": "..."}
  },
  "combined_output": "..."
}
```

### MCP Integration

```
POST /api/v1/mcp/:server
{
  "operation": "string",
  "params": {},
  "trust": true
}
```

Where `:server` = filesystem | github | brave | sqlite | memory | puppeteer | playwright

### Conversation Management

```
GET    /api/v1/conversations          # List conversations
GET    /api/v1/conversations/:id      # Get conversation messages
POST   /api/v1/conversations          # Create conversation
DELETE /api/v1/conversations/:id      # Delete conversation
POST   /api/v1/conversations/:id/export  # Export to markdown
```

### File Upload

```
POST /api/v1/files/upload
Content-Type: multipart/form-data

Response:
{
  "file_id": "file_xxxx",
  "filename": "input.txt",
  "size": 1024
}
```

### Batch Processing

```
POST /api/v1/batch
{
  "prompts": ["prompt1", "prompt2", ...],
  "model": "string",
  "trust": true,
  "timeout": "5m"
}

Response:
{
  "results": [
    {"prompt": "prompt1", "response": "...", "tokens": 80},
    {"prompt": "prompt2", "response": "...", "tokens": 65}
  ]
}
```

### Dry Run / Simulate

```
POST /api/v1/simulate
{
  "prompt": "string",
  "model": "string",
  "tools": ["run_sh"],
  "agents": ["coder"]
}

Response:
{
  "model": "bedrock-mantle/kimi-k2",
  "endpoint": "https://...",
  "estimated_tokens": 24,
  "tools_available": ["run_sh", "read_file", ...],
  "mcp_tools": ["create_issue", ...],
  "would_send": {...}
}
```

---

## Response Headers

All responses include:
- `X-Tokens-Used: 80`
- `X-Model: bedrock-mantle/kimi-k2`
- `X-Request-Time-Ms: 1245`
- `X-Conversation-Id: conv_xxxx`

---

## Content Negotiation

```
Accept: application/json     → JSON response (default)
Accept: application/yaml     → YAML response
Accept: text/markdown        → Markdown response
Accept: text/plain           → Plain text response
```

---

## Security

### Trust System
- Tools that modify the filesystem require `"trust": true`
- Read-only tools (read_file, list_dir, search_web) don't require trust
- Dangerous commands (rm -rf, git push --force) require `tools` scope

### Rate Limiting
- 100 requests/minute per API key (configurable)
- Streaming connections: 10 concurrent per key

---

## Implementation

### Server Structure
```go
type EnhancedServer struct {
    engine   *engine.Local
    mcpMgr   *mcp.Manager
    agentMgr *agent.Manager
    auth     *AuthManager
    config   ServerConfig
}

type ServerConfig struct {
    Address      string
    MaxTimeout   time.Duration
    TrustEnabled bool
    MCPEnabled   bool
}
```

### Middleware Chain
```
Request → Auth → RateLimit → Scope Check → Handler → Response Headers
```

---

## Configuration

```yaml
server:
  port: 8080
  bind: "0.0.0.0"
  max_timeout: 30m
  trust_required: true
  mcp_enabled: true

api_keys:
  - hash: "sha256:..."
    scopes: [chat, tools, agents, mcp]
    expires: "2025-12-31"
```
