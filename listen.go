package loopd

// ListenRequest receives messages for one participant. The cursor is persisted
// by Kubernetes, not supplied by the caller. Use Read for cursor-free history.
type ListenRequest struct {
	Actor ActorRef `json:"actor"`
	Limit int      `json:"limit,omitempty"`
}

type ListenResult struct {
	Messages      []Message `json:"messages"`
	LastMessageID string    `json:"last_message_id,omitempty"`
}
