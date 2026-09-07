package infra

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/runtime/model"
)

func TestReadEvents(t *testing.T) {
	for _, test := range []struct {
		name       string
		input      string
		wantEvents int
		wantError  bool
		retryable  bool
	}{
		{"end", "data: {\"seq\":1,\"op\":\"end\"}\n\n", 1, false, false},
		{"multiline", ": heartbeat\n\ndata: {\"seq\":1,\ndata: \"op\":\"end\"}\n\n", 1, false, false},
		{"disconnect", "data: {\"seq\":1,\"op\":\"end\"}", 0, true, true},
		{"invalid", "data: not-json\n\n", 0, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := make(chan ui.Event, 2)
			err := ReadEvents(context.Background(), strings.NewReader(test.input), events)
			if (err != nil) != test.wantError || model.IsRetryable(err) != test.retryable || len(events) != test.wantEvents {
				t.Fatalf("events=%d error=%v", len(events), err)
			}
			if test.retryable && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("lost disconnect cause: %v", err)
			}
		})
	}
}

func TestReadEventsCancelsBlockedConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := ReadEvents(ctx, strings.NewReader("data: {\"seq\":1,\"op\":\"end\"}\n\n"), make(chan ui.Event))
	if !errors.Is(err, context.Canceled) || model.IsRetryable(err) {
		t.Fatalf("cancel error=%v", err)
	}
}
