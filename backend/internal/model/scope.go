package model

// Scope is assembled by server authentication, never taken from model arguments.
type Scope struct{ UserID, SessionID, PageID string }
type UserSettings struct {
	Theme           string `json:"theme"`
	ReplyPreference string `json:"reply_preference"`
}
