package model

type Checkpoint struct {
	RunID, UserID, ConversationID, Engine, EngineVersion, Fingerprint string
	FormatVersion                                                     int
	Attempt                                                           int64
	Payload                                                           []byte
	Valid                                                             bool
}
type CheckpointWrite struct {
	Scope      Scope
	Checkpoint Checkpoint
}
