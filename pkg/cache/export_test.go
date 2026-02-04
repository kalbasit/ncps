package cache

import "context"

// CheckAndFixNarInfo is a test-only export of the unexported checkAndFixNarInfo method.
func (c *Cache) CheckAndFixNarInfo(ctx context.Context, hash string) error {
	return c.checkAndFixNarInfo(ctx, hash)
}
