package model

import (
	"errors"
	"net/http"
)

// Error describes a runtime failure. Retryable defaults to false; it is a hint,
// not permission to repeat a non-idempotent operation or restart a Harness run.
type Error struct {
	StatusCode int
	Type       string
	Message    string
	Retryable  bool
	Cause      error
}

func (err *Error) Error() string {
	if err.Type == "" {
		return err.Message
	}
	return err.Type + ": " + err.Message
}

func (err *Error) Unwrap() error { return err.Cause }

var (
	ErrCallConflict            = &Error{StatusCode: http.StatusConflict, Message: "Harness call conflicts with an existing submission"}
	ErrRegistrationKeyRequired = &Error{Message: "registration key is required"}
	ErrSpeakStatusInvalid      = &Error{Message: "Speak requires a terminal message status; use Tell for streaming"}
	ErrTellStatusInvalid       = &Error{Message: "Tell starts a streaming message; pass terminal status to End"}
	ErrMessageEnded            = &Error{Message: "message has ended"}
	ErrInvalidEmit             = &Error{Message: "Emit requires set or append; use End to finish sending"}
	ErrEndStatusCount          = &Error{Message: "End accepts at most one status"}
	ErrEndStatusInvalid        = &Error{Message: "End requires a terminal message status"}
	ErrEndStatusConflict       = &Error{Message: "message already ended with a different status"}
	ErrPendingMessageUpdate    = &Error{Message: "previous message update is unresolved; retry it before sending another update"}
)

func IsConflict(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.StatusCode == http.StatusConflict
}

// IsHarnessCapacityExceeded reports admission rejection; no new Run was accepted.
func IsHarnessCapacityExceeded(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.StatusCode == http.StatusTooManyRequests && target.Type == "harness_capacity_exceeded"
}

func IsRetryable(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.Retryable
}

// Unknown failures remain non-retryable until a concrete case warrants a
// different policy. Preserve original errors for errors.Is/As.
func WrapError(err error) error {
	if err == nil {
		return nil
	}
	var target *Error
	if errors.As(err, &target) {
		return err
	}
	return &Error{Message: err.Error(), Cause: err}
}
