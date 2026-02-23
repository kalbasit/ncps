package config

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// KeyCDCEnabled is the key for CDC enabled flag in the configuration database.
	KeyCDCEnabled = "cdc_enabled"
	// KeyCDCMin is the key for CDC minimum chunk size in the configuration database.
	KeyCDCMin = "cdc_min"
	// KeyCDCAvg is the key for CDC average chunk size in the configuration database.
	KeyCDCAvg = "cdc_avg"
	// KeyCDCMax is the key for CDC maximum chunk size in the configuration database.
	KeyCDCMax = "cdc_max"

	// lockKeyPrefix is the prefix used for locking configuration keys.
	lockKeyPrefix = "config_"

	// lockTTL is the time-to-live for configuration locks.
	lockTTL = 5 * time.Minute
)

var (
	// ErrConfigNotFound is returned if no config with this key was found.
	ErrConfigNotFound = errors.New("no config was found for this key")
	// ErrCDCDisabledAfterEnabled is returned when attempting to disable CDC after being enabled.
	ErrCDCDisabledAfterEnabled = errors.New(
		"CDC cannot be disabled after being enabled; existing chunked NARs would not be reconstructed",
	)
	// ErrCDCConfigMismatch is returned when CDC configuration values differ from stored values.
	ErrCDCConfigMismatch = errors.New(
		"CDC config changed; different chunk sizes create new chunks without reusing old ones, causing storage duplication",
	)
)

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

// GetCDCEnabled returns the CDC enabled flag from the configuration.
func (c *Config) GetCDCEnabled(ctx context.Context) (string, error) {
	return c.getConfig(ctx, KeyCDCEnabled)
}

// SetCDCEnabled stores the CDC enabled flag in the configuration.
func (c *Config) SetCDCEnabled(ctx context.Context, value string) error {
	return c.setConfig(ctx, KeyCDCEnabled, value)
}

// GetCDCMin returns the CDC minimum chunk size from the configuration.
func (c *Config) GetCDCMin(ctx context.Context) (string, error) {
	return c.getConfig(ctx, KeyCDCMin)
}

// SetCDCMin stores the CDC minimum chunk size in the configuration.
func (c *Config) SetCDCMin(ctx context.Context, value string) error {
	return c.setConfig(ctx, KeyCDCMin, value)
}

// GetCDCAvg returns the CDC average chunk size from the configuration.
func (c *Config) GetCDCAvg(ctx context.Context) (string, error) {
	return c.getConfig(ctx, KeyCDCAvg)
}

// SetCDCAvg stores the CDC average chunk size in the configuration.
func (c *Config) SetCDCAvg(ctx context.Context, value string) error {
	return c.setConfig(ctx, KeyCDCAvg, value)
}

// GetCDCMax returns the CDC maximum chunk size from the configuration.
func (c *Config) GetCDCMax(ctx context.Context) (string, error) {
	return c.getConfig(ctx, KeyCDCMax)
}

