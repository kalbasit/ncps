package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"

	"github.com/inconshreveable/log15/v3"
	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/kalbasit/ncps/pkg/upstreamcache"
	"github.com/urfave/cli/v3"
)

var serveCommand = &cli.Command{
	Name:    "serve",
	Aliases: []string{"s"},
	Usage:   "serve the nix binary cache over http",
	Action:  serveAction,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "cache-hostname",
			Usage: "The hostname of the cache server",
			// EnvVars:  []string{"CACHE_HOSTNAME"},
			Required: true,
		},
		&cli.StringFlag{
			Name:  "cache-path",
			Usage: "The local path for cache storage",
			// EnvVars:  []string{"CACHE_PATH"},
			Required: true,
		},
		&cli.StringFlag{
			Name:  "cache-secret-key",
			Usage: "The secret key of the cache server",
			// EnvVars:  []string{"CACHE_SECRET_KEY"},
			Required: true,
		},
		&cli.StringFlag{
			Name:  "server-addr",
			Usage: "The address of the server",
			// EnvVars: []string{"SERVER_ADDR"},
			Value: ":8501",
		},
		&cli.StringSliceFlag{
			Name:  "upstream-cache",
			Usage: "Set to host for each upstream cache",
			// EnvVars:  []string{"UPSTREAM_CACHES"},
			Required: true,
		},
		&cli.StringSliceFlag{
			Name:  "upstream-public-key",
			Usage: "Set to host:public-key for each upstream cache",
			// EnvVars:  []string{"UPSTREAM_PUBLIC_KEYS"},
			Required: true,
		},
	},
}

func serveAction(ctx context.Context, cmd *cli.Command) error {
	logger := log15.New()
	logger.SetHandler(log15.StreamHandler(os.Stdout, log15.JsonFormat()))

	ucs, err := getUpstreamCaches(ctx, cmd)
	if err != nil {
		return fmt.Errorf("error computing the upstream caches: %w", err)
	}

	cache, err := cache.New(cmd.String("cache-hostname"), cmd.String("cache-path"), cmd.String("cache-secret-key"))
	if err != nil {
		return fmt.Errorf("error creating a new cache: %w", err)
	}

	srv, err := server.New(logger, cache, ucs)
	if err != nil {
		return fmt.Errorf("error creating a new server: %w", err)
	}

	logger.Info("Server started", "server-addr", cmd.String("server-addr"))
	http.ListenAndServe(cmd.String("server-addr"), srv)

	return nil
}

func getUpstreamCaches(ctx context.Context, cmd *cli.Command) ([]upstreamcache.UpstreamCache, error) {
	var ucs []upstreamcache.UpstreamCache

	for _, host := range cmd.StringSlice("upstream-cache") {
		var pubKeys []string
		rx := regexp.MustCompile(fmt.Sprintf(`^%s-[0-9]+:[A-Za-z0-9+/=]+$`, regexp.QuoteMeta(host)))
		for _, pubKey := range cmd.StringSlice("upstream-public-key") {
			if rx.MatchString(pubKey) {
				pubKeys = append(pubKeys, pubKey)
			}
		}

		uc, err := upstreamcache.New(host, pubKeys)
		if err != nil {
			return nil, fmt.Errorf("error creating a new upstream cache: %w", err)
		}

		ucs = append(ucs, uc)
	}

	return ucs, nil

	return nil, nil
}
