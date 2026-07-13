package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/xnet-admin-1/ax/internal/engine"
)

// BatchRequest is the payload for POST /api/v1/batch.
type BatchRequest struct {
	Prompts []string `json:"prompts"`
	Model   string   `json:"model"`
	Trust   bool     `json:"trust"`
	Timeout string   `json:"timeout"`
}

// BatchResultItem represents a single result in the batch response.
type BatchResultItem struct {
	Prompt   string `json:"prompt"`
	Response string `json:"response"`
	Tokens   int    `json:"tokens"`
}

// BatchResponse is the response for POST /api/v1/batch.
type BatchResponse struct {
	Results []BatchResultItem `json:"results"`
}

// SimulateRequest is the payload for POST /api/v1/simulate.
type SimulateRequest struct {
	Prompt string   `json:"prompt"`
	Model  string   `json:"model"`
	Tools  []string `json:"tools"`
	Agents []string `json:"agents"`
}

// SimulateResponse is the dry-run response for POST /api/v1/simulate.
type SimulateResponse struct {
	Model           string   `json:"model"`
	Endpoint        string   `json:"endpoint"`
	EstimatedTokens int      `json:"estimated_tokens"`
	ToolsAvailable  []string `json:"tools_available"`
	McpTools        []string `json:"mcp_tools"`
	WouldSend       any      `json:"would_send"`
}

// BatchProcess processes multiple prompts sequentially through the engine.
// POST /api/v1/batch
func (h *Handlers) BatchProcess(w http.ResponseWriter, r *http.Request) {
	var req BatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.Prompts) == 0 {
		http.Error(w, "prompts array is required and must not be empty", http.StatusBadRequest)
		return
	}

	if req.Model == "" {
		http.Error(w, "model is required", http.StatusBadRequest)
		return
	}

	// Parse timeout, default to 60s per prompt.
	timeout := 60 * time.Second
	if req.Timeout != "" {
		parsed, err := time.ParseDuration(req.Timeout)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid timeout: %v", err), http.StatusBadRequest)
			return
		}
		timeout = parsed
	}

	results := make([]BatchResultItem, 0, len(req.Prompts))

	for _, prompt := range req.Prompts {
		response, tokens, err := h.processPrompt(prompt, req.Model, req.Trust, timeout)
		if err != nil {
			results = append(results, BatchResultItem{
				Prompt:   prompt,
				Response: fmt.Sprintf("error: %s", err.Error()),
				Tokens:   0,
			})
			continue
		}
		results = append(results, BatchResultItem{
			Prompt:   prompt,
			Response: response,
			Tokens:   tokens,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(BatchResponse{Results: results})
}

// processPrompt runs a single prompt through the engine and collects the full response.
func (h *Handlers) processPrompt(prompt, model string, trust bool, timeout time.Duration) (string, int, error) {
	backend := engine.NewLocal(h.DB, h.Gateway)
	backend.TrustAll = trust

	// Set the model for this request.
	if err := backend.SetModel(model); err != nil {
		return "", 0, fmt.Errorf("failed to set model: %w", err)
	}

	// Create a temporary conversation for this prompt.
	convID, err := backend.CreateConversation(prompt)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create conversation: %w", err)
	}

	ch, err := backend.Chat(convID, prompt)
	if err != nil {
		return "", 0, err
	}

	var response string
	var tokens int
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return response, tokens, nil
			}
			switch ev.Type {
			case "delta":
				response += ev.Delta
			case "end":
				tokens = ev.Tokens
				// Drain remaining events.
				for range ch {
				}
				return response, tokens, nil
			case "error":
				return response, tokens, fmt.Errorf("%s", ev.Error)
			}
		case <-timer.C:
			backend.Cancel(convID)
			return response, tokens, fmt.Errorf("timeout after %s", timeout)
		}
	}
}

// Simulate returns what WOULD be sent to the API without making a call.
// POST /api/v1/simulate
func (h *Handlers) Simulate(w http.ResponseWriter, r *http.Request) {
	var req SimulateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	if req.Model == "" {
		http.Error(w, "model is required", http.StatusBadRequest)
		return
	}

	// Resolve the model to get endpoint details.
	apiBase, _, upstream, err := h.Gateway.Resolve(req.Model)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to resolve model: %v", err), http.StatusBadRequest)
		return
	}

	// Estimate tokens: len(prompt)/4 + 4 per message.
	estimatedTokens := len(req.Prompt)/4 + 4

	// Build the list of tools that would be available.
	toolsAvailable := req.Tools
	if toolsAvailable == nil {
		toolsAvailable = []string{}
	}

	// MCP tools placeholder — return agents as mcp_tools if provided.
	mcpTools := req.Agents
	if mcpTools == nil {
		mcpTools = []string{}
	}

	// Build the would_send payload (what would be sent to the API).
	wouldSend := map[string]any{
		"model": upstream,
		"messages": []map[string]string{
			{"role": "user", "content": req.Prompt},
		},
		"stream": true,
	}
	if len(req.Tools) > 0 {
		wouldSend["tools"] = req.Tools
	}

	endpoint := apiBase + "/chat/completions"

	resp := SimulateResponse{
		Model:           upstream,
		Endpoint:        endpoint,
		EstimatedTokens: estimatedTokens,
		ToolsAvailable:  toolsAvailable,
		McpTools:        mcpTools,
		WouldSend:       wouldSend,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
