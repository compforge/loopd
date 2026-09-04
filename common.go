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

type ResponderRef struct {
	Role Role   `json:"role"`
	ID   string `json:"id"`
}

func (ref ResponderRef) Valid() bool {
	return (ref.Role == RoleHarness || ref.Role == RoleOperator) && ref.ID != ""
}

type Responder struct {
	ResponderRef
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
