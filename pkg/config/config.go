package config

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/lock"
)

const (
	KeyClusterUUID = "cluster_uuid"
	KeySecretKey   = "secret_key"

	lockKeyPrefix = "config_"

	lockTTL = 5 * time.Minute
)

// ErrConfigNotFound is returned if no config with this key was found.
var ErrConfigNotFound = errors.New("no config was found for this key")

type Config struct {
	db       database.Querier
	rwLocker lock.RWLocker
}

func New(db database.Querier, rwLocker lock.RWLocker) *Config {
	return &Config{
		db:       db,
		rwLocker: rwLocker,
	}
}

func (c *Config) GetClusterUUID(ctx context.Context) (string, error) {
	return c.getConfig(ctx, KeyClusterUUID)
}

func (c *Config) SetClusterUUID(ctx context.Context, value string) error {
	return c.setConfig(ctx, KeyClusterUUID, value)
}

func (c *Config) GetSecretKey(ctx context.Context) (string, error) {
	return c.getConfig(ctx, KeySecretKey)
}

func (c *Config) SetSecretKey(ctx context.Context, value string) error {
	return c.setConfig(ctx, KeySecretKey, value)
}

func (c *Config) getConfig(ctx context.Context, key string) (string, error) {
	lockKey := getLockKey(key)

	if err := c.rwLocker.RLock(ctx, lockKey, lockTTL); err != nil {
		zerolog.Ctx(ctx).Error().
			Err(err).
			Str("key", key).
			Msg("failed to acquire read lock")

		return "", fmt.Errorf("failed to acquire read lock: %w", err)
	}

	defer func() {
		if err := c.rwLocker.RUnlock(ctx, lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Str("key", key).
				Msg("failed to release read lock")
		}
	}()

	cu, err := c.db.GetConfigByKey(ctx, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: %s", ErrConfigNotFound, key)
		}

		return "", err
	}

	return cu.Value, nil
}

func (c *Config) setConfig(ctx context.Context, key, value string) error {
	lockKey := getLockKey(key)

	if err := c.rwLocker.Lock(ctx, lockKey, lockTTL); err != nil {
		zerolog.Ctx(ctx).Error().
			Err(err).
			Str("key", key).
			Msg("failed to acquire write lock")

		return fmt.Errorf("failed to acquire write lock: %w", err)
	}

	defer func() {
		if err := c.rwLocker.Unlock(ctx, lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Str("key", key).
				Msg("failed to release write lock")
		}
	}()

	return c.db.SetConfig(ctx, database.SetConfigParams{
		Key:   key,
		Value: value,
	})
}

func getLockKey(key string) string {
	return lockKeyPrefix + key
}
