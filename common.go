// Package loopd defines the stable collaboration model shared by loop-server,
// Operator runtimes, and Harness adapters.
package loopd

import "time"

type Role string

const (
	RoleUser     Role = "user"
	RoleHarness  Role = "harness"
	RoleOperator Role = "operator"
)

func (role Role) Valid() bool {
	switch role {
	case RoleUser, RoleHarness, RoleOperator:
		return true
	default:
		return false
	}
}

type ActorRef struct {
	Kind Role   `json:"kind"`
	Key  string `json:"key"`
}

func (ref ActorRef) Valid() bool {
	return ref.Kind.Valid() && ref.Key != ""
}

func (ref ActorRef) ValidTarget() bool {
	return (ref.Kind == RoleHarness || ref.Kind == RoleOperator) && ref.Key != ""
}

type Actor struct {
	ActorRef
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type ResourceRef struct {
	APIVersion string `json:"api_version"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}

type Timestamped struct {
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
