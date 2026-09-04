// Package service owns loop-server's application use cases.
package service

import (
	"errors"
	"log/slog"
)

var (
	ErrInvalid  = errors.New("invalid request")
	ErrConflict = errors.New("conflict")
)

func loggerOrDefault(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}
