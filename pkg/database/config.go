package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

// CreateConfig inserts a new config key-value pair.
func CreateConfig(ctx context.Context, db bun.IDB, key, value string) (Config, error) {
	config := &Config{
		Key:   key,
		Value: value,
	}

	if db.Dialect().Name() == dialect.MySQL {
		_, err := db.NewInsert().Model(config).Exec(ctx)
		if err != nil {
			return Config{}, err
		}

		return GetConfigByKey(ctx, db, key)
	}

	_, err := db.NewInsert().Model(config).Returning("*").Exec(ctx, config)
	if err != nil {
		return Config{}, err
	}

	return *config, nil
}

// GetConfigByKey retrieves a config entry by key.
func GetConfigByKey(ctx context.Context, db bun.IDB, key string) (Config, error) {
	var config Config

	if db.Dialect().Name() == dialect.MySQL {
		err := db.NewRaw("SELECT * FROM config WHERE `key` = ?", key).Scan(ctx, &config)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Config{}, ErrNotFound
			}

			return Config{}, err
		}

		return config, nil
	}

	err := db.NewSelect().Model(&config).Where("key = ?", key).Scan(ctx, &config)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Config{}, ErrNotFound
		}

		return Config{}, err
	}

	return config, nil
}

// GetConfigByID retrieves a config entry by ID.
func GetConfigByID(ctx context.Context, db bun.IDB, id int64) (Config, error) {
	var config Config

	err := db.NewSelect().Model(&config).Where("id = ?", id).Scan(ctx, &config)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Config{}, ErrNotFound
		}

		return Config{}, err
	}

	return config, nil
}

// SetConfig creates or updates a config entry.
//
// UPSERT behavior by engine:
// - PostgreSQL/SQLite: ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
// - MySQL: ON DUPLICATE KEY UPDATE value = VALUES(value).
func SetConfig(ctx context.Context, db bun.IDB, key, value string) error {
	config := &Config{
		Key:   key,
		Value: value,
	}

	if db.Dialect().Name() == dialect.MySQL {
		_, err := db.NewInsert().Model(config).
			On("DUPLICATE KEY UPDATE value = VALUES(value)").
			Exec(ctx)

		return err
	}

	_, err := db.NewInsert().Model(config).
		On("CONFLICT (key) DO UPDATE SET value = EXCLUDED.value").
		Exec(ctx)

	return err
}
