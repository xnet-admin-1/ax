package web

import (
	"github.com/xnet-admin-1/ax/internal/engine"
)

// BridgeEvents reads from an engine event channel and sends WS messages to the client
func BridgeEvents(ch <-chan engine.Event, client *Client, convID string) {
	for ev := range ch {
		switch ev.Type {
		case "delta":
			if ev.Delta != "" {
				client.Send(StreamDeltaMsg{
					Type:           MsgStreamDelta,
					ConversationID: convID,
					Delta:          ev.Delta,
				})
			}
			if ev.Reasoning != "" {
				client.Send(StreamReasonMsg{
					Type:           MsgStreamReason,
					ConversationID: convID,
					Delta:          ev.Reasoning,
				})
			}
		case "tool_call":
			client.Send(StreamToolCallMsg{
				Type:           MsgStreamTool,
				ConversationID: convID,
				ToolCall: ToolCallDTO{
					ID:        ev.Tool,
					Name:      ev.ToolName,
					Arguments: ev.ToolArgs,
				},
			})
		case "tool_result":
			isErr := len(ev.ToolResult) > 6 && ev.ToolResult[:6] == "error:"
			client.Send(StreamToolResultMsg{
				Type:           MsgStreamResult,
				ConversationID: convID,
				ToolCallID:     ev.Tool,
				Result:         ev.ToolResult,
				IsError:        isErr,
			})
		case "end":
			client.Send(StreamEndMsg{
				Type:           MsgStreamEnd,
				ConversationID: convID,
				Usage:          UsageDTO{TotalTokens: ev.TotalTokens},
			})
		case "confirm":
			// Auto-approve in web mode (no confirm UI yet)
			if ev.ConfirmCh != nil {
				ev.ConfirmCh <- true
			}
		case "error":
			client.Send(StreamErrorMsg{
				Type:           MsgStreamError,
				ConversationID: convID,
				Error:          ev.Error,
			})
		case "progress":
			// Send as tool_result update
			if ev.ToolName != "" {
				client.Send(StreamToolResultMsg{
					Type:           MsgStreamResult,
					ConversationID: convID,
					ToolCallID:     ev.ToolName,
					Result:         ev.ToolResult,
				})
			}
		}
	}
}
