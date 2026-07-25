// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xnet-admin-1/ax/internal/db"
)

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".ax")
	os.MkdirAll(dir, 0700)
	if f, err := os.OpenFile(filepath.Join(dir, "debug.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err == nil {
		log.SetOutput(f)
	}
}

var version = "dev"

type cliFlags struct {
	// Existing flags
	model    string
	prompt   string
	agent    string
	resume   bool
	models   bool
	trustAll bool
	serve    bool
	debug    bool

	// Feature 1: Tool Execution
	trust bool // --trust: show tool calls, auto-approve

	// Feature 3: Timeout Handling
	timeout time.Duration // --timeout: request timeout

	// Feature 4: Agent Orchestration
	agents string // --agents: comma-separated agent names

	// Feature 5: MCP Integration
	mcp     string // --mcp: specific MCP server to connect
	mcpAuto bool   // auto-connect enabled MCP servers

	// Feature 6: Metadata Output
	meta bool // --meta: show metadata (model, tokens, etc.)

	// Feature 7: Output Formats
	format string // --format: json, text, raw

	// Feature 8: Conversation Management
	conversation string // --conversation: specific conversation ID to resume

	// Feature 9: File Input
	file string // --file/-f: read prompt from file

	// Feature 10: Batch Processing
	batch bool // --batch: read prompts from stdin

	// Feature 11: Dry Run
	dryRun bool // --dry-run: validate and show what would be sent without calling the API

	// Data directory override (portable mode)
	dataDir string // --data-dir: use custom data directory
}

func parseFlags() cliFlags {
	var f cliFlags
	f.mcpAuto = true // MCP auto-connect by default
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "serve":
			f.serve = true
		case "--version", "-v":
			fmt.Println("ax", version)
			os.Exit(0)
		case "--help", "-h":
			printUsage()
			os.Exit(0)
		case "--models":
			f.models = true
		case "--trust":
			f.trust = true
		case "--trust-all":
			f.trustAll = true
			f.trust = true
		case "-d", "--debug":
			f.debug = true
		case "-r", "--resume":
			f.resume = true
		case "--meta":
			f.meta = true
		case "--batch":
			f.batch = true
		case "--dry-run":
			f.dryRun = true
		case "--no-mcp":
			f.mcpAuto = false
		case "-m", "--model":
			if i+1 < len(args) {
				i++
				f.model = args[i]
			}
		case "-p", "--prompt":
			if f.serve {
				// In serve mode, -p is port, not prompt — leave it for serve.go
				break
			}
			if i+1 < len(args) {
				i++
				// Consume all remaining args until next flag as the prompt
				var parts []string
				for ; i < len(args); i++ {
					if len(args[i]) > 0 && args[i][0] == '-' {
						i-- // back up so outer loop processes this flag
						break
					}
					parts = append(parts, args[i])
				}
				f.prompt = strings.Join(parts, " ")
			}
		case "-a", "--agent":
			if i+1 < len(args) {
				i++
				f.agent = args[i]
			}
		case "--timeout":
			if i+1 < len(args) {
				i++
				d, err := time.ParseDuration(args[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "invalid timeout: %s\n", args[i])
					os.Exit(1)
				}
				f.timeout = d
			}
		case "--agents":
			if i+1 < len(args) {
				i++
				f.agents = args[i]
			}
		case "--mcp":
			if i+1 < len(args) {
				i++
				f.mcp = args[i]
			}
		case "--format":
			if i+1 < len(args) {
				i++
				f.format = args[i]
			}
		case "--conversation", "-c":
			if i+1 < len(args) {
				i++
				f.conversation = args[i]
			}
		case "-f", "--file":
			if i+1 < len(args) {
				i++
				f.file = args[i]
			}
		case "--data-dir":
			if i+1 < len(args) {
				i++
				f.dataDir = args[i]
			}
		}
	}
	return f
}

func main() {
	f := parseFlags()
	if f.dataDir != "" {
		db.DataDir = f.dataDir
	}
	switch {
	case f.serve:
		runServe()
	case f.models:
		runListModels()
	case f.prompt != "" || f.file != "" || f.batch:
		runCLI(f)
	default:
		runTUI(f)
	}
}



func printUsage() {
	fmt.Println(`ax - terminal AI agent

Usage:
  ax                         Launch TUI
  ax serve                   Start web server + API
  ax -p "prompt"             One-shot CLI mode
  ax -f prompt.txt           Read prompt from file
  ax --batch < prompts.txt   Process multiple prompts

Core Flags:
  -p, --prompt "text"   Prompt text
  -m, --model model     Select model (provider/model)
  -a, --agent agent     Start with agent
  -r, --resume          Resume last conversation
  -c, --conversation ID Resume specific conversation
  -f, --file path       Read prompt from file
  -v, --version         Print version

Tool Execution:
  --trust               Enable tools with auto-approval
  --trust-all           Trust all tools without any confirmation

Processing:
  --timeout duration    Request timeout (default 5m, e.g. 30s, 10m)
  --agents "a,b,c"      Spawn agents in parallel
  --batch               Read prompts from stdin (one per line)
  --dry-run             Validate and show what would be sent (no API call)

Output:
  --format fmt          Output format: text (default), json, raw
  --meta                Show metadata (model, tokens) on stderr

Integration:
  --mcp server          Connect specific MCP server
  --no-mcp              Don't auto-connect MCP servers
  --models              List available models

Debug:
  -d, --debug           Enable debug logging

Portable:
  --data-dir path       Use custom data directory (overrides auto-detection)
                        Auto-detects: .ax/ or ax.db next to binary → ~/.ax/`)
}
