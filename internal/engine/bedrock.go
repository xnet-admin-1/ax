// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/xnet-admin-1/ax/internal/debug"
)

// IsBedrockProvider returns true if the apiBase indicates a Bedrock provider.
func IsBedrockProvider(apiBase string) bool {
	lower := strings.ToLower(apiBase)
	// Only route to Bedrock SDK for bedrock-runtime endpoints, NOT bedrock-mantle
	// bedrock-mantle is OpenAI Chat Completions compatible
	if strings.Contains(lower, "bedrock-runtime") {
		return true
	}
	if matched := bedrockRegionRe.MatchString(lower); matched {
		return true
	}
	return false
}

var bedrockRegionRe = regexp.MustCompile(`^(us|eu|ap|sa|ca|me|af)-(east|west|north|south|central|southeast|northeast)\w*-\d+$`)

// bedrockStream calls AWS Bedrock ConverseStream and emits events on ch.
func (l *Local) bedrockStream(ctx context.Context, region, apiKey, model string, messages []Message, ch chan Event) (string, []ToolCall, int, string, error) {
	debug.D.Info("bedrock: streaming model=%s region=%s hasKey=%v", model, region, apiKey != "")

	if apiKey != "" {
		os.Setenv("AWS_BEARER_TOKEN_BEDROCK", apiKey)
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return "", nil, 0, "", fmt.Errorf("bedrock: load config: %w", err)
	}
	client := bedrockruntime.NewFromConfig(cfg)

	// First pass: collect valid tool use IDs from assistant messages
	validToolUseIDs := make(map[string]bool)
	for _, m := range messages {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				validToolUseIDs[tc.ID] = true
			}
		}
	}

	// Convert messages to Bedrock format
	var bedrockMsgs []types.Message
	var systemPrompts []types.SystemContentBlock
	for i := 0; i < len(messages); i++ {
		m := messages[i]
		if m.Role == "system" {
			systemPrompts = append(systemPrompts, &types.SystemContentBlockMemberText{Value: m.Content})
			continue
		}
		role := types.ConversationRoleUser
		if m.Role == "assistant" {
			role = types.ConversationRoleAssistant
		}
		// Handle tool_result messages — group consecutive ones, but only include
		// those whose IDs match the preceding assistant's tool_use blocks.
		if m.Role == "tool" {
			// Find the preceding assistant message's tool_use IDs
			precedingToolUseIDs := make(map[string]bool)
			for j := len(bedrockMsgs) - 1; j >= 0; j-- {
				if bedrockMsgs[j].Role == types.ConversationRoleAssistant {
					for _, block := range bedrockMsgs[j].Content {
						if tu, ok := block.(*types.ContentBlockMemberToolUse); ok {
							precedingToolUseIDs[aws.ToString(tu.Value.ToolUseId)] = true
						}
					}
					break
				}
			}

			var toolResults []types.ContentBlock
			for ; i < len(messages) && messages[i].Role == "tool"; i++ {
				tid := messages[i].ToolCallID
				// Only include tool results that match the preceding assistant's tool_use IDs
				if tid == "" || !validToolUseIDs[tid] {
					debug.D.Warn("bedrock: skipping orphaned tool result id=%s name=%s", tid, messages[i].Name)
					continue
				}
				if len(precedingToolUseIDs) > 0 && !precedingToolUseIDs[tid] {
					debug.D.Warn("bedrock: skipping tool result id=%s (not in preceding assistant's tool_use set)", tid)
					continue
				}
				toolResults = append(toolResults, &types.ContentBlockMemberToolResult{
					Value: types.ToolResultBlock{
						ToolUseId: aws.String(tid),
						Content: []types.ToolResultContentBlock{
							&types.ToolResultContentBlockMemberText{Value: messages[i].Content},
						},
					},
				})
			}
			i--
			if len(toolResults) > 0 {
				bedrockMsgs = append(bedrockMsgs, types.Message{
					Role:    types.ConversationRoleUser,
					Content: toolResults,
				})
			}
			continue
		}
		// Handle assistant messages with tool calls
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var contentBlocks []types.ContentBlock
			if m.Content != "" {
				contentBlocks = append(contentBlocks, &types.ContentBlockMemberText{Value: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var argsDoc map[string]interface{}
				json.Unmarshal([]byte(tc.Function.Arguments), &argsDoc)
				if argsDoc == nil {
					argsDoc = map[string]interface{}{}
				}
				name := sanitizeToolName(tc.Function.Name)
				contentBlocks = append(contentBlocks, &types.ContentBlockMemberToolUse{
					Value: types.ToolUseBlock{
						ToolUseId: aws.String(tc.ID),
						Name:      aws.String(name),
						Input:     documentFromMap(argsDoc),
					},
				})
			}
			bedrockMsgs = append(bedrockMsgs, types.Message{
				Role:    role,
				Content: contentBlocks,
			})
			continue
		}
		// Skip empty assistant messages (no content, no tool calls)
		if m.Role == "assistant" && m.Content == "" {
			continue
		}
		// Regular text message
		bedrockMsgs = append(bedrockMsgs, types.Message{
			Role:    role,
			Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: m.Content}},
		})
	}

	// Validate tool_use/tool_result pairing:
	// 1. Every assistant tool_use must have a matching tool_result in the next user message
	// 2. tool_results must not exceed the tool_use count of the preceding assistant
	var validatedMsgs []types.Message
	for i := 0; i < len(bedrockMsgs); i++ {
		msg := bedrockMsgs[i]

		if msg.Role == types.ConversationRoleAssistant {
			// Collect tool_use IDs from this assistant message
			toolUseIDs := make(map[string]bool)
			for _, block := range msg.Content {
				if tu, ok := block.(*types.ContentBlockMemberToolUse); ok {
					toolUseIDs[aws.ToString(tu.Value.ToolUseId)] = true
				}
			}
			validatedMsgs = append(validatedMsgs, msg)

			if len(toolUseIDs) > 0 {
				// Check if next message is a user message with tool_results
				nextHasToolResults := false
				if i+1 < len(bedrockMsgs) && bedrockMsgs[i+1].Role == types.ConversationRoleUser {
					for _, block := range bedrockMsgs[i+1].Content {
						if _, ok := block.(*types.ContentBlockMemberToolResult); ok {
							nextHasToolResults = true
							break
						}
					}
				}

				if nextHasToolResults {
					// Filter tool_results to only include ones matching this assistant's tool_use IDs
					nextMsg := bedrockMsgs[i+1]
					var filteredContent []types.ContentBlock
					for _, block := range nextMsg.Content {
						if tr, ok := block.(*types.ContentBlockMemberToolResult); ok {
							id := aws.ToString(tr.Value.ToolUseId)
							if toolUseIDs[id] {
								filteredContent = append(filteredContent, block)
								delete(toolUseIDs, id)
							} else {
								debug.D.Warn("bedrock: dropping extra tool_result id=%s at message %d", id, i+1)
							}
						} else {
							filteredContent = append(filteredContent, block)
						}
					}
					// Add dummy results for any tool_use IDs that weren't matched
					for id := range toolUseIDs {
						filteredContent = append(filteredContent, &types.ContentBlockMemberToolResult{
							Value: types.ToolResultBlock{
								ToolUseId: aws.String(id),
								Content: []types.ToolResultContentBlock{
									&types.ToolResultContentBlockMemberText{Value: "error: tool call was interrupted"},
								},
							},
						})
					}
					if len(filteredContent) > 0 {
						validatedMsgs = append(validatedMsgs, types.Message{
							Role:    types.ConversationRoleUser,
							Content: filteredContent,
						})
					}
					i++ // skip the next message since we processed it
				} else {
					// Next message is plain text or missing — insert dummy tool_results
					var dummyResults []types.ContentBlock
					for id := range toolUseIDs {
						dummyResults = append(dummyResults, &types.ContentBlockMemberToolResult{
							Value: types.ToolResultBlock{
								ToolUseId: aws.String(id),
								Content: []types.ToolResultContentBlock{
									&types.ToolResultContentBlockMemberText{Value: "error: tool call was interrupted"},
								},
							},
						})
					}
					validatedMsgs = append(validatedMsgs, types.Message{
						Role:    types.ConversationRoleUser,
						Content: dummyResults,
					})
					debug.D.Warn("bedrock: inserted %d dummy tool_results before plain user message at %d", len(dummyResults), i)
				}
			}
		} else {
			// For user messages with tool_results that aren't preceded by an
			// assistant tool_use, strip the tool_result blocks (orphaned results)
			if msg.Role == types.ConversationRoleUser {
				hasToolResult := false
				for _, block := range msg.Content {
					if _, ok := block.(*types.ContentBlockMemberToolResult); ok {
						hasToolResult = true
						break
					}
				}
				if hasToolResult {
					// Check if preceded by assistant with tool_use
					preceded := false
					if len(validatedMsgs) > 0 {
						last := validatedMsgs[len(validatedMsgs)-1]
						if last.Role == types.ConversationRoleAssistant {
							for _, block := range last.Content {
								if _, ok := block.(*types.ContentBlockMemberToolUse); ok {
									preceded = true
									break
								}
							}
						}
					}
					if !preceded {
						debug.D.Warn("bedrock: dropping orphaned user message with tool_results at position %d", i)
						continue
					}
				}
			}
			validatedMsgs = append(validatedMsgs, msg)
		}
	}
	bedrockMsgs = validatedMsgs

	// Debug: log message structure
	for i, msg := range bedrockMsgs {
		var desc string
		for _, b := range msg.Content {
			switch b.(type) {
			case *types.ContentBlockMemberText:
				desc += "T "
			case *types.ContentBlockMemberToolUse:
				desc += "TU "
			case *types.ContentBlockMemberToolResult:
				desc += "TR "
			}
		}
		if i < 35 || strings.Contains(desc, "TU") || strings.Contains(desc, "TR") {
			debug.D.Verbose("bedrock msg[%d] role=%s blocks=[%s]", i, msg.Role, desc)
		}
	}

	// Build tool config
	toolSpecs := l.getBedrockTools()
	debug.D.Info("bedrock: %d tools configured", len(toolSpecs))

	// Build request
	meta := GetBedrockModelMeta(model)
	estInput := estimateTokens(messages)
	safeMax := meta.SafeMaxTokens(estInput)
	debug.D.Info("bedrock: model=%s msgs=%d bedrockMsgs=%d estInput=%d safeMaxTokens=%d streamingTools=%v", model, len(messages), len(bedrockMsgs), estInput, safeMax, meta.StreamingTools)
	input := &bedrockruntime.ConverseStreamInput{
		ModelId:  aws.String(model),
		Messages: bedrockMsgs,
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens:   aws.Int32(safeMax),
			Temperature: aws.Float32(0.7),
		},
	}
	if len(systemPrompts) > 0 && meta.SystemSupported {
		input.System = systemPrompts
	}
	if len(toolSpecs) > 0 {
		input.ToolConfig = &types.ToolConfiguration{
			Tools: toolSpecs,
		}
	}

	// If model doesn't support streaming tools, stream without toolConfig
	// (model can still respond with text, just can't make tool calls)
	// If model doesn't support streaming tools, route to sync Converse (with tools)
	// or stream without tools if no tool blocks exist in history
	if !meta.StreamingTools {
		hasToolBlocks := false
		for _, msg := range bedrockMsgs {
			for _, block := range msg.Content {
				switch block.(type) {
				case *types.ContentBlockMemberToolUse, *types.ContentBlockMemberToolResult:
					hasToolBlocks = true
				}
				if hasToolBlocks { break }
			}
			if hasToolBlocks { break }
		}
		if hasToolBlocks || input.ToolConfig != nil {
			// Use sync Converse with tools (model supports tool use, just not streaming)
			return l.bedrockConverseSync(ctx, client, input, ch)
		}
		input.ToolConfig = nil
	}

	// Call ConverseStream
	output, err := client.ConverseStream(ctx, input)
	if err != nil {
		if strings.Contains(err.Error(), "doesn't support tool use") {
			return l.bedrockConverseSync(ctx, client, input, ch)
		}
		return "", nil, 0, "", fmt.Errorf("bedrock: converse stream: %w", err)
	}

	// Process event stream
	var content strings.Builder
	var toolCalls []ToolCall
	var currentToolUseID string
	var currentToolName string
	var currentToolArgs strings.Builder
	var tokens int
	var finishReason string

	for event := range output.GetStream().Events() {
		select {
		case <-ctx.Done():
			return content.String(), toolCalls, tokens, "cancelled", ctx.Err()
		default:
		}

		switch v := event.(type) {
		case *types.ConverseStreamOutputMemberContentBlockStart:
			if toolUse, ok := v.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
				currentToolUseID = aws.ToString(toolUse.Value.ToolUseId)
				currentToolName = aws.ToString(toolUse.Value.Name)
				currentToolArgs.Reset()
				debug.D.Verbose("bedrock stream: toolUse start name=%s id=%s", currentToolName, currentToolUseID)
			}

		case *types.ConverseStreamOutputMemberContentBlockDelta:
			if textDelta, ok := v.Value.Delta.(*types.ContentBlockDeltaMemberText); ok {
				content.WriteString(textDelta.Value)
				ch <- Event{Type: "delta", Delta: textDelta.Value}
			}
			if reasonDelta, ok := v.Value.Delta.(*types.ContentBlockDeltaMemberReasoningContent); ok {
				if textBlock, ok := reasonDelta.Value.(*types.ReasoningContentBlockDeltaMemberText); ok {
					ch <- Event{Type: "delta", Reasoning: textBlock.Value}
				}
			}
			if toolDelta, ok := v.Value.Delta.(*types.ContentBlockDeltaMemberToolUse); ok {
				if toolDelta.Value.Input != nil {
					currentToolArgs.WriteString(*toolDelta.Value.Input)
				}
			}

		case *types.ConverseStreamOutputMemberContentBlockStop:
			if currentToolName != "" {
				args := currentToolArgs.String()
				debug.D.Verbose("bedrock stream: toolUse stop name=%s argsLen=%d", currentToolName, len(args))
				toolCalls = append(toolCalls, ToolCall{
					ID:   currentToolUseID,
					Type: "function",
					Function: FunctionCall{
						Name:      currentToolName,
						Arguments: args,
					},
				})
				currentToolName = ""
				currentToolUseID = ""
			}

		case *types.ConverseStreamOutputMemberMessageStop:
			reason := string(v.Value.StopReason)
			debug.D.Verbose("bedrock stream: messageStop reason=%s", reason)
			if reason == "tool_use" {
				finishReason = "tool_calls"
			} else {
				finishReason = reason
			}

		case *types.ConverseStreamOutputMemberMetadata:
			if v.Value.Usage != nil {
				tokens = int(aws.ToInt32(v.Value.Usage.TotalTokens))
			}
		}
	}

	debug.D.Info("bedrock: done finishReason=%s contentLen=%d toolCalls=%d tokens=%d", finishReason, content.Len(), len(toolCalls), tokens)

	// Guard: flush incomplete tool call if stream ended prematurely
	if currentToolName != "" {
		args := currentToolArgs.String()
		debug.D.Warn("bedrock: flushing incomplete tool call name=%s argsLen=%d", currentToolName, len(args))
		toolCalls = append(toolCalls, ToolCall{
			ID:   currentToolUseID,
			Type: "function",
			Function: FunctionCall{
				Name:      currentToolName,
				Arguments: args,
			},
		})
		if finishReason == "" || finishReason == "end_turn" {
			finishReason = "tool_calls"
		}
	}

	return content.String(), toolCalls, tokens, finishReason, nil
}

