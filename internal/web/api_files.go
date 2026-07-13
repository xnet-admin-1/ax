package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const uploadDir = "/tmp/ax-uploads"

// handleFileUpload handles POST /api/v1/files/upload
// Accepts multipart/form-data with a "file" field.
func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing or invalid file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Ensure upload directory exists
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		http.Error(w, "failed to create upload directory", http.StatusInternalServerError)
		return
	}

	// Generate random ID
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		http.Error(w, "failed to generate file id", http.StatusInternalServerError)
		return
	}
	fileID := hex.EncodeToString(idBytes)

	filename := header.Filename
	savePath := fmt.Sprintf("%s/%s-%s", uploadDir, fileID, filename)

	dst, err := os.Create(savePath)
	if err != nil {
		http.Error(w, "failed to create file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	size, err := io.Copy(dst, file)
	if err != nil {
		http.Error(w, "failed to write file", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"file_id":  fileID,
		"filename": filename,
		"size":     size,
		"path":     savePath,
	})
}

// handleMCP handles POST /api/v1/mcp/{server}
// Proxies tool execution to an MCP server via McpMgr.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	serverName := r.PathValue("server")
	if serverName == "" {
		http.Error(w, "missing server name", http.StatusBadRequest)
		return
	}

	var body struct {
		Operation string         `json:"operation"`
		Params    map[string]any `json:"params"`
		Trust     string         `json:"trust"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.Operation == "" {
		http.Error(w, "operation is required", http.StatusBadRequest)
		return
	}

	start := time.Now()
	result, err := s.McpMgr.ExecuteTool(body.Operation, body.Params)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]any{
			"error":   err.Error(),
			"server":  serverName,
			"time_ms": elapsed,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"result":  result,
		"server":  serverName,
		"time_ms": elapsed,
	})
}
