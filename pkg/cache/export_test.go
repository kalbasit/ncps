package cache

import "context"

func (c *Cache) GenerateIndexForTest(ctx context.Context) error {
	return c.doGenerateIndex(ctx)
}
