package server

import "time"

// DefaultMessageTTL bounds inactive output and Redis event retention. Each store
// renews independently; Redis eviction never determines a message's SQL status.
const DefaultMessageTTL = 24 * time.Hour
