package api

import (
	"encoding/json"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/view"
)

// Snapshots carry Message metadata; ordinary deltas are bare AgentUE events.
func messageEventData(message *contract.Message, event json.RawMessage) ([]byte, error) {
	return json.Marshal(view.MessageEvent{Message: message, Event: event})
}
