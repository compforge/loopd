// Package view defines transport and presentation models shared by API and service.
// It contains no handlers, persistence, or enrichment logic.
package view

type ErrorResponse struct {
	Error Error `json:"error"`
}
type Error struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
type Page[T any] struct {
	Data []T `json:"data"`
}
