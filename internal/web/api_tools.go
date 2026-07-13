package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/xnet-admin-1/ax/internal/llm"
)

// toolExecRequest is the JSON body for POST /api/v1/tools/execute
type toolExecRequest struct {
	Tool       string         `json:"tool"`
	Parameters map[string]any `json:"parameters"`
	Trust      bool           `json:"trust"`
}

// toolExecResponse is the JSON response for tool execution endpoints.
type toolExecResponse struct {
	Result   string `json:"result"`
	ToolUsed string `json:"tool_used"`
	TimeMs   int64  `json:"time_ms"`
}

// toolsRequiringTrust lists tools that require trust=true in the request.
var toolsRequiringTrust = map[string]bool{
	"run_sh":     true,
	"write_file": true,
	"edit_file":  true,
}

// newToolContext builds a ToolContext from the request trust field.
func newToolContext(trust bool) *llm.ToolContext {
	return &llm.ToolContext{
		ShellOutputLimit:  8000,
		FileReadLimit:     32000,
		TrustAll:          trust,
		SearchProviderURL: "https://search.xnet.ngo",
	}
}

// runTool runs a tool and writes the JSON response with timing headers.
func (h *Handlers) runTool(w http.ResponseWriter, toolName string, params map[string]any, trust bool) {
	// Validate trust requirement
	if toolsRequiringTrust[toolName] && !trust {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{
			"error": fmt.Sprintf("tool %q requires trust=true", toolName),
		})
		return
	}

	ctx := newToolContext(trust)
	start := time.Now()
	result, err := llm.ExecuteTool(toolName, params, ctx)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Tool-Used", toolName)
		w.Header().Set("X-Request-Time-Ms", fmt.Sprintf("%d", elapsed))
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error":     err.Error(),
			"tool_used": toolName,
			"time_ms":   elapsed,
		})
		return
	}

	resp := toolExecResponse{
		Result:   result,
		ToolUsed: toolName,
		TimeMs:   elapsed,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Tool-Used", toolName)
	w.Header().Set("X-Request-Time-Ms", fmt.Sprintf("%d", elapsed))
	json.NewEncoder(w).Encode(resp)
}

// ExecuteTool handles POST /api/v1/tools/execute
// Accepts any tool name with parameters and trust flag.
func (h *Handlers) ExecuteTool(w http.ResponseWriter, r *http.Request) {
	var req toolExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	if req.Tool == "" {
		http.Error(w, `{"error":"tool field is required"}`, http.StatusBadRequest)
		return
	}
	if req.Parameters == nil {
		req.Parameters = make(map[string]any)
	}
	h.runTool(w, req.Tool, req.Parameters, req.Trust)
}

// RunSh handles POST /api/v1/tools/run_sh
func (h *Handlers) RunSh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Command string `json:"command"`
		Trust   bool   `json:"trust"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	params := map[string]any{"command": body.Command}
	h.runTool(w, "run_sh", params, body.Trust)
}

// ReadFile handles POST /api/v1/tools/read_file
func (h *Handlers) ReadFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path  string `json:"path"`
		Trust bool   `json:"trust"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	params := map[string]any{"path": body.Path}
	h.runTool(w, "read_file", params, body.Trust)
}

// WriteFile handles POST /api/v1/tools/write_file
func (h *Handlers) WriteFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Trust   bool   `json:"trust"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	params := map[string]any{"path": body.Path, "content": body.Content}
	h.runTool(w, "write_file", params, body.Trust)
}

// EditFile handles POST /api/v1/tools/edit_file
func (h *Handlers) EditFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path    string `json:"path"`
		Search  string `json:"search"`
		Replace string `json:"replace"`
		Trust   bool   `json:"trust"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	params := map[string]any{"path": body.Path, "search": body.Search, "replace": body.Replace}
	h.runTool(w, "edit_file", params, body.Trust)
}

// ListDir handles POST /api/v1/tools/list_dir
func (h *Handlers) ListDir(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path  string `json:"path"`
		Trust bool   `json:"trust"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	params := map[string]any{"path": body.Path}
	h.runTool(w, "list_dir", params, body.Trust)
}

// SearchWeb handles POST /api/v1/tools/search_web
func (h *Handlers) SearchWeb(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query string `json:"query"`
		Trust bool   `json:"trust"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	params := map[string]any{"query": body.Query}
	h.runTool(w, "search_web", params, body.Trust)
}

// BatchProcess and Simulate are defined in api_batch.go
