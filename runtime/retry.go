package runtime

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

// write retries only identity-bearing publications. Ordinary non-idempotent
// requests must not use it. All attempts share one request deadline.
func (c *client) write(ctx context.Context, path string, input, output any) error {
	ctx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	for attempt := 0; ; attempt++ {
		err := c.do(ctx, http.MethodPost, path, input, output)
		if err == nil || attempt == 2 || ctx.Err() != nil || !transientWrite(err) {
			return err
		}
		c.logger.WarnContext(ctx, "retry message publication", "path", path, "attempt", attempt+1)
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func transientWrite(err error) bool {
	var response *Error
	if errors.As(err, &response) {
		return response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
	}
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
