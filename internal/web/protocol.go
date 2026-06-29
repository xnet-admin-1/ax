package web

// Client → Server message types
const (
	MsgChatSend       = "chat.send"
	MsgChatCancel     = "chat.cancel"
	MsgChatRegenerate = "chat.regenerate"
)

// Server → Client message types
const (
	MsgConvCreated  = "conversation.created"
	MsgConvUpdated  = "conversation.updated"
	MsgMsgCreated   = "message.created"
	MsgStreamStart  = "stream.start"
	MsgStreamDelta  = "stream.delta"
	MsgStreamReason = "stream.reasoning"
	MsgStreamTool   = "stream.tool_call"
	MsgStreamResult = "stream.tool_result"
	MsgStreamEnd    = "stream.end"
	MsgStreamError  = "stream.error"
)

// Envelope wraps all WS messages
type Envelope struct {
	Type string `json:"type"`
}

// Client messages
type ChatSendMsg struct {
	Type           string   `json:"type"`
	ConversationID string   `json:"conversationId"`
	Content        string   `json:"content"`
	Mode           string   `json:"mode"`
	Images         []string `json:"images,omitempty"`
}

type ChatCancelMsg struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversationId"`
}

// Server messages
type ConvCreatedMsg struct {
	Type         string `json:"type"`
	Conversation ConvDTO `json:"conversation"`
}

type ConvUpdatedMsg struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Title string `json:"title"`
}

type MessageCreatedMsg struct {
	Type    string  `json:"type"`
	Message MsgDTO  `json:"message"`
}

type StreamStartMsg struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
}

type StreamDeltaMsg struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversationId"`
	Delta          string `json:"delta"`
}

type StreamReasonMsg struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversationId"`
	Delta          string `json:"delta"`
}

type StreamToolCallMsg struct {
	Type           string      `json:"type"`
	ConversationID string      `json:"conversationId"`
	ToolCall       ToolCallDTO `json:"toolCall"`
}

type StreamToolResultMsg struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversationId"`
	ToolCallID     string `json:"toolCallId"`
	Result         string `json:"result"`
	IsError        bool   `json:"isError"`
}

type StreamEndMsg struct {
	Type           string   `json:"type"`
	ConversationID string   `json:"conversationId"`
	Usage          UsageDTO `json:"usage"`
}

type StreamErrorMsg struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversationId"`
	Error          string `json:"error"`
}

// DTOs
type ConvDTO struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type MsgDTO struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	Role           string `json:"role"`
	Content        string `json:"content"`
	Timestamp      int64  `json:"timestamp"`
}

type ToolCallDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type UsageDTO struct {
	TotalTokens int `json:"totalTokens"`
}
