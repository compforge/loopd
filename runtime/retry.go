package runtime

import (
	"context"
	"net/http"
	"time"
)

// write retries only identity-bearing publications. Ordinary non-idempotent
// requests must not use it. All attempts share one request deadline.
// Harness capacity rejection is retryable by the Operator, but returned immediately
// here so the SDK does not hide admission backpressure.
func (c *client) write(ctx context.Context, path string, input, output any) error {
	ctx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	for attempt := 0; ; attempt++ {
		err := c.do(ctx, http.MethodPost, path, input, output)
		if err == nil || attempt == 2 || ctx.Err() != nil || !IsRetryable(err) || IsHarnessCapacityExceeded(err) {
			return err
		}
		c.logger.WarnContext(ctx, "retry message publication", "path", path, "attempt", attempt+1)
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return wrapError(ctx.Err())
		case <-timer.C:
		}
	}
}
