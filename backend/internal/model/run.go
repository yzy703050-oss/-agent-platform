package model

type Run struct {
	ID, ConversationID, UserID, PageID, Status string
	Attempt, Version                           int64
	ContextEligible                            bool
}
