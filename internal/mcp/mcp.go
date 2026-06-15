package mcp

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
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
type jsonrpcResp struct{ Result json.RawMessage `json:"result"`; Error *struct{ Message string } `json:"error"` }

func NewManager(db *sql.DB) *Manager {
	db.Exec(`CREATE TABLE IF NOT EXISTS mcp_servers(id TEXT PRIMARY KEY,name TEXT NOT NULL,command TEXT NOT NULL,args TEXT NOT NULL DEFAULT '[]',env TEXT NOT NULL DEFAULT '{}',enabled INTEGER NOT NULL DEFAULT 1)`)
	return &Manager{DB: db, servers: make(map[string]*Server)}
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
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, _ := cmd.StdinPipe()
	stdoutPipe, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		return err
	}
	s := &Server{Config: cfg, cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdoutPipe), running: true, nextID: 1}
	m.servers[id] = s
	if _, err := s.call("initialize", map[string]any{"capabilities": map[string]any{}}); err != nil {
		s.stop()
		delete(m.servers, id)
		return err
	}
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
	line, err := s.stdout.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp jsonrpcResp
	json.Unmarshal(line, &resp)
	if resp.Error != nil {
		return nil, fmt.Errorf("rpc error: %s", resp.Error.Message)
	}
	return resp.Result, nil
}

func (s *Server) stop() {
	s.running = false
	s.stdin.Close()
	s.cmd.Process.Kill()
	s.cmd.Wait()
}
