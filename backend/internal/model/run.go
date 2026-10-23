package model

type Run struct {
	ID              string `json:"id"`
	ConversationID  string `json:"conversation_id"`
	UserID          string `json:"-"`
	PageID          string `json:"page_id"`
	Status          string `json:"status"`
	Attempt         int64  `json:"attempt"`
	Version         int64  `json:"version"`
	ContextEligible bool   `json:"context_eligible"`
}
