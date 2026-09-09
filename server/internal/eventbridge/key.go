package eventbridge

import "fmt"

const (
	DefaultKeyPrefix = "loopd:agentue"
	MessageKeyFormat = "message/%s"
)

// MessageKey addresses one message independently of its writer or subscriber Pod.
// AgentUE adds the configured prefix and :events/:state suffixes to this key.
func MessageKey(messageID string) string {
	return fmt.Sprintf(MessageKeyFormat, messageID)
}
