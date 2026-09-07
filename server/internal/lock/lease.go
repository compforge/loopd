// Package lock coordinates Server components using renewable resource leases.
package lock

import (
	"context"
	"errors"
	"time"
)

var ErrLocked = errors.New("resource is locked")
var ErrLost = errors.New("resource lease lost")

type Token struct {
	Resource, LockerID string
	TTL                time.Duration
}
type Locker interface {
	Lock(context.Context, string, time.Duration) (Token, error)
	Renew(context.Context, Token) error
	Unlock(context.Context, Token) error
}

// WithLease does not queue behind another holder. Scanners can try other work.
// The token must also fence persistent writes; context cancellation alone cannot.
func WithLease(ctx context.Context, l Locker, resource string, ttl time.Duration, fn func(context.Context, Token) error) (err error) {
	if ttl <= 0 {
		return errors.New("lease TTL must be positive")
	}
	token, err := l.Lock(ctx, resource, ttl)
	if err != nil {
		return err
	}
	work, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		timer := time.NewTicker(ttl / 3)
		defer timer.Stop()
		for {
			select {
			case <-work.Done():
				done <- nil
				return
			case <-timer.C:
				if e := l.Renew(work, token); e != nil {
					cancel()
					done <- e
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		renewErr := <-done
		release, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		err = errors.Join(err, renewErr, l.Unlock(release, token))
	}()
	return fn(work, token)
}
