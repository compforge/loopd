package loopd

// Consumption follows Kafka's log/queue semantics: messages remain in DB,
// each actor consumes independently, and Poll never implies Commit. Offsets
// are inclusive Message IDs rather than Kafka's numeric next-record offsets.
// https://kafka.apache.org/42/javadoc/org/apache/kafka/clients/consumer/KafkaConsumer.html

// PollRequest reads a bounded batch. After is the current consumption position;
// omit it on recovery to resume from the participant's committed CRD cursor.
type PollRequest struct {
	Actor ActorRef `json:"actor"`
	Limit int      `json:"limit,omitempty"`
	After string   `json:"after,omitempty"`
}

type PollResult struct {
	// EndOffset is the CRD wake hint, not a bound on the database query.
	EndOffset string    `json:"end_offset,omitempty"`
	Committed string    `json:"committed,omitempty"`
	Messages  []Message `json:"messages"`
	Position  string    `json:"position,omitempty"`
}

// CommitRequest acknowledges a contiguous prefix already handled or durably
// adopted by the Operator. It does not close the conversation or a UI stream.
type CommitRequest struct {
	Actor   ActorRef `json:"actor"`
	Through string   `json:"through"`
}
