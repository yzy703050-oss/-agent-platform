package postgres

import "agent-platform/backend/internal/agent/ports"

var _ ports.RunStore = (*Repository)(nil)
var _ ports.MessageStore = (*Repository)(nil)
var _ ports.CheckpointStore = (*Repository)(nil)
