package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRuntimeErrorRetryHint(t *testing.T) {
	unknown := errors.New("unknown failure")
	for _, test := range []struct {
		name      string
		err       error
		cause     error
		retryable bool
	}{
		{"default", &Error{Message: "invalid request"}, nil, false},
		{"unknown", wrapError(unknown), unknown, false},
		{"cancelled", transportError(context.Canceled), context.Canceled, false},
		{"disconnect", transportError(io.ErrUnexpectedEOF), io.ErrUnexpectedEOF, true},
		{"wrapped", fmt.Errorf("read call: %w", transportError(io.EOF)), io.EOF, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value *Error
			if !errors.As(test.err, &value) || IsRetryable(test.err) != test.retryable {
				t.Fatalf("error=%v retryable=%v", test.err, IsRetryable(test.err))
			}
			if test.cause != nil && !errors.Is(test.err, test.cause) {
				t.Fatal("original error was lost")
			}
		})
	}
	for _, status := range []int{400, 401, 403, 409, 429, 500, 503} {
		response := &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(`{"error":{"type":"test","message":"failure"}}`))}
		err := decodeResponseError(response)
		_ = response.Body.Close()
		if IsRetryable(err) != (status == 429 || status >= 500) || IsConflict(err) != (status == 409) {
			t.Fatalf("status=%d error=%v", status, err)
		}
	}
}

func TestRuntimeValidationErrorsDefaultToNoRetry(t *testing.T) {
	rt, _ := New("http://localhost", Options{})
	defer rt.Close()
	_, invalidURL := New(":invalid", Options{})
	_, invalidAsk := rt.Loop.Human.Ask(context.Background(), AskRequest{})
	invalidRegistration := rt.Loop.Harness.Register(context.Background(), HarnessRegistration{})
	for _, err := range []error{invalidURL, invalidAsk, invalidRegistration} {
		var value *Error
		if !errors.As(err, &value) || value.Retryable {
			t.Fatalf("expected non-retryable runtime validation error: %v", err)
		}
	}
}