// bedrockConverseSync uses the non-streaming Converse API (supports tools for all models).
func (l *Local) bedrockConverseSync(ctx context.Context, client *bedrockruntime.Client, streamInput *bedrockruntime.ConverseStreamInput, ch chan Event) (string, []ToolCall, int, string, error) {
	debug.D.Info("bedrock: falling back to sync Converse (model doesn't support streaming tool use)")

	input := &bedrockruntime.ConverseInput{
		ModelId:         streamInput.ModelId,
		Messages:        streamInput.Messages,
		System:          streamInput.System,
		InferenceConfig: streamInput.InferenceConfig,
		ToolConfig:      streamInput.ToolConfig,
	}

	output, err := client.Converse(ctx, input)
	if err != nil {
		return "", nil, 0, "", fmt.Errorf("bedrock: converse sync: %w", err)
	}

	var content strings.Builder
	var toolCalls []ToolCall
	var tokens int

	if output.Usage != nil {
		tokens = int(aws.ToInt32(output.Usage.TotalTokens))
	}

	if output.Output != nil {
		if msg, ok := output.Output.(*types.ConverseOutputMemberMessage); ok {
			for _, block := range msg.Value.Content {
				switch b := block.(type) {
				case *types.ContentBlockMemberText:
					content.WriteString(b.Value)
					ch <- Event{Type: "delta", Delta: b.Value}
				case *types.ContentBlockMemberToolUse:
					var argsMap map[string]interface{}
					if b.Value.Input != nil {
						b.Value.Input.UnmarshalSmithyDocument(&argsMap)
					}
					if argsMap == nil {
						argsMap = map[string]interface{}{}
					}
					argsBytes, _ := json.Marshal(argsMap)
					toolCalls = append(toolCalls, ToolCall{
						ID:   aws.ToString(b.Value.ToolUseId),
						Type: "function",
						Function: FunctionCall{
							Name:      aws.ToString(b.Value.Name),
							Arguments: string(argsBytes),
						},
					})
				}
			}
		}
	}

	finishReason := string(output.StopReason)
	if finishReason == "tool_use" {
		finishReason = "tool_calls"
	}

	debug.D.Info("bedrock: sync done finishReason=%s contentLen=%d toolCalls=%d tokens=%d", finishReason, content.Len(), len(toolCalls), tokens)

	return content.String(), toolCalls, tokens, finishReason, nil
}

