package cmd

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/inconshreveable/log15/v3"
	"github.com/robfig/cron/v3"
	"github.com/urfave/cli/v3"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/server"
)

func serveCommand(logger log15.Logger) *cli.Command {
	return &cli.Command{
		Name:    "serve",
		Aliases: []string{"s"},
		Usage:   "serve the nix binary cache over http",
		Action:  serveAction(logger.New("cmd", "serve")),
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
				Name:    "cache-max-size",
				Usage:   "The maximum size of the store. It can be given with units such as 5K, 10G etc. Supported units: B, K, M, G, T",
				Sources: cli.EnvVars("CACHE_MAX_SIZE"),
			},
			&cli.StringFlag{
				Name:    "cache-lru-cron-spec",
				Usage:   "The cron spec for cleaning the store. Refer to https://pkg.go.dev/github.com/robfig/cron/v3#hdr-Usage for documentation",
				Sources: cli.EnvVars("LRU_CRON_SPEC"),
			},
			&cli.StringFlag{
				Name:    "cache-lru-cron-timezone",
				Usage:   "The name of the timezone to use for the cron",
				Sources: cli.EnvVars("LRU_CRON_TZ"),
			},
			&cli.StringFlag{
				Name:    "server-addr",
				Usage:   "The address of the server",
				Sources: cli.EnvVars("SERVER_ADDR"),
				Value:   ":8501",
			},
			&cli.StringSliceFlag{
				Name:     "upstream-cache",
				Usage:    "Set to host for each upstream cache",
				Sources:  cli.EnvVars("UPSTREAM_CACHES"),
				Required: true,
			},
			&cli.StringSliceFlag{
				Name:     "upstream-public-key",
				Usage:    "Set to host:public-key for each upstream cache",
				Sources:  cli.EnvVars("UPSTREAM_PUBLIC_KEYS"),
				Required: true,
			},
		},
	}
}

func serveAction(logger log15.Logger) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		ucs, err := getUpstreamCaches(ctx, logger, cmd)
		if err != nil {
			return fmt.Errorf("error computing the upstream caches: %w", err)
		}

		cache, err := createCache(logger, cmd, ucs)
		if err != nil {
			return err
		}

		srv := server.New(logger, cache)
		srv.SetDeletePermitted(cmd.Bool("allow-delete"))
		srv.SetPutPermitted(cmd.Bool("allow-put"))

		server := &http.Server{
			Addr:              cmd.String("server-addr"),
			Handler:           srv,
			ReadHeaderTimeout: 10 * time.Second,
		}

		logger.Info("Server started", "server-addr", cmd.String("server-addr"))

		if err := server.ListenAndServe(); err != nil {
			return fmt.Errorf("error starting the HTTP listener: %w", err)
		}

		return nil
	}
}

func getUpstreamCaches(_ context.Context, logger log15.Logger, cmd *cli.Command) ([]upstream.Cache, error) {
	ucSlice := cmd.StringSlice("upstream-cache")

	ucs := make([]upstream.Cache, 0, len(ucSlice))

	for _, host := range ucSlice {
		var pubKeys []string

		rx := regexp.MustCompile(fmt.Sprintf(`^%s-[0-9]+:[A-Za-z0-9+/=]+$`, regexp.QuoteMeta(host)))

		for _, pubKey := range cmd.StringSlice("upstream-public-key") {
			if rx.MatchString(pubKey) {
				pubKeys = append(pubKeys, pubKey)
			}
		}

		uc, err := upstream.New(logger, host, pubKeys)
		if err != nil {
			return nil, fmt.Errorf("error creating a new upstream cache: %w", err)
		}

		ucs = append(ucs, uc)
	}

	return ucs, nil
}

func createCache(logger log15.Logger, cmd *cli.Command, ucs []upstream.Cache) (*cache.Cache, error) {
	c, err := cache.New(logger, cmd.String("cache-hostname"), cmd.String("cache-data-path"))
	if err != nil {
		return nil, fmt.Errorf("error creating a new cache: %w", err)
	}

	c.AddUpstreamCaches(ucs...)

	if cmd.String("cache-lru-cron-spec") == "" {
		return c, nil
	}

	if cmd.String("cache-max-size") == "" {
		return nil, fmt.Errorf("--cache-max-size is required when --cache-lru-cron-spec is specified")
	}

	size, err := helper.ParseSize(cmd.String("cache-max-size"))
	if err != nil {
		return nil, fmt.Errorf("error parsing the size: %w", err)
	}

	c.SetMaxSize(size)

	var loc *time.Location

	if cronTimezone := cmd.String("cache-lru-cron-timezone"); cronTimezone != "" {
		loc, err = time.LoadLocation(cronTimezone)
		if err != nil {
			return nil, fmt.Errorf("error parsing the timezone %q: %w", cronTimezone, err)
		}
	}

	c.SetupCron(loc)

	schedule, err := cron.ParseStandard(cmd.String("cache-lru-cron-spec"))
	if err != nil {
		return nil, fmt.Errorf("error parsing the cron spec %q: %w", err)
	}

	c.AddLRUCronJob(schedule)

	return c, nil
}
