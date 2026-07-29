// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package mcp

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xnet-admin-1/ax/internal/db"
)

type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
	ServerID    string `json:"-"`
}

type ServerConfig struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Enabled bool              `json:"enabled"`
}

type Server struct {
	Config  ServerConfig
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	running bool
	tools   []ToolDef
	mu      sync.Mutex
	nextID  int
}

type Manager struct {
	DB      *sql.DB
	servers map[string]*Server
}

type jsonrpcReq struct{ JSONRPC string `json:"jsonrpc"`; Method string `json:"method"`; ID int `json:"id"`; Params any `json:"params"` }

func NewManager(db *sql.DB) *Manager {
	db.Exec(`CREATE TABLE IF NOT EXISTS mcp_servers(id TEXT PRIMARY KEY,name TEXT NOT NULL,command TEXT NOT NULL,args TEXT NOT NULL DEFAULT '[]',env TEXT NOT NULL DEFAULT '{}',enabled INTEGER NOT NULL DEFAULT 1)`)
	m := &Manager{DB: db, servers: make(map[string]*Server)}
	m.loadConfigFile()
	return m
}

// loadConfigFile merges mcp.json from the data directory into the DB (upsert)
func (m *Manager) loadConfigFile() {
	path := filepath.Join(db.ResolveDataDir(), "mcp.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var cfg struct {
		Servers []ServerConfig `json:"servers"`
	}
	if json.Unmarshal(data, &cfg) != nil || len(cfg.Servers) == 0 {
		cfg.Servers = nil
		// Try Claude Desktop format: {"mcpServers": {"name": {...}}}
		var claudeFmt struct {
			McpServers map[string]struct {
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
				Enabled *bool             `json:"enabled"`
			} `json:"mcpServers"`
		}
		if json.Unmarshal(data, &claudeFmt) == nil && len(claudeFmt.McpServers) > 0 {
			for name, s := range claudeFmt.McpServers {
				enabled := true
				if s.Enabled != nil {
					enabled = *s.Enabled
				}
				cfg.Servers = append(cfg.Servers, ServerConfig{
					ID: name, Name: name, Command: s.Command, Args: s.Args, Env: s.Env, Enabled: enabled,
				})
			}
		} else {
			// Try flat map format: {"name": {command, args, env}}
			var alt map[string]struct {
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
				Enabled *bool             `json:"enabled"`
			}
			if json.Unmarshal(data, &alt) == nil {
				for name, s := range alt {
					if s.Command == "" {
						continue
					}
					enabled := true
					if s.Enabled != nil {
						enabled = *s.Enabled
					}
					cfg.Servers = append(cfg.Servers, ServerConfig{
						ID: name, Name: name, Command: s.Command, Args: s.Args, Env: s.Env, Enabled: enabled,
					})
				}
			}
		}
	}
	for _, s := range cfg.Servers {
		if s.ID == "" {
			s.ID = s.Name
		}
		if s.Env == nil {
			s.Env = map[string]string{}
		}
		argsJ, _ := json.Marshal(s.Args)
		envJ, _ := json.Marshal(s.Env)
		enabled := 0
		if s.Enabled {
			enabled = 1
		}
		m.DB.Exec(`INSERT OR REPLACE INTO mcp_servers(id,name,command,args,env,enabled) VALUES(?,?,?,?,?,?)`,
			s.ID, s.Name, s.Command, string(argsJ), string(envJ), enabled)
	}
}

// ConnectEnabled connects all enabled servers. Call on startup.
func (m *Manager) ConnectEnabled() []error {
	var errs []error
	for _, cfg := range m.ListServers() {
		if cfg.Enabled {
			if err := m.Connect(cfg.ID); err != nil {
				errs = append(errs, fmt.Errorf("mcp %s: %w", cfg.Name, err))
			}
		}
	}
	return errs
}

// DisconnectAll stops all running servers.
func (m *Manager) DisconnectAll() {
	for id := range m.servers {
		m.Disconnect(id)
	}
}

