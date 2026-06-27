# ax

A terminal AI agent with full filesystem, shell, and network access. Single binary, multi-provider, multi-agent.

## Install

```bash
# Download and run — self-installs to /usr/local/bin/ax
./ax

# Or build from source
go build -o ax ./cmd/ax
sudo cp ax /usr/local/bin/ax
```

Requires Go 1.25+ (CGo for SQLite).

## Usage

```
ax                    # TUI mode
ax -p "prompt"        # One-shot CLI mode
ax -p "prompt" -m provider/model
ax -r                 # Resume last conversation
ax -a agent           # Start with agent handoff
ax -d                 # Enable debug logging
ax --models           # List available models
ax --trust-all        # Skip tool confirmations
```

## Tools

ax exposes these tools to the LLM:

| Tool | Description |
|------|-------------|
| `run_sh` | Execute shell commands (bash) |
| `read_file` | Read file contents |
| `write_file` | Write/create files |
| `edit_file` | SEARCH/REPLACE editing with flexible matching |
| `list_dir` | List directory contents |
| `search_web` | Web search via SearXNG |
| `orchestrate` | Multi-agent pipeline (DAG execution) |
| `save_memory` | Persist key-value pairs across sessions |
| `recall_memory` | Retrieve stored memories |
| `delete_memory` | Remove memories |

### edit_file

Structured file editing using SEARCH/REPLACE blocks. More precise than rewriting entire files.

Matching fallback chain:
1. Exact string match
2. Indent-flexible match (handles LLM indentation errors)
3. Trimmed whitespace match

### orchestrate

Runs a multi-agent pipeline with parallel and sequential stages:

```json
{
  "task": "Research and implement feature X",
  "stages": [
    {"name": "research", "agent": "researcher", "prompt": "Find best practices for X"},
    {"name": "design", "agent": "architect", "prompt": "Design the implementation"},
    {"name": "implement", "agent": "coder", "prompt": "Write the code", "depends_on": ["research", "design"]}
  ]
}
```

Stages without `depends_on` run in parallel. Dependent stages receive prior results as context.

## Agents

Built-in agent roster:

| Agent | Role |
|-------|------|
| `default` | General purpose |
| `architect` | System design, planning |
| `coder` | Implementation |
| `researcher` | Web search, synthesis |
| `qa` | Testing, validation |
| `security` | Security audit |
| `devops` | Infrastructure |
| `writer` | Documentation |

Custom agents can be created via `/spawn` > `b` (builder) with custom system prompts, model overrides, and tool allowlists.

## Providers

ax works with any OpenAI-compatible API. Configure via `/provider` panel or seed on first run.

Provider config is stored in SQLite. Each provider has:
- Name, API base URL, API key
- Model list (auto-discovered via `/models` endpoint)

Select models with `/model` or `-m provider/model`.

## TUI Commands

| Command | Action |
|---------|--------|
| `/model` | Select model |
| `/provider` | Manage providers |
| `/new` | New conversation |
| `/list` | Conversation history |
| `/spawn` | Spawn background agent |
| `/monitor` | View running agents |
| `/memory` | Manage persistent memories |
| `/tools` | Toggle tools |
| `/mcp` | MCP server connections |
| `/settings` | Configuration |
| `/theme` | Switch light/dark |
| `/debug` | Toggle debug logging |
| `/compact` | Summarize conversation |
| `/export` | Export to markdown |
| `/help` | Show all commands |

## Key Bindings

| Key | Action |
|-----|--------|
| `shift+up/down` | Scroll chat (line) |
| `ctrl+d/u` | Scroll chat (half page) |
| `up/down/left/right` | Navigate text input |
| `shift+left/right` | Prompt history |
| `ctrl+c` | Cancel/stop streaming |
| `ctrl+n` | New conversation |
| `ctrl+e` | Open $EDITOR for input |
| `ctrl+o` | Expand/collapse tool output |
| `ctrl+y` | Copy last response |
| `/` | Command autocomplete |
| `esc` | Close panel |

## Architecture

```
cmd/ax/
  main.go              CLI flags, entry point, self-install
  run.go               Mode dispatch (TUI/CLI/serve)

internal/
  engine/
    engine.go          Chat loop, streaming, tool execution
    local.go           TUI backend (conversations, messages, DB)
    orchestrate.go     Multi-agent DAG execution
  llm/
    llm.go            Tool definitions, ExecuteTool, dangerous cmd detection
  agent/
    agent.go          Agent roster, task manager, background execution
  gateway/
    gateway.go        Provider router (name/model → API base + key)
  edit/
    edit.go           SEARCH/REPLACE parser with flexible matching
  mcp/
    mcp.go           Model Context Protocol client
  knowledge/
    knowledge.go     Vector store for document indexing
  db/
    db.go            SQLite schema, migrations
    seed.go          Provider seed data
  debug/
    debug.go         Shared debug logger (info/warning/error/verbose)

tui/
  tui.go             Model struct, Init, Update router, View
  keyhandler.go      Key event handling
  events.go          LLM event handling, chat flow
  bubbles.go         Message bubble rendering
  layout.go          Layout composition, floating dialogs
  chat.go            Message rendering, tool output formatting
  input.go           Text input, autocomplete
  theme.go           Styles, help bar, spinners
  theme_detect.go    Light/dark theme, /theme panel
  debug.go           Debug panel
  commands.go        Slash command dispatch, panel views
  panels.go          Panel item types
  stubs.go           Backend wiring, agent delivery
```

## Reflection Loop

When a tool call fails (e.g., `edit_file` can't find the search block), the error is sent back to the LLM as the tool result. The LLM sees the error and retries with corrected input. Hard limit: 3 retries before stopping.

## Dangerous Command Detection

Shell commands are checked against destructive patterns before execution:
- `rm -rf` outside /tmp
- `dd`, `mkfs`, `fdisk`
- `chmod 777`, `chown`
- `kill -9`, `killall`
- `git push --force`, `git reset --hard`
- `DROP TABLE`, `DROP DATABASE`
- Writes to `/etc/`, `/usr/`, `/boot/`

Matched commands show a confirmation dialog. Bypass with `--trust-all`.

## MCP (Model Context Protocol)

Connect external tool servers via `/mcp`. Supports stdio and HTTP transports. MCP tools are injected into the LLM's tool list alongside built-in tools.

## Debug

- `/debug` panel: toggle on/off, cycle levels (info/warning/error/verbose)
- `-d` flag: enable verbose logging on startup
- Output: `/tmp/ax-debug.log`

Logs key events, tool execution, scroll operations, LLM calls, agent spawns.

## Data

All state stored in `~/.ax/ax.db` (SQLite):
- Conversations and messages
- Provider configuration
- Persistent memories
- Settings
- MCP server config

## Build

```bash
go build -o ax ./cmd/ax
```

Produces a single ~25MB static binary. No runtime dependencies.

## License

Proprietary. Copyright XNet.
