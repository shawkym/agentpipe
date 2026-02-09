package matrix

// SyncResponse is a minimal Matrix sync response representation.
type SyncResponse struct {
	NextBatch string            `json:"next_batch"`
	Rooms     SyncResponseRooms `json:"rooms"`
}

type SyncResponseRooms struct {
	Join map[string]SyncRoom `json:"join"`
}

type SyncRoom struct {
	Timeline SyncTimeline `json:"timeline"`
}

type SyncTimeline struct {
	Events []MatrixEvent `json:"events"`
}

// MatrixEvent represents a timeline event we care about.
type MatrixEvent struct {
	Type           string             `json:"type"`
	Sender         string             `json:"sender"`
	EventID        string             `json:"event_id"`
	OriginServerTS int64              `json:"origin_server_ts"`
	Content        MatrixEventContent `json:"content"`
}

type MatrixEventContent struct {
	MsgType string `json:"msgtype"`
	Body    string `json:"body"`
}
