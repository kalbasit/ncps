package upstream

import (
	"context"
	"time"
)

// SetRetryWaitForTest replaces the retry backoff wait so tests can observe the
// requested delays without sleeping.
// This is only for testing purposes.
func (c *Cache) SetRetryWaitForTest(fn func(context.Context, time.Duration) error) {
	c.retryWait = fn
}
