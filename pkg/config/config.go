package config

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kalbasit/ncps/pkg/database"
)

const (
	KeyClusterUUID = "cluster_uuid"
)

// ErrNoClusterUUID is returned if no cluster uuid is available in the database.
var ErrNoClusterUUID = errors.New("no cluster uuid is found")

type Config struct {
	db database.Querier
}

func New(db database.Querier) *Config {
	return &Config{db}
}

func (c *Config) GetClusterUUID(ctx context.Context) (string, error) {
	cu, err := c.db.GetConfigByKey(ctx, KeyClusterUUID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNoClusterUUID
		}

		return "", errors.Join(err, ErrNoClusterUUID)
	}

	return cu.Value, nil
}

func (c *Config) SetClusterUUID(ctx context.Context, cu string) error {
	return c.db.SetConfig(ctx, database.SetConfigParams{
		Key:   KeyClusterUUID,
		Value: cu,
	})
}
