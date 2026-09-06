package view

type RegistrationRequest struct {
	DisplayName  string `json:"display_name,omitempty"`
	Description  string `json:"description,omitempty"`
	LeaseSeconds int    `json:"lease_seconds,omitempty"`
}
