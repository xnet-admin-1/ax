# AX vs Kiro CLI — Feature Comparison

## Slash Commands

| Kiro Command | AX Equivalent | Status |
|---|---|---|
| `/help` | `/help` (in commands.go) | Implemented |
| `/model [name]` | `/model [name]` | Implemented |
| `/agent [name]` | `/agent` | Implemented (roster panel) |
| `/chat [save\|load\|new]` | `/list`, `/new`, `/fork` | Implemented (tree panel) |
| `/clear` | `/clear` | Implemented |
| `/code [init\|status\|overview]` | — | Not implemented (no LSP) |
| `/compact` | `/compact` | Implemented (LLM-based) |
| `/context [add\|remove]` | `/knowledge add\|rm` | Implemented |
| `/feedback` | — | Not needed |
| `/guide` | — | Not implemented |
| `/hooks` | — | Not implemented |
| `/knowledge [show\|add\|rm]` | `/knowledge [add\|search\|rm\|list]` | Implemented |
| `/mcp` | `/mcp` | Implemented |
| `/paste` | — | Not implemented (no clipboard in terminal) |
| `/plan` | `/mode plan` (mode switch) | Implemented |
| `/prompts` | — | Not implemented |
| `/quit` / `/exit` | `/exit` | Implemented |
| `/reply` | — | Not implemented |
| `/settings` | `/settings`, `/config` | Implemented |
| `/spawn <task>` | `/spawn` (panel) | Implemented |
| `/theme` | — | Not implemented (single theme) |
| `/tools [trust\|untrust]` | `/tools` | Implemented (toggles) |
| `/transcript` | `/export` | Implemented (markdown) |
| `/usage` | `/usage` | Implemented |

## Tools

| Kiro Tool | AX Tool | Status |
|---|---|---|
| shell (run commands) | `run_sh` | Implemented |
| read (files) | `read_file` | Implemented |
| write (files) | `write_file` | Implemented |
| list (directory) | `list_dir` | Implemented |
| grep (search files) | via `run_sh` | Works via shell |
| glob (find files) | via `run_sh` | Works via shell |
| web_search | `search_web` | Implemented |
| web_fetch | via `run_sh` (curl) | Works via shell |
| knowledge | `/knowledge` commands | Implemented |
| code (LSP) | — | Not implemented |
| subagent (pipeline) | `orchestrate` | Implemented (DAG pipeline) |
| use_aws | via `run_sh` (aws cli) | Works via shell |
| introspect | — | Not applicable |
| goal | — | Not implemented |

## Agent Orchestration

| Kiro Feature | AX Feature | Status |
|---|---|---|
| subagent pipeline (DAG) | `orchestrate` tool | Implemented |
| Parallel execution | Stages without deps run parallel | Implemented |
| Dependency chaining | `depends_on` field | Implemented |
| Sub-agent tool access | Full tool loop (20 turns) | Implemented |
| Session management | spawn_agent + get_agent_result | Implemented |
| Agent roles | Default roster (7 agents) | Implemented |
| Turn limit | 20 turns per sub-agent | Implemented |
| Summary/report back | Results auto-returned | Implemented |
| Trust settings per agent | TrustAll on sub-agents | Implemented (always trusted) |
| Monitor (ctrl+g) | `/monitor` panel | Implemented |

## TUI Features

| Kiro Feature | AX Feature | Status |
|---|---|---|
| Alt-screen mode | Alt-screen | Implemented |
| Mouse scroll | Mouse cell motion | Implemented |
| Streaming responses | SSE parsing + live render | Implemented |
| Reasoning/thoughts | Separate display with --- | Implemented |
| Tool progress | Activity line | Implemented |
| Tool confirmation | y/n prompt | Implemented |
| Ctrl+C cancel | Cancel stream / double=exit | Implemented |
| Editor mode | Ctrl+E (micro/nano) | Implemented |
| Input history | Up/down arrows | Implemented |
| Autocomplete | / command autocomplete | Implemented |
| Themes | Single dark theme | Partial |
| Keybindings | Hardcoded | Not configurable |
| Clipboard paste | — | Not implemented |

## CLI Flags

| Kiro Flag | AX Flag | Status |
|---|---|---|
| (default) | `ax` (TUI) | Implemented |
| `-p` / prompt | `ax -p "..."` | Implemented |
| `--model` | `-m model` | Implemented |
| `--resume` | `-r` | Implemented |
| `--trust-all-tools` | `--trust-all` | Implemented |
| `--agent` | `-a agent` | Implemented |
| `--list-models` | `--models` | Implemented |
| `--version` | `-v` / `--version` | Implemented |

## Configuration

| Kiro Config | AX Config | Status |
|---|---|---|
| `~/.kiro/settings.json` | `~/.ax/ax.db` (settings table) | Implemented (DB-based) |
| Agent configs (JSON files) | DB agent_roster (JSON in settings) | Implemented |
| MCP servers | DB mcp_servers table | Implemented |
| Knowledge bases | DB knowledge_docs table | Implemented |
| Per-agent isolation | Single shared DB | Not isolated |

## Missing from AX (by design or deferred)

- **Code intelligence / LSP** — No language server integration
- **OAuth / billing / usage tracking** — Not applicable (direct provider keys)
- **Themes** — Single dark theme
- **Clipboard support** — Terminal limitation
- **Web server** — Deferred (`ax serve`)
- **Agent config files** — Using DB roster instead
- **Hooks** — Not implemented
- **Prompts library** — Not implemented