// SetCDCMax stores the CDC maximum chunk size in the configuration.
func (c *Config) SetCDCMax(ctx context.Context, value string) error {
	return c.setConfig(ctx, KeyCDCMax, value)
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
		if database.IsNotFoundError(err) {
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

// ValidateOrStoreCDCConfig validates CDC configuration against stored values or stores them if not yet configured.
// If CDC is disabled and no config exists, returns nil (no-op).
// If CDC is enabled and no config exists, stores all 4 values.
// If config exists, validates that enabled flag matches and all values are identical.
// If CDC was previously enabled (config exists) but now disabled, returns an error.
func (c *Config) ValidateOrStoreCDCConfig(
	ctx context.Context,
	enabled bool,
	minSize, avgSize, maxSize uint32,
) error {
	// Try to get the stored CDC enabled flag
	storedEnabledStr, err := c.GetCDCEnabled(ctx)
	if err != nil {
		if errors.Is(err, ErrConfigNotFound) {
			// First boot - no CDC config in DB yet
			if !enabled {
				// CDC is disabled and never used before - nothing to store
				return nil
			}

			return c.storeCDCConfig(ctx, enabled, minSize, avgSize, maxSize)
		}

		return fmt.Errorf("failed to get CDC enabled config: %w", err)
	}

	// CDC config exists in DB
	return c.validateCDCConfig(ctx, enabled, minSize, avgSize, maxSize, storedEnabledStr)
}

// storeCDCConfig stores all 4 CDC configuration values in the database.
func (c *Config) storeCDCConfig(
	ctx context.Context,
	_ bool,
	minSize, avgSize, maxSize uint32,
) error {
	minStr := fmt.Sprintf("%d", minSize)
	avgStr := fmt.Sprintf("%d", avgSize)
	maxStr := fmt.Sprintf("%d", maxSize)

	if err := c.SetCDCEnabled(ctx, "true"); err != nil {
		return fmt.Errorf("failed to store CDC enabled flag: %w", err)
	}

	if err := c.SetCDCMin(ctx, minStr); err != nil {
		return fmt.Errorf("failed to store CDC min: %w", err)
	}

	if err := c.SetCDCAvg(ctx, avgStr); err != nil {
		return fmt.Errorf("failed to store CDC avg: %w", err)
	}

	if err := c.SetCDCMax(ctx, maxStr); err != nil {
		return fmt.Errorf("failed to store CDC max: %w", err)
	}

	return nil
}

// validateCDCConfig validates that the current CDC configuration matches stored values.
func (c *Config) validateCDCConfig(
	ctx context.Context,
	enabled bool,
	minSize, avgSize, maxSize uint32,
	storedEnabledStr string,
) error {
	storedEnabled := storedEnabledStr == "true"

	// Check if user is trying to disable CDC after it was previously enabled
	if storedEnabled && !enabled {
		return fmt.Errorf(
			"%w; stored cdc_enabled=%s, current cdc_enabled=%v",
			ErrCDCDisabledAfterEnabled,
			storedEnabledStr, enabled,
		)
	}

	// Get stored values for comparison
	storedMinStr, err := c.GetCDCMin(ctx)
	if err != nil {
		return fmt.Errorf("failed to get stored CDC min: %w", err)
	}

	storedAvgStr, err := c.GetCDCAvg(ctx)
	if err != nil {
		return fmt.Errorf("failed to get stored CDC avg: %w", err)
	}

	storedMaxStr, err := c.GetCDCMax(ctx)
	if err != nil {
		return fmt.Errorf("failed to get stored CDC max: %w", err)
	}

	// Compare current values with stored values
	minStr := fmt.Sprintf("%d", minSize)
	avgStr := fmt.Sprintf("%d", avgSize)
	maxStr := fmt.Sprintf("%d", maxSize)

	var mismatches []string

	if fmt.Sprintf("%v", enabled) != storedEnabledStr {
		mismatches = append(mismatches, fmt.Sprintf(
			"cdc_enabled: stored=%s, current=%v",
			storedEnabledStr, enabled,
		))
	}

	if minStr != storedMinStr {
		mismatches = append(mismatches, fmt.Sprintf(
			"cdc_min: stored=%s, current=%s",
			storedMinStr, minStr,
		))
	}

	if avgStr != storedAvgStr {
		mismatches = append(mismatches, fmt.Sprintf(
			"cdc_avg: stored=%s, current=%s",
			storedAvgStr, avgStr,
		))
	}

	if maxStr != storedMaxStr {
		mismatches = append(mismatches, fmt.Sprintf(
			"cdc_max: stored=%s, current=%s",
			storedMaxStr, maxStr,
		))
	}

	if len(mismatches) > 0 {
		return fmt.Errorf(
			"%w: %s",
			ErrCDCConfigMismatch,
			strings.Join(mismatches, "; "),
		)
	}

	return nil
}

// getLockKey returns the lock key for the specified configuration key.
func getLockKey(key string) string {
	return lockKeyPrefix + key
}
