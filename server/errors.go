package server

import "errors"

var (
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("conflict")
	ErrInvalid     = errors.New("invalid request")
	ErrUnavailable = errors.New("unavailable")
)
