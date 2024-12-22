package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/kalbasit/ncps/pkg/storage/local"
)

// ErrCacheMaxSizeRequired is returned if --cache-lru-schedule was given but not --cache-max-size.
var ErrCacheMaxSizeRequired = errors.New("--cache-max-size is required when --cache-lru-schedule is specified")

func serveCommand() *cli.Command {
	return &cli.Command{
		Name:    "serve",
		Aliases: []string{"s"},
		Usage:   "serve the nix binary cache over http",
		Action:  serveAction(),
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "allow-delete",
				Usage:   "Whether to allow the DELETE verb to delete narInfo and nar files",
				Sources: cli.EnvVars("ALLOW_DELETE_VERB"),
			},
			&cli.BoolFlag{
				Name:    "allow-put",
				Usage:   "Whether to allow the PUT verb to push narInfo and nar files directly",
				Sources: cli.EnvVars("ALLOW_PUT_VERB"),
			},
			&cli.StringFlag{
				Name:     "cache-hostname",
				Usage:    "The hostname of the cache server",
				Sources:  cli.EnvVars("CACHE_HOSTNAME"),
				Required: true,
			},
			&cli.StringFlag{
				Name:     "cache-data-path",
				Usage:    "The local data path used for configuration and cache storage",
				Sources:  cli.EnvVars("CACHE_DATA_PATH"),
				Required: true,
			},
			&cli.StringFlag{
				Name:     "cache-database-url",
				Usage:    "The URL of the database",
				Sources:  cli.EnvVars("CACHE_DATABASE_URL"),
				Required: true,
			},
			&cli.StringFlag{
				Name: "cache-max-size",
				//nolint:lll
				Usage:   "The maximum size of the store. It can be given with units such as 5K, 10G etc. Supported units: B, K, M, G, T",
				Sources: cli.EnvVars("CACHE_MAX_SIZE"),
				Validator: func(s string) error {
					_, err := helper.ParseSize(s)

					return err
				},
			},
			&cli.StringFlag{
				Name: "cache-lru-schedule",
				//nolint:lll
				Usage:   "The cron spec for cleaning the store. Refer to https://pkg.go.dev/github.com/robfig/cron/v3#hdr-Usage for documentation",
				Sources: cli.EnvVars("CACHE_LRU_SCHEDULE"),
				Validator: func(s string) error {
					_, err := cron.ParseStandard(s)

					return err
				},
			},
			&cli.StringFlag{
				Name:    "cache-lru-schedule-timezone",
				Usage:   "The name of the timezone to use for the cron",
				Sources: cli.EnvVars("CACHE_LRU_SCHEDULE_TZ"),
				Value:   "Local",
			},
			&cli.StringFlag{
				Name:    "server-addr",
				Usage:   "The address of the server",
				Sources: cli.EnvVars("SERVER_ADDR"),
				Value:   ":8501",
			},
			&cli.StringSliceFlag{
				Name:     "upstream-cache",
				Usage:    "Set to URL (with scheme) for each upstream cache",
				Sources:  cli.EnvVars("UPSTREAM_CACHES"),
				Required: true,
			},
			&cli.StringSliceFlag{
				Name:    "upstream-public-key",
				Usage:   "Set to host:public-key for each upstream cache",
				Sources: cli.EnvVars("UPSTREAM_PUBLIC_KEYS"),
			},
		},
	}
}

