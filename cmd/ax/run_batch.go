// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1

package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/xnet-admin-1/ax/internal/agent"
	"github.com/xnet-admin-1/ax/internal/engine"
	"github.com/xnet-admin-1/ax/internal/gateway"
	"github.com/xnet-admin-1/ax/internal/mcp"
)

// --- Feature 10: Batch Processing ---

func runBatch(f cliFlags, database *sql.DB, gw *gateway.Router, mcpMgr *mcp.Manager) {
	eng := &engine.Engine{DB: database, Gateway: gw}
	if f.model != "" {
		eng.Model = f.model
	}

	backend := engine.NewLocal(eng.DB, eng.Gateway)
	backend.TrustAll = f.trustAll
	if mcpMgr != nil {
		backend.McpMgr = mcpMgr
	}

	// Read prompts from stdin (one per line)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer

	lineNum := 0
	var results []map[string]any

	for scanner.Scan() {
		lineNum++
		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" || strings.HasPrefix(prompt, "#") {
			continue
		}

		msgs := []engine.Message{{Role: "user", Content: prompt}}

		timeout := f.timeout
		if timeout == 0 {
			timeout = 5 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)

		var resp strings.Builder
		var toolCalls []engine.ToolCall
		var totalTokens int

		// No spinner in batch mode
		spinner := newSpinner()
		// Don't start the spinner in batch mode — silent processing

		err := runChatLoop(ctx, eng, backend, msgs, f, spinner, &resp, &toolCalls, &totalTokens)
		cancel()

		output := stripThought(resp.String())

		switch f.format {
		case "json":
			result := map[string]any{
				"line":     lineNum,
				"prompt":   prompt,
				"response": output,
				"tokens":   totalTokens,
			}
			if err != nil {
				result["error"] = err.Error()
			}
			results = append(results, result)
		default:
			if err != nil {
				fmt.Fprintf(os.Stderr, "[%d] error: %v\n", lineNum, err)
			} else {
				fmt.Printf("[%d] %s\n", lineNum, output)
			}
		}
	}

	if f.format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(results)
	}
}

// --- Feature 4: Agent Orchestration ---

func runAgentOrchestration(f cliFlags, database *sql.DB, gw *gateway.Router, prompt string) {
	agentNames := strings.Split(f.agents, ",")
	for i := range agentNames {
		agentNames[i] = strings.TrimSpace(agentNames[i])
	}

	mgr := agent.NewManager(database, gw)

	fmt.Fprintf(os.Stderr, "⚡ Spawning %d agent(s): %s\n", len(agentNames), strings.Join(agentNames, ", "))

	type agentResult struct {
		name   string
		result string
		err    error
	}

	results := make(chan agentResult, len(agentNames))

	timeout := f.timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.Now().Add(timeout)

	for _, name := range agentNames {
		go func(agentName string) {
			taskID, err := mgr.Spawn(agentName, prompt)
			if err != nil {
				results <- agentResult{name: agentName, err: err}
				return
			}
			// Wait for completion
			for {
				if time.Now().After(deadline) {
					results <- agentResult{name: agentName, err: fmt.Errorf("timeout")}
					return
				}
				t := mgr.GetTask(taskID)
				if t != nil && (t.Status == "done" || t.Status == "error") {
					if t.Status == "error" {
						results <- agentResult{name: agentName, err: fmt.Errorf("%s", t.Result)}
					} else {
						results <- agentResult{name: agentName, result: t.Result}
					}
					return
				}
				time.Sleep(time.Second)
			}
		}(name)
	}

	// Collect results
	allResults := make(map[string]string)
	for i := 0; i < len(agentNames); i++ {
		r := <-results
		if r.err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", r.name, r.err)
			allResults[r.name] = "error: " + r.err.Error()
		} else {
			fmt.Fprintf(os.Stderr, "  ✓ %s: done\n", r.name)
			allResults[r.name] = r.result
		}
	}

	// Output
	switch f.format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(allResults)
	default:
		for _, name := range agentNames {
			fmt.Printf("--- %s ---\n%s\n\n", name, allResults[name])
		}
	}
}

// --- HTTP helpers ---

func newRequestWithContext(ctx context.Context, url, apiKey string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return req, nil
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute}
}
