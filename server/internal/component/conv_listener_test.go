package component

import (
	"context"
	"errors"
	"testing"
	"time"

	runner "github.com/compforge/agentue/sdks/go/runner"
	"github.com/compforge/loopd/server/internal/model"
)

type activeRepository struct{ ConvMessageRepository }

func (activeRepository) LatestMessageID(context.Context, string) (string, error) { return "a", nil }
func (activeRepository) ListStreamMessages(context.Context, string, string, string, int) ([]model.Message, error) {
	return []model.Message{{ID: "a", ConversationID: "conv", Status: "streaming", Revision: 1,
		Content: []byte(`{"version":"1.1","biz":"chat","meta":{},"blocks":[]}`)}}, nil
}

type blockedBridge struct {
	runner.EventBridge
	started, stopped chan struct{}
}

func (b *blockedBridge) State(ctx context.Context, _ string) (runner.State, error) {
	close(b.started)
	<-ctx.Done()
	close(b.stopped)
	return runner.State{}, ctx.Err()
}

// +case=`A blocked Redis reader does not hold up SQL bootstrap/ping and is cancelled when the page leaves.`
func TestConvListenerCancelsItsReaders(t *testing.T) {
	bridge := &blockedBridge{started: make(chan struct{}), stopped: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	disconnected := errors.New("page disconnected")
	err := NewConvListener(bridge, activeRepository{}, "conv").Run(ctx, func(event Event) error {
		if event.MessageID != "" {
			return nil
		}
		select {
		case <-bridge.started:
			return disconnected
		case <-ctx.Done():
			t.Fatal("Redis setup blocked bootstrap")
			return ctx.Err()
		}
	})
	if !errors.Is(err, disconnected) {
		t.Fatalf("listener = %v", err)
	}
	select {
	case <-bridge.stopped:
	case <-ctx.Done():
		t.Fatal("reader survived page disconnect")
	}
}
