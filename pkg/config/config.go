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
	// KeyClusterUUID is the key for the cluster UUID in the configuration database.
	KeyClusterUUID = "cluster_uuid"
	// KeySecretKey is the key for the secret key in the configuration database.
	KeySecretKey = "secret_key"

	// lockKeyPrefix is the prefix used for locking configuration keys.
	lockKeyPrefix = "config_"

	// lockTTL is the time-to-live for configuration locks.
	lockTTL = 5 * time.Minute
)

// ErrConfigNotFound is returned if no config with this key was found.
var ErrConfigNotFound = errors.New("no config was found for this key")

// Config provides access to the persistent configuration stored in the database.
// It uses an RWLocker to ensure thread-safe access to configuration keys.
type Config struct {
	db       database.Querier
	rwLocker lock.RWLocker
}

// New returns a new Config instance.
func New(db database.Querier, rwLocker lock.RWLocker) *Config {
	return &Config{
		db:       db,
		rwLocker: rwLocker,
	}
}

// GetClusterUUID returns the cluster UUID from the configuration.
func (c *Config) GetClusterUUID(ctx context.Context) (string, error) {
	return c.getConfig(ctx, KeyClusterUUID)
}

// SetClusterUUID stores the cluster UUID in the configuration.
func (c *Config) SetClusterUUID(ctx context.Context, value string) error {
	return c.setConfig(ctx, KeyClusterUUID, value)
}

// GetSecretKey returns the secret key from the configuration.
func (c *Config) GetSecretKey(ctx context.Context) (string, error) {
	return c.getConfig(ctx, KeySecretKey)
}

// SetSecretKey stores the secret key in the configuration.
func (c *Config) SetSecretKey(ctx context.Context, value string) error {
	return c.setConfig(ctx, KeySecretKey, value)
}

// getConfig retrieves a configuration value by key, acquiring a read lock.
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

// setConfig stores a configuration value for the given key, acquiring a write lock.
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

// getLockKey returns the lock key for the specified configuration key.
func getLockKey(key string) string {
	return lockKeyPrefix + key
}