// getBedrockTools builds tool specs from engine's toolDefs + MCP tools
func (l *Local) getBedrockTools() []types.Tool {
	var toolSpecs []types.Tool
	for _, t := range toolDefs {
		fn, ok := t["function"].(map[string]any)
		if !ok {
			continue
		}
		name, _ := fn["name"].(string)
		desc, _ := fn["description"].(string)
		params, _ := fn["parameters"].(map[string]any)
		spec := types.ToolSpecification{
			Name:        aws.String(name),
			Description: aws.String(desc),
		}
		if params != nil {
			spec.InputSchema = &types.ToolInputSchemaMemberJson{Value: documentFromMap(params)}
		}
		toolSpecs = append(toolSpecs, &types.ToolMemberToolSpec{Value: spec})
	}
	if l.McpMgr != nil {
		for _, t := range l.McpMgr.GetToolDefs() {
			spec := types.ToolSpecification{
				Name:        aws.String(t.Name),
				Description: aws.String(t.Description),
			}
			if m, ok := t.InputSchema.(map[string]any); ok && m != nil {
				spec.InputSchema = &types.ToolInputSchemaMemberJson{Value: documentFromMap(m)}
			}
			toolSpecs = append(toolSpecs, &types.ToolMemberToolSpec{Value: spec})
		}
	}
	return toolSpecs
}

