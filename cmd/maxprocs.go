package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"go.uber.org/automaxprocs/maxprocs"
)

// Inspired from:
// https://github.com/elastic/apm-server/blob/d1b93c984d4cc1a214afaef95fbf06b854d6f1f1/internal/beatcmd/maxprocs.go#L63

// autoMaxProcs automatically configures Go's runtime.GOMAXPROCS based on the
// given quota in a container.
func autoMaxProcs(ctx context.Context, d time.Duration, logger zerolog.Logger) error {
	log := logger.With().Str("operation", "auto-max-procs").Logger()

	infof := diffInfof(log)
	setMaxProcs := func() {
		if _, err := maxprocs.Set(maxprocs.Logger(infof)); err != nil {
			log.Error().Err(err).Msg("failed to set GOMAXPROCS")
		}
	}
	// set the gomaxprocs immediately.
	setMaxProcs()

	ticker := time.NewTicker(d)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			setMaxProcs()
		}
	}
}

func diffInfof(logger zerolog.Logger) func(string, ...interface{}) {
	var last string

	return func(format string, args ...interface{}) {
		msg := fmt.Sprintf(format, args...)
		if msg != last {
			logger.Info().Msg(msg)
			last = msg
		}
	}
}
