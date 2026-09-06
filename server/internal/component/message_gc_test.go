package component

import (
	"context"
	"errors"
	"testing"
	"time"
)

type expiryCall struct {
	cutoff time.Time
	limit  int
}
type expiryRepository struct{ calls chan expiryCall }

func (r *expiryRepository) ExpireMessages(_ context.Context, cutoff time.Time, limit int) ([]string, error) {
	r.calls <- expiryCall{cutoff, limit}
	return nil, errors.New("temporary database failure")
}

// +case=`GC runs with no page listener, retries failed sweeps and stops with the server.`
func TestMessageGCRunsIndependently(t *testing.T) {
	repository := &expiryRepository{calls: make(chan expiryCall, 10)}
	gc := NewMessageGC(repository, 24*time.Hour, 10*time.Millisecond, 100, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); gc.Run(ctx) }()
	for range 2 {
		select {
		case call := <-repository.calls:
			age := time.Since(call.cutoff)
			if call.limit != 100 || age < 24*time.Hour || age > 24*time.Hour+time.Second {
				t.Fatalf("sweep = %+v", call)
			}
		case <-time.After(time.Second):
			t.Fatal("GC did not retry independently")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("GC did not stop")
	}
}