// documentFromMap converts a Go map to a Bedrock Document
func documentFromMap(m map[string]any) document.Interface {
	return document.NewLazyDocument(m)
}

// sanitizeToolName ensures tool names match Bedrock's pattern [a-zA-Z0-9_-]+
var toolNameRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func sanitizeToolName(name string) string {
	sanitized := toolNameRe.ReplaceAllString(name, "_")
	if sanitized == "" {
		return "unknown_tool"
	}
	return sanitized
}

// ParseBedrockRegion extracts the region from a bedrock provider's apiBase field.
func ParseBedrockRegion(apiBase string) string {
	if apiBase == "" {
		return "us-west-2"
	}
	if !strings.Contains(apiBase, "/") && !strings.Contains(apiBase, ".") {
		return apiBase
	}
	parts := strings.Split(apiBase, ".")
	for i, p := range parts {
		if p == "bedrock-runtime" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return "us-west-2"
}

// BedrockModelMeta holds cached model capabilities
type BedrockModelMeta struct {
	MaxTokens       int32
	ContextWindow   int
	StreamingTools  bool
	SystemSupported bool
	Reasoning       bool
}

// SafeMaxTokens returns a reasonable max_tokens to request.
func (m BedrockModelMeta) SafeMaxTokens(estimatedInputTokens int) int32 {
	maxAllowed := int32(m.ContextWindow * 80 / 100)
	if estimatedInputTokens > 0 {
		remaining := int32(m.ContextWindow - estimatedInputTokens)
		if remaining < maxAllowed {
			maxAllowed = remaining
		}
	}
	if maxAllowed > m.MaxTokens {
		maxAllowed = m.MaxTokens
	}
	agentCap := int32(16384)
	if maxAllowed < agentCap {
		return maxAllowed
	}
	return agentCap
}

// GetBedrockModelMeta returns model metadata.
func GetBedrockModelMeta(model string) BedrockModelMeta {
	lower := strings.ToLower(model)

	// Claude Opus 4.6+: 1M context
	if strings.Contains(lower, "claude-opus-4-6") || strings.Contains(lower, "claude-opus-4-7") || strings.Contains(lower, "claude-opus-4-8") {
		return BedrockModelMeta{MaxTokens: 128000, ContextWindow: 1000000, StreamingTools: true, SystemSupported: true, Reasoning: true}
	}
	if strings.Contains(lower, "claude-opus-4-5") {
		return BedrockModelMeta{MaxTokens: 64000, ContextWindow: 200000, StreamingTools: true, SystemSupported: true, Reasoning: true}
	}
	if strings.Contains(lower, "claude-opus-4-1") {
		return BedrockModelMeta{MaxTokens: 32000, ContextWindow: 200000, StreamingTools: true, SystemSupported: true, Reasoning: true}
	}
	if strings.Contains(lower, "claude-sonnet-4-6") || strings.Contains(lower, "claude-sonnet-5") {
		return BedrockModelMeta{MaxTokens: 128000, ContextWindow: 200000, StreamingTools: true, SystemSupported: true, Reasoning: true}
	}
	if strings.Contains(lower, "claude-sonnet-4-5") {
		return BedrockModelMeta{MaxTokens: 64000, ContextWindow: 200000, StreamingTools: true, SystemSupported: true, Reasoning: true}
	}
	if strings.Contains(lower, "claude-sonnet-4-2") {
		return BedrockModelMeta{MaxTokens: 65536, ContextWindow: 200000, StreamingTools: true, SystemSupported: true, Reasoning: true}
	}
	if strings.Contains(lower, "claude-haiku") {
		return BedrockModelMeta{MaxTokens: 64000, ContextWindow: 200000, StreamingTools: true, SystemSupported: true, Reasoning: false}
	}
	if strings.Contains(lower, "claude-fable") {
		return BedrockModelMeta{MaxTokens: 128000, ContextWindow: 200000, StreamingTools: true, SystemSupported: true, Reasoning: true}
	}
	if strings.Contains(lower, "anthropic") || strings.Contains(lower, "claude") {
		return BedrockModelMeta{MaxTokens: 64000, ContextWindow: 200000, StreamingTools: true, SystemSupported: true, Reasoning: true}
	}
	// Amazon Nova
	if strings.Contains(lower, "nova-2-lite") {
		return BedrockModelMeta{MaxTokens: 65535, ContextWindow: 300000, StreamingTools: true, SystemSupported: true}
	}
	if strings.Contains(lower, "nova-pro") {
		return BedrockModelMeta{MaxTokens: 10000, ContextWindow: 300000, StreamingTools: true, SystemSupported: true}
	}
	if strings.Contains(lower, "nova-lite") {
		return BedrockModelMeta{MaxTokens: 5000, ContextWindow: 300000, StreamingTools: true, SystemSupported: true}
	}
	if strings.Contains(lower, "nova-micro") {
		return BedrockModelMeta{MaxTokens: 5000, ContextWindow: 128000, StreamingTools: true, SystemSupported: true}
	}
	if strings.Contains(lower, "nova-premier") {
		return BedrockModelMeta{MaxTokens: 10000, ContextWindow: 1000000, StreamingTools: true, SystemSupported: true}
	}
	if strings.Contains(lower, "nova") {
		return BedrockModelMeta{MaxTokens: 5000, ContextWindow: 300000, StreamingTools: true, SystemSupported: true}
	}
	// DeepSeek
	if strings.Contains(lower, "deepseek") {
		return BedrockModelMeta{MaxTokens: 16384, ContextWindow: 128000, StreamingTools: false, SystemSupported: true, Reasoning: true}
	}
	// Meta Llama
	if strings.Contains(lower, "llama4") {
		return BedrockModelMeta{MaxTokens: 4096, ContextWindow: 1000000, StreamingTools: false, SystemSupported: true}
	}
	if strings.Contains(lower, "llama") || strings.Contains(lower, "meta") {
		return BedrockModelMeta{MaxTokens: 4096, ContextWindow: 128000, StreamingTools: false, SystemSupported: true}
	}
	// Mistral
	if strings.Contains(lower, "mistral-large") || strings.Contains(lower, "devstral") {
		return BedrockModelMeta{MaxTokens: 16384, ContextWindow: 128000, StreamingTools: false, SystemSupported: true}
	}
	if strings.Contains(lower, "mistral") {
		return BedrockModelMeta{MaxTokens: 16384, ContextWindow: 32000, StreamingTools: false, SystemSupported: true}
	}
	// Qwen
	if strings.Contains(lower, "qwen") {
		return BedrockModelMeta{MaxTokens: 16384, ContextWindow: 128000, StreamingTools: false, SystemSupported: true, Reasoning: true}
	}
	// Moonshot/Kimi
	if strings.Contains(lower, "kimi") || strings.Contains(lower, "moonshot") {
		return BedrockModelMeta{MaxTokens: 16384, ContextWindow: 128000, StreamingTools: false, SystemSupported: true}
	}
	// OpenAI on Bedrock
	if strings.Contains(lower, "openai") || strings.Contains(lower, "gpt") {
		return BedrockModelMeta{MaxTokens: 16384, ContextWindow: 128000, StreamingTools: true, SystemSupported: true, Reasoning: true}
	}
	// NVIDIA
	if strings.Contains(lower, "nvidia") || strings.Contains(lower, "nemotron") {
		return BedrockModelMeta{MaxTokens: 16384, ContextWindow: 128000, StreamingTools: false, SystemSupported: true}
	}
	// MiniMax
	if strings.Contains(lower, "minimax") {
		return BedrockModelMeta{MaxTokens: 16384, ContextWindow: 1000000, StreamingTools: false, SystemSupported: true}
	}
	// Z.AI/GLM
	if strings.Contains(lower, "zai") || strings.Contains(lower, "glm") {
		return BedrockModelMeta{MaxTokens: 16384, ContextWindow: 128000, StreamingTools: false, SystemSupported: true}
	}
	// Google Gemma
	if strings.Contains(lower, "gemma") {
		return BedrockModelMeta{MaxTokens: 16384, ContextWindow: 128000, StreamingTools: false, SystemSupported: true}
	}
	// Default
	return BedrockModelMeta{MaxTokens: 4096, ContextWindow: 32000, StreamingTools: false, SystemSupported: true}
}
