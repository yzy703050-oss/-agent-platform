package model

import "time"

type Conversation struct {
	ID        string    `json:"id"`
	AgentType string    `json:"agent_type"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}
