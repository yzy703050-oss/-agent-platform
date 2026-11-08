package platformpg

import "agent-platform/backend/internal/repository/runtimepg"

// The unexported embedding promotes the service ports without exposing a
// mutable cross-service schema selector.
type scopedStore = runtimepg.Store
