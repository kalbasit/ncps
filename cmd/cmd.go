package cmd

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/kalbasit/ncps/pkg/otelzerolog"
)

// Version defines the version of the binary, and is meant to be set with ldflags at build time.
//
//nolint:gochecknoglobals
var Version = "dev"

func New() *cli.Command {
	var otelWriter *otelzerolog.OtelWriter

	return &cli.Command{
		Name:    "ncps",
		Usage:   "Nix Binary Cache Proxy Service",
		Version: Version,
		After: func(ctx context.Context, _ *cli.Command) error {
			if otelWriter != nil {
				return otelWriter.Close(ctx)
			}

			return nil
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			logLvl := cmd.String("log-level")

			lvl, err := zerolog.ParseLevel(logLvl)
			if err != nil {
				return ctx, fmt.Errorf("error parsing the log-level %q: %w", logLvl, err)
			}

			var output io.Writer = os.Stdout

			if colURL := cmd.String("log-otel-grpc-endpoint"); colURL != "" {
				otelWriter, err = otelzerolog.NewOtelWriter(ctx, colURL, "ncps")
				if err != nil {
					return ctx, err
				}

				output = zerolog.MultiLevelWriter(os.Stdout, otelWriter)
			}

			if term.IsTerminal(int(os.Stdout.Fd())) {
				output = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
			}

			log := zerolog.New(output).Level(lvl)

			log.Info().Str("log-level", lvl.String()).Msg("logger created")

			return log.WithContext(ctx), nil
		},
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
			&cli.StringFlag{
				Name:    "log-otel-grpc-endpoint",
				Usage:   "Forward logs to an OpenTelemetry gRPC endpoint",
				Sources: cli.EnvVars("LOG_OTEL_GRPC_ENDPOINT"),
				Value:   "",
				Validator: func(colURL string) error {
					_, err := url.Parse(colURL)

					return err
				},
			},
		},
		Commands: []*cli.Command{
			serveCommand(),
		},
	}
}
