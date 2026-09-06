// Package contract defines the stable collaboration model shared by loop-server,
// Operator runtimes, and Harness adapters.
package contract

import (
	"strings"
	"time"
)

// ActorKind is an open string enum. The constants name built-in participants.
type ActorKind string

const (
	ActorKindUser     ActorKind = "user"
	ActorKindHarness  ActorKind = "harness"
	ActorKindOperator ActorKind = "operator"
)

// Valid checks identity shape, not membership in the built-in constants.
// ActorKind is open; namespace-prefix policy is not enforced here.
func (kind ActorKind) Valid() bool {
	value := string(kind)
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value
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

// Actor describes any participant, including a user or an Operator-owned role.
// Registry discovery currently returns only online Operators and Harnesses.
type Actor struct {
	ActorRef
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type Timestamped struct {
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (kind ActorKind) IsOperator() bool {
	return kind == ActorKindOperator || (kind.Valid() && strings.HasPrefix(string(kind), "operator/"))
}
