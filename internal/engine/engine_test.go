package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageSerialization(t *testing.T) {
	// 1. Assistant message with empty content still includes "content":"" in JSON
	m := Message{Role: "assistant", Content: ""}
	b, _ := json.Marshal(m)
	if !strings.Contains(string(b), `"content":""`) {
		t.Fatalf("empty content omitted: %s", b)
	}

	// 2. Tool result message always has name field
	m = Message{Role: "tool", Content: "result", Name: "run_sh", ToolCallID: "call_1"}
	b, _ = json.Marshal(m)
	if !strings.Contains(string(b), `"name":"run_sh"`) {
		t.Fatalf("tool message missing name: %s", b)
	}

	// 3. Assistant message with ToolCalls includes type: function
	m = Message{Role: "assistant", Content: "", ToolCalls: []ToolCall{
		{ID: "call_1", Type: "function", Function: FunctionCall{Name: "run_sh", Arguments: `{}`}},
	}}
	b, _ = json.Marshal(m)
	if !strings.Contains(string(b), `"type":"function"`) {
		t.Fatalf("tool_call missing type: %s", b)
	}

	// 4. Message with ToolCallID includes both tool_call_id and name
	m = Message{Role: "tool", Content: "ok", Name: "save_memory", ToolCallID: "call_2"}
	b, _ = json.Marshal(m)
	if !strings.Contains(string(b), `"tool_call_id":"call_2"`) {
		t.Fatalf("missing tool_call_id: %s", b)
	}
	if !strings.Contains(string(b), `"name":"save_memory"`) {
		t.Fatalf("missing name: %s", b)
	}
}

func TestSystemPromptText(t *testing.T) {
	s := systemPromptText()
	if s == "" {
		t.Fatal("systemPromptText returned empty string")
	}
	if !strings.Contains(s, "AX") {
		t.Fatal("missing AX identity")
	}
	if !strings.Contains(s, "@") {
		t.Fatal("missing hostname marker")
	}
}
