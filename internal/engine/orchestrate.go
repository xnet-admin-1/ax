package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

var orchestrateTool = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        "orchestrate",
		"description": "Execute a multi-agent pipeline (DAG). Stages without depends_on run in parallel. Results from completed stages are passed as context to dependent stages. Returns combined results from all stages.",
		"parameters": map[string]any{
			"type":     "object",
			"required": []string{"task", "stages"},
			"properties": map[string]any{
				"task":   map[string]string{"type": "string", "description": "Overall task description"},
				"stages": map[string]any{"type": "array", "description": "Array of stages: [{name: string, agent: string, prompt: string, depends_on: [string]}]", "items": map[string]any{"type": "object"}},
			},
		},
	},
}

func init() {
	toolDefs = append(toolDefs, orchestrateTool)
}

type Stage struct {
	Name      string   `json:"name"`
	Agent     string   `json:"agent"`
	Prompt    string   `json:"prompt"`
	DependsOn []string `json:"depends_on"`
}

// RunPipeline executes stages as a DAG — parallel where possible, sequential where dependencies exist.
func (l *Local) RunPipeline(task string, stages []Stage, ch chan Event) string {
	results := make(map[string]string)
	var mu sync.Mutex
	completed := make(map[string]bool)

	for len(completed) < len(stages) {
		// Find ready stages (all deps satisfied)
		var ready []Stage
		for _, s := range stages {
			if completed[s.Name] {
				continue
			}
			depsOk := true
			for _, dep := range s.DependsOn {
				if !completed[dep] {
					depsOk = false
					break
				}
			}
			if depsOk {
				ready = append(ready, s)
			}
		}
		if len(ready) == 0 {
			break // deadlock
		}

		// Run ready stages in parallel
		var wg sync.WaitGroup
		for _, s := range ready {
			wg.Add(1)
			go func(st Stage) {
				defer wg.Done()
				// Build context from dependencies
				var ctx string
				for _, dep := range st.DependsOn {
					mu.Lock()
					ctx += fmt.Sprintf("\n--- %s result ---\n%s\n", dep, results[dep])
					mu.Unlock()
				}
				prompt := st.Prompt
				if ctx != "" {
					prompt += "\n\nContext from prior stages:" + ctx
				}
				ch <- Event{Type: "progress", ToolName: "orchestrate", ToolResult: fmt.Sprintf("[%s] started (%s)", st.Name, st.Agent)}
				
				id, err := l.AgentMgr.Spawn(st.Agent, prompt)
				if err != nil {
					mu.Lock()
					results[st.Name] = "error: " + err.Error()
					completed[st.Name] = true
					mu.Unlock()
					return
				}
				// Wait for completion
				for i := 0; i < 300; i++ {
					t := l.AgentMgr.GetTask(id)
					if t != nil && (t.Status == "done" || t.Status == "error") {
						mu.Lock()
						results[st.Name] = t.Result
						completed[st.Name] = true
						mu.Unlock()
						ch <- Event{Type: "progress", ToolName: "orchestrate", ToolResult: fmt.Sprintf("[%s] completed", st.Name)}
						return
					}
					time.Sleep(time.Second)
					// 1 second sleep via time import in local.go
				}
				mu.Lock()
				results[st.Name] = "timeout"
				completed[st.Name] = true
				mu.Unlock()
			}(s)
		}
		wg.Wait()
	}

	// Combine results
	var out strings.Builder
	for _, s := range stages {
		fmt.Fprintf(&out, "## %s (%s)\n%s\n\n", s.Name, s.Agent, results[s.Name])
	}
	return out.String()
}

// ExecuteOrchestrate parses the tool call and runs the pipeline
func (l *Local) ExecuteOrchestrate(argsJSON string, ch chan Event) string {
	var params struct {
		Task   string  `json:"task"`
		Stages []Stage `json:"stages"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &params); err != nil {
		return "error parsing orchestrate args: " + err.Error()
	}
	if l.AgentMgr == nil {
		return "orchestrate not available: no agent manager"
	}
	ch <- Event{Type: "progress", ToolName: "orchestrate", ToolResult: fmt.Sprintf("Orchestrating %d stages...", len(params.Stages))}
	return l.RunPipeline(params.Task, params.Stages, ch)
}
