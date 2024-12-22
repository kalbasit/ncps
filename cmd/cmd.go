package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

// Version defines the version of the binary, and is meant to be set with ldflags at build time.
//
//nolint:gochecknoglobals
var Version = "dev"

func New() *cli.Command {
	return &cli.Command{
		Name:    "ncps",
		Usage:   "Nix Binary Cache Proxy Service",
		Version: Version,
		Before:  beforeFunc,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "log-level",
				Usage:   "Set the log level",
				Sources: cli.EnvVars("LOG_LEVEL"),
				Value:   "info",
				Validator: func(lvl string) error {
					_, err := zerolog.ParseLevel(lvl)

					return err
				},
			},
		},
		Commands: []*cli.Command{
			serveCommand(),
		},
	}
}

func beforeFunc(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	logLvl := cmd.String("log-level")

	lvl, err := zerolog.ParseLevel(logLvl)
	if err != nil {
		return ctx, fmt.Errorf("error parsing the log-level %q: %w", logLvl, err)
	}

	var output io.Writer = os.Stdout

	if term.IsTerminal(int(os.Stdout.Fd())) {
		output = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	}

	log := zerolog.New(output).Level(lvl)

	log.Info().Str("log-level", lvl.String()).Msg("logger created")

	return log.WithContext(ctx), nil
}
