package cmd

import (
	"github.com/inconshreveable/log15/v3"
	"github.com/urfave/cli/v3"
)

var (
	// Version defines the version of the binary, and is meant to be set with ldflags at build time.
	//nolint:gochecknoglobals
	Version = "dev"
)

func New(logger log15.Logger) *cli.Command {
	return &cli.Command{
		Name:    "ncps",
		Usage:   "Nix Binary Cache Proxy Service",
		Version: Version,
		Commands: []*cli.Command{
			serveCommand(logger),
		},
	}
}
