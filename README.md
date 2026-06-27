# ax

> **A** autonomous terminal a**x** — an LLM-operated shell-native agent.

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-Proprietary-blue)](#license)
[![Size](https://img.shields.io/badge/Size-25MB%20static%20binary-success)](https://github.com/xnet-admin-1/ax/releases)
[![Platform](https://img.shields.io/badge/Platform-linux%20%7C%20macOS%20%7C%20WSL-lightgrey)](#install)
[![TUI](https://img.shields.io/badge/TUI-Bubbletea-ff69b4)](#tui-commands)

ax connects to any OpenAI-compatible LLM and gives it unrestricted access to your shell,
filesystem, and network — turning it into a hands-on collaborator that reads code, runs
commands, edits files, searches the web, and delegates work to specialized sub-agents.

**Single Go binary. Zero runtime deps. No npm, no pip, no Docker.**

---

## Table of Contents

- [Quick Start](#quick-start)
- [Why ax](#why-ax)
- [Install](#install)
- [Usage](#usage)
- [Architecture](#architecture)
- [Tools](#tools)
- [Agents & Orchestration](#agents--orchestration)
- [Security](#security)
- [Providers](#providers)
- [TUI Commands](#tui-commands)
- [Key Bindings](#key-bindings)
- [Configuration](#configuration)
- [Comparison with Kiro](#comparison-with-kiro)
- [FAQ](#faq)
- [Troubleshooting](#troubleshooting)
- [Build from Source](#build-from-source)
- [License](#license)

---

## Quick Start

```bash
# 1. Download the binary
curl -LO https://github.com/xnet-admin-1/ax/releases/latest/download/ax
chmod +x ax

# 2. Run it (self-installs to /usr/local/bin)
./ax

# 3. Add a provider (OpenAI, Anthropic, Ollama, OpenRouter, DeepSeek, Gemini...)
#    Use /provider in the TUI, or ax will prompt on first launch.

# 4. Try it:
ax -p "What's my current working directory?"
ax -p "Show me the git log for this repo"
ax -p "Find all TODO comments in ./src and summarize them"
```

**One-shot from CLI:**
```bash
ax -p "Read the README, find anything outdated, and write a PR-ready diff"
```

**Orchestrate a multi-agent pipeline:**
```bash
ax -p 'orchestrate a research + implement pipeline: find best practices for Go testing,
then rewrite our test suite to match'
```

**Resume last conversation:**
```bash
ax -r
```

---

## Why ax

Most AI coding tools lock you into one provider, one repo, one language, and a dependency
tree that breaks on every update. ax takes a different position:

### 🧠 You Own the Binary
25 MB, statically compiled, runs anywhere Go compiles. No `node_modules`, no `virtualenvs`,
no containers. Copy it to a server over `scp` and it works.

### 🗂️ You Own the Context
ax operates on your actual filesystem. It doesn't clone repos into sandboxes or limit you
to one project. Point it at anything — a Go service, a Kotlin app, Terraform configs,
a research paper — in the same conversation.

### 🔀 You Choose the Model
Any OpenAI-compatible endpoint. Run local models through **Ollama**, route through
**OpenRouter**, or hit **OpenAI / Anthropic / DeepSeek / Gemini** directly. Switch
mid-conversation. Use a cheap model for background agents and an expensive one for
the main thread.

### 🎭 Agents Are First-Class
Not a prompt wrapper — real background goroutines with independent tool access,
configurable system prompts, and **DAG-based orchestration**. Spawn a researcher,
architect, and coder in parallel. Results flow between stages automatically.

### ✂️ Edits Are Precise
SEARCH/REPLACE blocks with a **three-tier fallback chain** (exact → indent-flexible →
trimmed). The LLM doesn't rewrite your 500-line file to change one function — it targets
the exact lines and the engine handles indentation mismatches gracefully.

### 🔁 Errors Are Learning
When a tool fails, the error goes back to the LLM as context. It sees what went wrong,
adjusts, and retries. **Up to three attempts** before giving up — the same way a
human developer iterates.

### 📋 Compare Side-by-Side
See the full feature comparison with [Kiro CLI](./COMPARISON.md).

---

## Install

### Prebuilt Binary (Recommended)

```bash
# Linux / macOS / WSL
curl -LO https://github.com/xnet-admin-1/ax/releases/latest/download/ax
chmod +x ax
./ax        # Self-installs to /usr/local/bin/ax
```

The first run detects your platform, copies itself to `/usr/local/bin/ax`, and offers to
add it to your PATH. Subsequent runs are just `ax`.

### macOS (Homebrew)

```bash
brew tap xnet-admin-1/tap
brew install ax
```

### Build from Source

Requires **Go 1.25+** with CGo (for SQLite via `mattn/go-sqlite3`).

```bash
git clone https://github.com/xnet-admin-1/ax.git
cd ax
go build -o ax ./cmd/ax
sudo cp ax /usr/local/bin/ax
```

### Verify

```bash
ax --version
ax --models       # List available models (requires provider config)
```

> **Note for headless/SSH:** Works fully via CLI mode (`ax -p "prompt"`). No TUI
> dependencies needed.

---

## Usage

```
ax                    # TUI mode (interactive chat)
ax -p "prompt"        # One-shot CLI mode
ax -p "prompt" -m provider/model
ax -r                 # Resume last conversation
ax -a agent           # Start with agent handoff
ax --models           # List available models
ax --trust-all        # Skip tool confirmations
ax -d                 # Enable debug logging to /tmp/ax-debug.log
ax -v                 # Print version
```

### Switching Models Mid-Conversation

In TUI: `/model <provider>/<modelname>`
In CLI: `-m openai/gpt-4o` or `-m ollama/llama3`

The provider router resolves any `provider/model` string via [gateway.go](./internal/gateway/gateway.go).

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│  TUI (Bubbletea)     ◄───►    CLI Mode     ◄───►   serve │
│  charm.sh/bubbletea          os.Args              (future)│
└────────────────────────┬────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────┐
│  Engine                    internal/engine/engine.go     │
│  • Chat loop & streaming   • Tool dispatch & retry       │
│  • Memory injection        • DAG orchestration           │
│  • Conversation management • Context truncation          │
└────────┬──────────┬──────────────┬──────────────────────┘
         │          │              │
┌────────▼──┐ ┌────▼──────┐ ┌─────▼──────────────┐
│  LLM      │ │  Agent    │ │  Gateway            │
│  Tool defs │ │  Roster   │ │  Provider Router    │
│  Execute   │ │  Spawner  │ │  API base + key     │
│  Dangerous │ │  Monitor  │ │  Model discovery    │
│  cmd check │ │  Pipeline │ └────────────────────┘
└─────┬─────┘ └────┬──────┘
      │            │
┌─────▼────────────▼───────────────────────────────────┐
│  Tools                      internal/llm/llm.go       │
│  ┌───────┐ ┌──────────┐ ┌─────────┐ ┌──────────┐    │
│  │run_sh │ │read_file │ │write_file│ │edit_file │    │
│  ├───────┤ ├──────────┤ ├─────────┤ ├──────────┤    │
│  │list_dir│ │search_web│ │orchestrate│ │memories  │    │
│  └───────┘ └──────────┘ └─────────┘ └──────────┘    │
│                                                       │
│  Edit Engine (3-tier fallback)  internal/edit/edit.go │
│  1. Exact match → 2. Indent-flex → 3. Trimmed        │
└───────────────────────────────────────────────────────┘
│  ┌─────────────────────────────────────────────────┐
│  │  SQLite DB  (~/.ax/ax.db)                      │
│  │  conversations │ messages │ memories │ settings │
│  │  providers │ agent_roster │ mcp_servers         │
│  │  knowledge_docs │ kv_store                      │
│  └─────────────────────────────────────────────────┘
```

### Package Map

| Package | Responsibility |
|---------|---------------|
| `cmd/ax/` | Entrypoint, CLI flags, self-install |
| `internal/engine/` | Chat loop, streaming, tool execution, orchestration |
| `internal/llm/` | Tool definitions, `ExecuteTool`, dangerous cmd detection |
| `internal/agent/` | Agent roster, task manager, background sub-agents |
| `internal/gateway/` | Provider router: `provider/model` → API base + key |
| `internal/edit/` | SEARCH/REPLACE parser with 3-tier flexible matching |
| `internal/db/` | SQLite schema, migrations, seed data |
| `internal/mcp/` | Model Context Protocol client (external tools) |
| `internal/knowledge/` | Vector store for document indexing |
| `tui/` | Full TUI: model, key handler, events, bubbles, layout, panels |

---

## Tools

| Tool | Description |
|------|-------------|
| `run_sh` | Execute bash commands (with dangerous command guarding) |
| `read_file` | Read file contents |
| `write_file` | Write / create files |
| `edit_file` | SEARCH/REPLACE editing with flexible matching |
| `list_dir` | List directory contents |
| `search_web` | Web search via SearXNG |
| `orchestrate` | Multi-agent DAG pipeline |
| `save_memory` | Persist key-value pairs across sessions |
| `recall_memory` | Retrieve stored memories |
| `delete_memory` | Remove memories |

### edit_file — The Smart Editor

The LLM sends a SEARCH/REPLACE block and ax applies it with a **3-tier fallback**:

1. **Exact match** — finds the search text verbatim
2. **Indent-flexible** — normalizes leading whitespace differences (handles LLM indentation drift)
3. **Trimmed match** — strips all surrounding whitespace per line

If all three fail, the error is returned to the LLM which can reformat and retry
(up to 3 attempts).

### orchestrate — DAG Pipeline

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

- Stages **without** `depends_on` run in **parallel**
- Stages **with** `depends_on` wait for dependencies and receive their results as context
- Each sub-agent gets **20 tool-call turns** with **full tool access**
- Results are automatically synthesized and returned

---

## Agents & Orchestration

### Built-in Agent Roster

| Agent | Role | Best For |
|-------|------|----------|
| `default` | General purpose | Everyday tasks |
| `architect` | System design, planning | Architecture decisions, design docs |
| `coder` | Implementation | Writing code, fixing bugs |
| `researcher` | Web search, synthesis | Gathering info, comparing options |
| `qa` | Testing, validation | Unit tests, integration tests, linting |
| `security` | Security audit | Vulnerability scanning, dependency checks |
| `devops` | Infrastructure | CI/CD, Docker, Terraform, cloud |
| `writer` | Documentation | README, docs, changelogs |

### Spawning Agents

In the TUI:
- `/spawn` → pick an agent → enter task → runs in background
- `/monitor` → view running/completed agents
- `r` key → inject agent result into current conversation

Via the `orchestrate` tool: build DAG pipelines with parallel + sequential stages
(see above).

Via CLI:
```bash
ax -a researcher -p "Find the latest Go testing best practices"
```

### Custom Agents

Create custom agents via `/spawn` > `b` (builder) with:
- Custom system prompt
- Model override (different provider/model than main)
- Tool allowlist (restrict which tools the agent can use)

Custom agents are saved to the database roster and persist across sessions.

---

## Security

### Dangerous Command Detection

Shell commands are checked against destructive patterns **before execution**:

| Category | Examples |
|----------|----------|
| Destructive file ops | `rm -rf` outside `/tmp`, `dd`, `mkfs`, `fdisk` |
| Permission changes | `chmod 777`, `chown` |
| Process killing | `kill -9`, `killall` |
| Destructive git | `git push --force`, `git reset --hard` |
| Database destruction | `DROP TABLE`, `DROP DATABASE` |
| System file writes | Targets in `/etc/`, `/usr/`, `/boot/` |

When a dangerous command is detected:
1. **TUI mode:** Shows a confirmation prompt (`y/n`)
2. **CLI mode:** Blocks and returns a warning to the LLM
3. **Bypass:** `ax --trust-all` skips all confirmations

### Sub-Agent Trust

Sub-agents spawned via orchestration are **always trusted** (no secondary confirmation).
They operate with the same tool access as the main agent.

### Data Storage

All state stored in `~/.ax/ax.db` (SQLite):
- Conversations and messages
- Provider API keys
- Persistent memories
- MCP server configurations

File permissions: DB file is `0600` (owner read/write only).

---

## Providers

ax works with any **OpenAI-compatible API**. Configure via `/provider` in TUI or on
the first launch prompt.

### Configured Providers

| Provider | Base URL | Notes |
|----------|----------|-------|
| OpenAI | `https://api.openai.com/v1` | GPT-4o, GPT-4, o3, o4-mini |
| Anthropic | `https://api.anthropic.com` | Claude 4 Opus, Sonnet, Haiku |
| OpenRouter | `https://openrouter.ai/api/v1` | Route to any supported model |
| DeepSeek | `https://api.deepseek.com` | DeepSeek-V3, DeepSeek-R1 |
| Google Gemini | `https://generativelanguage.googleapis.com/v1beta/openai` | Gemini 2.5 Pro, Flash |
| Ollama | `http://localhost:11434/v1` | Local models (Llama 3, Mistral, Qwen, etc.) |
| Custom | Any URL | Any OpenAI-compatible endpoint |

### Provider Configuration

Each provider stores:
- **Name** (identifier)
- **API base URL**
- **API key** (from environment or stored)
- **Model list** (auto-discovered via `/v1/models` endpoint)

Select with `/model` or `-m provider/model`.

> **Tip:** Name your providers short: `openai`, `anthropic`, `ollama`, `deepseek`, `openrouter`.

---

## TUI Commands

| Command | Action |
|---------|--------|
| `/model` | Select model |
| `/provider` | Manage providers |
| `/new` | New conversation |
| `/list` | Conversation history (tree view) |
| `/fork` | Branch a new conversation from current point |
| `/spawn` | Spawn background agent |
| `/monitor` | View running agents (`` `r` `` to resume, `` `k` `` to kill) |
| `/memory` | Manage persistent memories |
| `/tools` | Toggle individual tools on/off |
| `/mcp` | MCP server connections |
| `/settings` | Configuration (models, search URL, auto-compact) |
| `/theme` | Switch light/dark |
| `/debug` | Toggle debug logging |
| `/compact` | Summarize conversation with fast LLM |
| `/export` | Export to markdown file |
| `/clear` | Clear chat view |
| `/usage` | Show token/model usage |
| `/help` | Show all commands |

---

## Key Bindings

| Key | Action |
|-----|--------|
| `shift+up/down` | Scroll chat (line by line) |
| `ctrl+d/u` | Scroll chat (half page) |
| `up/down/left/right` | Navigate text input |
| `shift+left/right` | Prompt history (previous sent messages) |
| `ctrl+c` | Cancel / stop streaming (double=exit) |
| `ctrl+n` | New conversation |
| `ctrl+e` | Open `$EDITOR` for long input |
| `ctrl+o` | Expand / collapse tool output |
| `ctrl+y` | Copy last response |
| `ctrl+a` | Open agent monitor |
| `/` | Command autocomplete |
| `esc` | Close panel |
| `y/n` | Confirm or deny dangerous command |

---

## Configuration

### Settings Panel (`/settings`)

| Setting | Description | Default |
|---------|-------------|---------|
| `title_model` | Model for auto-generating conversation titles | `default` |
| `task_model_summary` | Model for `/compact` summarization | `default` |
| `search_url` | SearXNG endpoint for web search | `https://search.xnet.ngo` |
| `auto_compact` | Auto-compact after N messages | off |

### Data Directory

```
~/.ax/
├── ax.db          # SQLite database (all state)
└── ax-debug.log   # Debug log (when -d or /debug enabled)
```

### Environment Variables

| Variable | Purpose |
|----------|---------|
| `OPENAI_API_KEY` | Default provider key (first launch) |
| `ANTHROPIC_API_KEY` | Anthropic provider key |
| `EDITOR` | Editor for `ctrl+e` (default: `micro`, fallback: `nano`) |
| `SEARXNG_API_KEY` | Search provider key (if required) |

---

## Comparison with Kiro

See the full **[COMPARISON.md](./COMPARISON.md)** for a detailed feature-by-feature
comparison with Kiro CLI.

**Key differences:**

| Feature | ax | Kiro CLI |
|---------|----|----------|
| Runtime | Single Go binary | Requires Go runtime + tools |
| Database | SQLite (single file) | JSON files |
| Custom agents | DB-stored roster | JSON config files |
| DAG orchestration | `orchestrate` tool | `subagent` tool |
| Code intelligence | Not implemented (shell-based) | LSP integration |
| Model switching | Mid-conversation | Mid-conversation |
| Theme | Light/dark | Single dark theme |

---

## FAQ

### Does ax send my code to third parties?
ax sends prompts and context to whatever LLM provider you configure. Your provider
selection determines data handling. Local models (Ollama) never leave your machine.

### Can I use ax over SSH?
Yes. The CLI mode (`ax -p "prompt"`) works perfectly in SSH sessions. The TUI mode
also works if your terminal supports alt-screen and mouse events.

### How do I switch models mid-conversation?
In TUI: `/model provider/modelname`. In CLI: `-m provider/modelname`.
The conversation context is preserved when switching.

### What if the LLM makes a destructive command?
Dangerous commands trigger confirmation prompts. See the [Security](#security) section.

### Can I run ax without a TUI?
Yes. `ax -p "prompt"` runs in CLI mode. Use `ax -r` to resume. Use `ax -a agent -p "..."` for agent handoff. All features work without a TUI.

### How do I stop a runaway agent?
- `ctrl+c` in TUI (double `ctrl+c` exits)
- Kill the process: `pkill ax`
- Background sub-agents: `/monitor` → `k` to kill

### Does ax support streaming?
Yes. LLM responses stream token-by-token. Shell command output streams line-by-line
to the activity line.

### What models work best?
Models with strong tool-calling capabilities: Claude 4 Opus/Sonnet, GPT-4o, DeepSeek-V3,
and local models like Qwen 2.5 and Llama 3 through Ollama.

---

## Troubleshooting

### "No providers configured"
Run `ax` and follow the setup prompt, or use `/provider` in TUI to add an
OpenAI-compatible provider.

### edit_file fails constantly
Make sure the LLM is sending proper SEARCH/REPLACE blocks. The SEARCH block must match
existing file content exactly (after fallback attempts). Use `read_file` first to get
the exact content.

### Binary won't run
```
$ file ax
ax: ELF 64-bit LSB executable, x86-64
$ ldd ax
        statically linked
```
The binary is statically linked. If it won't run, your architecture may not match
(x86_64 vs arm64 vs ...). Build from source for your platform.

### SQLite errors on build (CGo)
```bash
# Install GCC/musl
sudo apt install gcc musl-tools   # Linux
xcode-select --install             # macOS
```
Then rebuild:
```bash
CGO_ENABLED=1 go build -o ax ./cmd/ax
```

### Debug logging
```bash
ax -d                              # Verbose startup logging
# Or in TUI: /debug → toggle on → cycle levels
tail -f /tmp/ax-debug.log
```

### Conversation too long / hitting context limits
Use `/compact` to summarize the conversation. The middle messages are replaced with
a summary to free context window space. Configure which model does the summarizing
in `/settings`.

### Search doesn't work
Check `/settings` → search URL defaults to `https://search.xnet.ngo`.
Set `SEARXNG_API_KEY` if your search instance requires authentication.

---

## Build from Source

```bash
git clone https://github.com/xnet-admin-1/ax.git
cd ax

# Production build
go build -ldflags="-s -w" -o ax ./cmd/ax
# Stripped, ~25MB

# Development build (with debug symbols)
go build -o ax ./cmd/ax

# Cross-compile (example: ARM64)
GOARCH=arm64 go build -o ax-arm64 ./cmd/ax

# Install
sudo cp ax /usr/local/bin/ax
```

### Build Requirements

| Dependency | Version | Notes |
|-----------|---------|-------|
| Go | 1.25+ | Required for modern stdlib |
| GCC / musl-tools | any | CGo support for SQLite |
| make | optional | If you use the Makefile |

---

## License

Proprietary. Copyright © XNet.

---

### 📚 Further Reading

- [Feature Specification (SPEC.md)](./SPEC.md) — Detailed TUI/CLI feature parity docs
- [Comparison with Kiro CLI (COMPARISON.md)](./COMPARISON.md) — Side-by-side feature matrix
- [Source Code Analysis](./AX-SOURCE-ANALYSIS.md) — Generated deep-dive of the codebase
