package model

// Registration advertises an actor; its kind is fixed by the selected registry.
type Registration struct {
	Key         string
	DisplayName string
	Description string
}
