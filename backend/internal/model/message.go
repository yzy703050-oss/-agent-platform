package model

type ToolCall struct{ ID, Name, Arguments string }
type Message struct {
	ID, ConversationID, RunID, Role, Content, ToolCallID string
	Ordinal                                              int64
	ToolCalls                                            []ToolCall
}
