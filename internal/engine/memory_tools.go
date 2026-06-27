// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package engine

var memoryTools = []map[string]any{
	{
		"type": "function",
		"function": map[string]any{
			"name":        "save_memory",
			"description": "Save a fact, preference, or important information to persistent memory. Memories persist across conversations.",
			"parameters": map[string]any{
				"type":     "object",
				"required": []string{"key", "value"},
				"properties": map[string]any{
					"key":   map[string]string{"type": "string", "description": "Short identifier (e.g. 'preferred_language', 'project_name', 'user_timezone')"},
					"value": map[string]string{"type": "string", "description": "The information to remember"},
				},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "recall_memory",
			"description": "Search persistent memories for relevant information. Returns matching memories.",
			"parameters": map[string]any{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]any{
					"query": map[string]string{"type": "string", "description": "Search term or key to look up"},
				},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "delete_memory",
			"description": "Delete a memory by its key.",
			"parameters": map[string]any{
				"type":     "object",
				"required": []string{"key"},
				"properties": map[string]any{
					"key": map[string]string{"type": "string", "description": "Memory key to delete"},
				},
			},
		},
	},
}

func init() {
	toolDefs = append(toolDefs, memoryTools...)
}
