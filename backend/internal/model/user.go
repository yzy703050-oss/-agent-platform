package model

import "time"

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}
type Account struct {
	User         User
	PasswordHash string
}
type Session struct {
	ID, UserID, TokenHash, CSRFHash string
	ExpiresAt                       time.Time
}
