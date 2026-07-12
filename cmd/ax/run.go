// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/xnet-admin-1/ax/internal/debug"
	"github.com/xnet-admin-1/ax/internal/engine"
	"github.com/xnet-admin-1/ax/internal/mcp"
)

// runCLI is the main CLI entrypoint handling all 10 features.
func runCLI(f cliFlags) {
	initDebug(f)
	debug.D.Info("cli: prompt=%q model=%s resume=%v", f.prompt, f.model, f.resume)

	database, gw := openDB()
	defer database.Close()

	// Feature 5: MCP Integration - connect MCP servers if --mcp specified
	var mcpMgr *mcp.Manager
	if f.mcp != "" || f.mcpAuto {
		mcpMgr = mcp.NewManager(database)
		mcpMgr.ConnectEnabled()
	}

	// Feature 9: File Input - read prompt from file if --file specified
	prompt := f.prompt
	if f.file != "" {
		data, err := os.ReadFile(f.file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading file %s: %v\n", f.file, err)
			os.Exit(1)
		}
		if prompt != "" {
			prompt = prompt + "\n\n" + string(data)
		} else {
			prompt = string(data)
		}
	}

	// Feature 10: Batch Processing - read lines from stdin or file
	if f.batch {
		runBatch(f, database, gw, mcpMgr)
		return
	}

	// Feature 4: Agent Orchestration - delegate to agents
	if f.agents != "" {
		runAgentOrchestration(f, database, gw, prompt)
		return
	}

	// Build the engine
	eng := &engine.Engine{DB: database, Gateway: gw}
	if f.model != "" {
		eng.Model = f.model
	}

	// Feature 8: Conversation Management - resume or use specific conversation
	var msgs []engine.Message
	var convID string

	if f.conversation != "" {
		convID = f.conversation
		msgs = loadConversation(database, convID)
	} else if f.resume {
		database.QueryRow("SELECT id FROM conversations ORDER BY updated_at DESC LIMIT 1").Scan(&convID)
		if convID != "" {
			msgs = loadConversation(database, convID)
		}
	}

	msgs = append(msgs, engine.Message{Role: "user", Content: prompt})

	// Feature 11: Dry Run - show what would be sent without calling the API
	if f.dryRun {
		runDryRun(f, eng, msgs, convID, mcpMgr)
		return
	}

	// Feature 3: Timeout Handling
	timeout := f.timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Feature 2: Streaming Feedback - spinner + streaming output
	spinner := newSpinner()
	spinner.Start()

	// Feature 1: Tool Execution with --trust / --trust-all
	var resp strings.Builder
	var toolCalls []engine.ToolCall
	var totalTokens int

	// Use the Local backend for full tool support
	backend := engine.NewLocal(eng.DB, eng.Gateway)
	backend.TrustAll = f.trustAll
	if mcpMgr != nil {
		backend.McpMgr = mcpMgr
	}

	// Run the chat loop manually for CLI with tool execution
	err := runChatLoop(ctx, eng, backend, msgs, f, spinner, &resp, &toolCalls, &totalTokens)
	spinner.Stop()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Fprintln(os.Stderr, "error: request timed out after", timeout)
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(1)
	}

	output := resp.String()
	// Strip thought tags
	if i := strings.Index(output, "</thought>"); i >= 0 {
		output = strings.TrimSpace(output[i+len("</thought>"):])
	}
	if i := strings.Index(output, "</think>"); i >= 0 {
		output = strings.TrimSpace(output[i+len("</think>"):])
	}

	// Feature 7: Output Formats
	switch f.format {
	case "json":
		result := map[string]any{
			"response": output,
			"tokens":   totalTokens,
		}
		if f.meta {
			result["model"] = eng.SelectedModel()
			result["conversation_id"] = convID
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(result)
	case "raw":
		fmt.Print(output)
	default: // "text"
		fmt.Println(output)
	}

	// Feature 6: Metadata Output
	if f.meta && f.format != "json" {
		fmt.Fprintf(os.Stderr, "---\nmodel: %s\ntokens: %d\n", eng.SelectedModel(), totalTokens)
		if convID != "" {
			fmt.Fprintf(os.Stderr, "conversation: %s\n", convID)
		}
	}
}
