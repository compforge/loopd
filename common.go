// Package loopd defines the stable collaboration model shared by loop-server,
// Operator runtimes, and Harness adapters.
package loopd

import (
	"regexp"
	"strings"
	"time"
)

type ActorKind string

const (
	ActorKindUser     ActorKind = "user"
	ActorKindHarness  ActorKind = "harness"
	ActorKindOperator ActorKind = "operator"
)

func (role ActorKind) Valid() bool {
	switch role {
	case ActorKindUser, ActorKindHarness, ActorKindOperator:
		return true
	default:
		return len(role) <= 128 && customActorKind.MatchString(string(role))
	}
}

type ActorRef struct {
	Kind ActorKind `json:"kind"`
	Key  string    `json:"key"`
}

func (ref ActorRef) Valid() bool {
	return ref.Kind.Valid() && strings.TrimSpace(ref.Key) == ref.Key && ref.Key != "" && len(ref.Key) <= 128
}

func (ref ActorRef) ValidTarget() bool {
	return ref.Valid() && ref.Kind != ActorKindUser
}

type Actor struct {
	ActorRef
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type Timestamped struct {
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Custom actor kinds identify Operator-owned participants; they do not register a service.
var customActorKind = regexp.MustCompile(`^operator/[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._-]*$`)

func (kind ActorKind) IsOperator() bool {
	return kind == ActorKindOperator || (kind.Valid() && strings.HasPrefix(string(kind), "operator/"))
}
