package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/compforge/loopd/runtime/infra"
	"github.com/compforge/loopd/runtime/model"
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
		{"unknown", model.WrapError(unknown), unknown, false},
		{"cancelled", infra.TransportError(context.Canceled), context.Canceled, false},
		{"disconnect", infra.TransportError(io.ErrUnexpectedEOF), io.ErrUnexpectedEOF, true},
		{"wrapped", fmt.Errorf("read call: %w", infra.TransportError(io.EOF)), io.EOF, true},
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
		err := infra.DecodeResponseError(response)
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
