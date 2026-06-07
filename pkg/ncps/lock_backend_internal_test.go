package ncps

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// TestDetermineEffectiveLockBackend pins the lock-backend resolution matrix that
// drives in-flight staging: staging is only distributed (and therefore active)
// when the effective backend resolves to Redis. This covers the
// backward-compatibility path where legacy --cache-redis-addrs implies Redis even
// when --cache-lock-backend is left at its "local" default.
func TestDetermineEffectiveLockBackend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		args            []string
		wantBackend     string
		wantStagingDist bool
	}{
		{
			name:            "default is local, staging not distributed",
			args:            []string{"app"},
			wantBackend:     lockBackendLocal,
			wantStagingDist: false,
		},
		{
			name:            "explicit redis backend, staging distributed",
			args:            []string{"app", "--cache-lock-backend", lockBackendRedis},
			wantBackend:     lockBackendRedis,
			wantStagingDist: true,
		},
		{
			name:            "legacy redis-addrs falls back to redis",
			args:            []string{"app", "--cache-redis-addrs", "127.0.0.1:6379"},
			wantBackend:     lockBackendRedis,
			wantStagingDist: true,
		},
		{
			name:            "empty redis-addrs stays local",
			args:            []string{"app", "--cache-redis-addrs", ""},
			wantBackend:     lockBackendLocal,
			wantStagingDist: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				gotBackend     string
				gotStagingDist bool
			)

			cmd := &cli.Command{
				Name: "app",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "cache-lock-backend", Value: lockBackendLocal},
					&cli.StringSliceFlag{Name: "cache-redis-addrs"},
				},
				Action: func(_ context.Context, c *cli.Command) error {
					gotBackend, _ = determineEffectiveLockBackend(c)
					gotStagingDist = gotBackend == lockBackendRedis

					return nil
				},
			}

			require.NoError(t, cmd.Run(context.Background(), tt.args))
			assert.Equal(t, tt.wantBackend, gotBackend)
			assert.Equal(t, tt.wantStagingDist, gotStagingDist)
		})
	}
}
