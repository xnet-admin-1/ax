# AX Feature Spec — TUI/CLI Parity

## 1. Agent System (TUI)

### /agent panel
- List agents from roster (stored in settings_kv key='agent_roster' as JSON)
- Create: name, system prompt, model override (optional), tool allowlist
- Edit: inline fields, save on enter
- Delete: 'd' key on selected agent

### /spawn panel  
- Pick agent from roster dropdown
- Enter task description
- Launch as background task
- Show active task count in status bar

### Agent monitor
- ctrl+a or /monitor opens panel showing all running/completed tasks
- Live log streaming for active task
- 'r' to resume/report result to chat
- 'k' to kill running task

## 2. Context Management

### /compact (LLM-based)
- Send first N + last M messages to LLM with "summarize" prompt
- Replace middle messages with system summary message
- Use a fast/cheap model (configurable via settings task_model_summary)

### /memory panel
- Persistent key-value store (memories table in DB)
- Auto-injected into system prompt when relevant
- CRUD: add, edit, delete memories
- Format: key → value (e.g. "preferred_language" → "Go")

### /knowledge
- Vector store for indexed documents
- /knowledge add <path> — index a file/dir
- /knowledge search <query> — semantic search
- Auto-search on relevant user queries (if enabled in settings)

## 3. Settings & Configuration

### /settings panel
- Task models: title generation, summary/compact, translation
- Search provider URL (default: https://search.xnet.ngo)
- Tool toggles: enable/disable individual tools
- Default model selection
- Auto-compact threshold

## 4. Conversation Management

### Resume (-r flag)
- `ax -r` resumes last conversation (loads messages, continues chat)
- TUI: automatically loads last conversation on start if exists

### /fork
- Creates a new conversation branched from current point
- Copies messages up to current position into new conv

### /export
- Export current conversation to markdown file
- Default path: ~/ax-export-{convID[:8]}.md

### Auto-title
- After first assistant response, generate title using fast model
- Store in conversations.title
- Show in /list panel and status bar

## 5. Input Enhancements

### Editor mode (ctrl+e)
- Opens $EDITOR (default: micro, fallback: nano) with temp file
- On save+close, content becomes the message
- For long/multi-line input

### History (up/down)
- Up arrow recalls previous sent messages
- Down arrow moves forward
- Only when input is empty or at first line

### Inline tool confirmation
- When dangerous command detected, show "Approve: <cmd> (reason)? y/n"
- 'y' approves and continues
- 'n' denies and tells model
- --trust-all flag skips all confirmations

## 6. CLI Flags

```
ax                    # TUI mode
ax -p "prompt"        # One-shot CLI
ax -p "prompt" -m model  # With model
ax -r                 # Resume last conversation
ax -a agent           # Start with agent handoff
ax --models           # List available models
ax --trust-all        # Skip tool confirmations
ax serve              # Web server (future)
```

## 7. Streaming Enhancements

### Tool progress
- Shell commands stream stdout live to the activity line
- Show partial output as it arrives (truncated to 1 line in status)
- Full output shown in tool_result message

### Image/vision
- Detect image paths in user message
- Encode as base64 data URLs in message content
- Models with vision capability process them
- Display image paths inline in chat

## 8. Auto-title
- After first response in a new conversation
- Use task model (fast/cheap) to generate ≤6 word title
- Update conversations.title in DB
- Refresh status bar and /list panel
