// Package api defines loopd's public HTTP contract.
package api

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

type ErrorResponse struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type Page[T any] struct {
	Data []T `json:"data"`
}

type Timestamped struct {
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
