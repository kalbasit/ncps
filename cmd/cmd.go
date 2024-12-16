package cmd

import (
	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"
)

// Version defines the version of the binary, and is meant to be set with ldflags at build time.
//
//nolint:gochecknoglobals
var Version = "dev"

func New(logger zerolog.Logger) *cli.Command {
	return &cli.Command{
		Name:    "ncps",
		Usage:   "Nix Binary Cache Proxy Service",
		Version: Version,
		Commands: []*cli.Command{
			serveCommand(logger),
		},
	}
}
