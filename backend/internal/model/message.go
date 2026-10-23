package model

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type Message struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversation_id"`
	RunID          string     `json:"run_id"`
	Role           string     `json:"role"`
	Content        string     `json:"content"`
	ToolCallID     string     `json:"tool_call_id"`
	Ordinal        int64      `json:"ordinal"`
	ToolCalls      []ToolCall `json:"tool_calls"`
	DisplayStatus  string     `json:"display_status,omitempty"`
}