// SaveConfigFile writes current servers back to mcp.json in the data directory
func (m *Manager) SaveConfigFile() error {
	dir := db.ResolveDataDir()
	os.MkdirAll(dir, 0700)
	servers := m.ListServers()
	data, _ := json.MarshalIndent(struct {
		Servers []ServerConfig `json:"servers"`
	}{Servers: servers}, "", "  ")
	return os.WriteFile(filepath.Join(dir, "mcp.json"), data, 0600)
}

func (m *Manager) ListServers() []ServerConfig {
	rows, err := m.DB.Query("SELECT id,name,command,args,env,enabled FROM mcp_servers")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var cfgs []ServerConfig
	for rows.Next() {
		var c ServerConfig
		var argsJ, envJ string
		var enabled int
		rows.Scan(&c.ID, &c.Name, &c.Command, &argsJ, &envJ, &enabled)
		json.Unmarshal([]byte(argsJ), &c.Args)
		json.Unmarshal([]byte(envJ), &c.Env)
		c.Enabled = enabled == 1
		cfgs = append(cfgs, c)
	}
	return cfgs
}

func (m *Manager) AddServer(cfg ServerConfig) error {
	argsJ, _ := json.Marshal(cfg.Args)
	envJ, _ := json.Marshal(cfg.Env)
	enabled := 0
	if cfg.Enabled {
		enabled = 1
	}
	_, err := m.DB.Exec("INSERT INTO mcp_servers(id,name,command,args,env,enabled) VALUES(?,?,?,?,?,?)",
		cfg.ID, cfg.Name, cfg.Command, string(argsJ), string(envJ), enabled)
	return err
}

func (m *Manager) RemoveServer(id string) error {
	m.Disconnect(id)
	_, err := m.DB.Exec("DELETE FROM mcp_servers WHERE id=?", id)
	return err
}

func (m *Manager) Connect(id string) error {
	if s, ok := m.servers[id]; ok && s.running {
		return nil
	}
	var cfg ServerConfig
	for _, c := range m.ListServers() {
		if c.ID == id {
			cfg = c
			break
		}
	}
	if cfg.ID == "" {
		return fmt.Errorf("server %s not found", id)
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	if len(cfg.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range cfg.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	stdin, _ := cmd.StdinPipe()
	stdoutPipe, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		return err
	}
	s := &Server{Config: cfg, cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdoutPipe), running: true, nextID: 1}
	m.servers[id] = s
	if _, err := s.call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "ax",
			"version": "0.2.0",
		},
	}); err != nil {
		s.stop()
		delete(m.servers, id)
		return err
	}
	// Send initialized notification (no id, no response expected)
	notif, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	notif = append(notif, '\n')
	s.stdin.Write(notif)
	resp, err := s.call("tools/list", map[string]any{})
	if err != nil {
		s.stop()
		delete(m.servers, id)
		return err
	}
	var result struct{ Tools []ToolDef }
	json.Unmarshal(resp, &result)
	for i := range result.Tools {
		result.Tools[i].ServerID = id
	}
	s.tools = result.Tools
	return nil
}

func (m *Manager) Disconnect(id string) {
	if s, ok := m.servers[id]; ok {
		s.stop()
		delete(m.servers, id)
	}
}

func (m *Manager) GetToolDefs() []ToolDef {
	var defs []ToolDef
	for _, s := range m.servers {
		defs = append(defs, s.tools...)
	}
	return defs
}

func (m *Manager) ExecuteTool(name string, args map[string]any) (string, error) {
	for _, s := range m.servers {
		for _, t := range s.tools {
			if t.Name == name {
				resp, err := s.call("tools/call", map[string]any{"name": name, "arguments": args})
				if err != nil {
					return "", err
				}
				// Parse MCP content response: {"content":[{"type":"text","text":"..."}]}
				var result struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
					IsError bool `json:"isError"`
				}
				if json.Unmarshal(resp, &result) == nil && len(result.Content) > 0 {
					var texts []string
					for _, c := range result.Content {
						if c.Text != "" {
							texts = append(texts, c.Text)
						}
					}
					if result.IsError {
						return "", fmt.Errorf("%s", strings.Join(texts, "\n"))
					}
					return strings.Join(texts, "\n"), nil
				}
				return string(resp), nil
			}
		}
	}
	return "", fmt.Errorf("tool %s not found", name)
}

