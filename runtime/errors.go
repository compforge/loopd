package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
	errRegistrationKeyRequired = &Error{Message: "registration key is required"}
	errSpeakStatusInvalid      = &Error{Message: "Speak requires a terminal message status; use Tell for streaming"}
	errTellStatusInvalid       = &Error{Message: "Tell starts a streaming message; pass terminal status to End"}
	errMessageEnded            = &Error{Message: "message has ended"}
	errInvalidEmit             = &Error{Message: "Emit requires set or append; use End to finish sending"}
	errEndStatusCount          = &Error{Message: "End accepts at most one status"}
	errEndStatusInvalid        = &Error{Message: "End requires a terminal message status"}
	errEndStatusConflict       = &Error{Message: "message already ended with a different status"}
	errPendingMessageUpdate    = &Error{Message: "previous message update is unresolved; retry it before sending another update"}
	errHarnessStreamIncomplete = &Error{Message: "Harness stream ended before durable terminal state", Retryable: true}
)

func invalidServerURLError(baseURL string) error {
	return &Error{Message: fmt.Sprintf("invalid loop-server URL %q", baseURL)}
}
func harnessFailure(message string) error { return &Error{Message: message} }
func harnessConflict(cause error) error   { return errors.Join(ErrCallConflict, cause) }

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
func wrapError(err error) error {
	if err == nil {
		return nil
	}
	var target *Error
	if errors.As(err, &target) {
		return err
	}
	return &Error{Message: err.Error(), Cause: err}
}

func transportError(err error) error {
	var network net.Error
	retryable := !errors.Is(err, context.Canceled) &&
		(errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF))
	return &Error{Message: err.Error(), Cause: err, Retryable: retryable}
}

func decodeResponseError(response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	err := &Error{StatusCode: response.StatusCode, Message: response.Status,
		Retryable: response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500}
	var envelope errorResponse
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope) == nil && envelope.Error.Message != "" {
		err.Type, err.Message = envelope.Error.Type, envelope.Error.Message
	}
	return err
}
