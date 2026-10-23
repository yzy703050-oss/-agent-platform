package model

import "time"

type Page struct {
	ID, UserID, SessionID, TokenHash string
	ExpiresAt                        time.Time
}
type PageLease struct {
	PageID    string    `json:"page_id"`
	Token     string    `json:"page_token"`
	ExpiresAt time.Time `json:"expires_at"`
}
type ScopedRun struct {
	Run   Run
	Scope Scope
}