func (s *Server) call(method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	req := jsonrpcReq{JSONRPC: "2.0", Method: method, ID: id, Params: params}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := s.stdin.Write(data); err != nil {
		return nil, err
	}
	// Read lines until we get a response with our id (skip notifications/logs)
	// Use a generous timeout for slow external API calls
	type readResult struct {
		line []byte
		err  error
	}
	for i := 0; i < 200; i++ {
		ch := make(chan readResult, 1)
		go func() {
			line, err := s.stdout.ReadBytes('\n')
			ch <- readResult{line, err}
		}()
		select {
		case res := <-ch:
			if res.err != nil {
				return nil, res.err
			}
			var msg struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(res.line, &msg) != nil {
				continue // not valid JSON, skip
			}
			if msg.ID == nil {
				continue // notification (no id), skip
			}
			if *msg.ID != id {
				continue // response to different request, skip
			}
			if msg.Error != nil {
				return nil, fmt.Errorf("rpc error %d: %s", msg.Error.Code, msg.Error.Message)
			}
			return msg.Result, nil
		case <-time.After(60 * time.Second):
			return nil, fmt.Errorf("timeout waiting for response to %s (id=%d)", method, id)
		}
	}
	return nil, fmt.Errorf("no response received for %s (id=%d) after 200 lines", method, id)
}

func (s *Server) stop() {
	s.running = false
	s.stdin.Close()
	s.cmd.Process.Kill()
	s.cmd.Wait()
}

// InstallTool handles the install_mcp tool calls from the LLM
func (m *Manager) InstallTool(args map[string]any) (string, error) {
	action, _ := args["action"].(string)

	switch action {
	case "install":
		name, _ := args["name"].(string)
		command, _ := args["command"].(string)
		if name == "" || command == "" {
			return "error: name and command required", nil
		}
		var cmdArgs []string
		if a, ok := args["args"].([]any); ok {
			for _, v := range a {
				if s, ok := v.(string); ok {
					cmdArgs = append(cmdArgs, s)
				}
			}
		}
		env := map[string]string{}
		if e, ok := args["env"].(map[string]any); ok {
			for k, v := range e {
				if s, ok := v.(string); ok {
					env[k] = s
				}
			}
		}
		cfg := ServerConfig{
			ID: name, Name: name, Command: command, Args: cmdArgs, Env: env, Enabled: true,
		}
		// Remove existing if present
		m.RemoveServer(name)
		if err := m.AddServer(cfg); err != nil {
			return fmt.Sprintf("error adding server: %v", err), nil
		}
		if err := m.Connect(name); err != nil {
			return fmt.Sprintf("server added but failed to connect: %v", err), nil
		}
		tools := []string{}
		if s, ok := m.servers[name]; ok {
			for _, t := range s.tools {
				tools = append(tools, t.Name)
			}
		}
		m.SaveConfigFile()
		return fmt.Sprintf("installed and connected MCP server '%s' (%s %v)\nTools available: %v", name, command, cmdArgs, tools), nil

	case "remove":
		name, _ := args["name"].(string)
		if name == "" {
			return "error: name required", nil
		}
		m.RemoveServer(name)
		m.SaveConfigFile()
		return fmt.Sprintf("removed MCP server '%s'", name), nil

	case "list":
		servers := m.ListServers()
		if len(servers) == 0 {
			return "no MCP servers configured", nil
		}
		result := "MCP Servers:\n"
		for _, s := range servers {
			status := "disconnected"
			toolCount := 0
			if srv, ok := m.servers[s.ID]; ok && srv.running {
				status = "connected"
				toolCount = len(srv.tools)
			}
			enabled := "enabled"
			if !s.Enabled {
				enabled = "disabled"
			}
			result += fmt.Sprintf("- %s: %s %v [%s, %s, %d tools]\n", s.Name, s.Command, s.Args, status, enabled, toolCount)
		}
		return result, nil

	case "reconnect":
		name, _ := args["name"].(string)
		if name == "" {
			return "error: name required", nil
		}
		m.Disconnect(name)
		if err := m.Connect(name); err != nil {
			return fmt.Sprintf("error reconnecting: %v", err), nil
		}
		return fmt.Sprintf("reconnected MCP server '%s'", name), nil

	default:
		return "error: action must be install, remove, list, or reconnect", nil
	}
}