func serveAction() cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		logger := zerolog.Ctx(ctx).With().Str("cmd", "serve").Logger()

		ctx = logger.WithContext(ctx)

		ctx, cancel := context.WithCancel(ctx)

		g, ctx := errgroup.WithContext(ctx)
		defer func() {
			if err := g.Wait(); err != nil {
				logger.Error().Err(err).Msg("error returned from g.Wait()")
			}
		}()

		// NOTE: Reminder that defer statements run last to first so the first
		// thing that happens here is the context is canceled which triggers the
		// errgroup 'g' to start exiting.
		defer cancel()

		g.Go(func() error {
			return autoMaxProcs(ctx, 30*time.Second, logger)
		})

		ucs, err := getUpstreamCaches(ctx, logger, cmd)
		if err != nil {
			return fmt.Errorf("error computing the upstream caches: %w", err)
		}

		cache, err := createCache(ctx, cmd, ucs)
		if err != nil {
			return err
		}

		srv := server.New(cache)
		srv.SetDeletePermitted(cmd.Bool("allow-delete"))
		srv.SetPutPermitted(cmd.Bool("allow-put"))

		server := &http.Server{
			BaseContext:       func(net.Listener) context.Context { return ctx },
			Addr:              cmd.String("server-addr"),
			Handler:           srv,
			ReadHeaderTimeout: 10 * time.Second,
		}

		logger.Info().
			Str("server-addr", cmd.String("server-addr")).
			Msg("Server started")

		if err := server.ListenAndServe(); err != nil {
			return fmt.Errorf("error starting the HTTP listener: %w", err)
		}

		return nil
	}
}

func getUpstreamCaches(_ context.Context, logger zerolog.Logger, cmd *cli.Command) ([]upstream.Cache, error) {
	ucSlice := cmd.StringSlice("upstream-cache")

	ucs := make([]upstream.Cache, 0, len(ucSlice))

	for _, us := range ucSlice {
		var pubKeys []string

		u, err := url.Parse(us)
		if err != nil {
			return nil, fmt.Errorf("error parsing --upstream-cache=%q: %w", us, err)
		}

		rx := regexp.MustCompile(fmt.Sprintf(`^%s-[0-9]+:[A-Za-z0-9+/=]+$`, regexp.QuoteMeta(u.Host)))

		for _, pubKey := range cmd.StringSlice("upstream-public-key") {
			if rx.MatchString(pubKey) {
				pubKeys = append(pubKeys, pubKey)
			}
		}

		uc, err := upstream.New(logger, u, pubKeys)
		if err != nil {
			return nil, fmt.Errorf("error creating a new upstream cache: %w", err)
		}

		ucs = append(ucs, uc)
	}

	return ucs, nil
}

func createCache(
	ctx context.Context,
	cmd *cli.Command,
	ucs []upstream.Cache,
) (*cache.Cache, error) {
	dbURL := cmd.String("cache-database-url")

	db, err := database.Open(dbURL)
	if err != nil {
		return nil, fmt.Errorf("error opening the database %q: %w", dbURL, err)
	}

	cacheDataPath := cmd.String("cache-data-path")

	localStore, err := local.New(ctx, cacheDataPath)
	if err != nil {
		return nil, fmt.Errorf("error creating a new local store at %q: %w", cacheDataPath, err)
	}

	c, err := cache.New(
		ctx,
		cmd.String("cache-hostname"),
		db,
		localStore,
		localStore,
		localStore,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating a new cache: %w", err)
	}

	c.AddUpstreamCaches(ctx, ucs...)

	if cmd.String("cache-lru-schedule") == "" {
		return c, nil
	}

	maxSizeStr := cmd.String("cache-max-size")
	if maxSizeStr == "" {
		return nil, ErrCacheMaxSizeRequired
	}

	maxSize, err := helper.ParseSize(maxSizeStr)
	if err != nil {
		return nil, fmt.Errorf("error parsing the size: %w", err)
	}

	zerolog.Ctx(ctx).
		Info().
		Uint64("max-size", maxSize).
		Msg("setting up the cache max-size")

	c.SetMaxSize(maxSize)

	var loc *time.Location

	if cronTimezone := cmd.String("cache-lru-schedule-timezone"); cronTimezone != "" {
		loc, err = time.LoadLocation(cronTimezone)
		if err != nil {
			return nil, fmt.Errorf("error parsing the timezone %q: %w", cronTimezone, err)
		}
	}

	zerolog.Ctx(ctx).
		Info().
		Str("time-zone", loc.String()).
		Msg("setting up the cache timezone location")

	c.SetupCron(ctx, loc)

	schedule, err := cron.ParseStandard(cmd.String("cache-lru-schedule"))
	if err != nil {
		return nil, fmt.Errorf("error parsing the cron spec %q: %w", cmd.String("cache-lru-schedule"), err)
	}

	c.AddLRUCronJob(ctx, schedule)

	c.StartCron(ctx)

	return c, nil
}
