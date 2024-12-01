package main

import (
	"context"
	"log"
	"os"

	"github.com/inconshreveable/log15/v3"
	"github.com/mattn/go-colorable"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

var (
	// Version defines the version of the binary, and is meant to be set with ldflags at build time.
	//nolint:gochecknoglobals
	Version = "dev"

	//nolint:gochecknoglobals
	logger log15.Logger
)

func main() {
	os.Exit(realMain())
}

func realMain() int {
	logger = log15.New()
	if term.IsTerminal(int(os.Stdout.Fd())) {
		logger.SetHandler(log15.StreamHandler(colorable.NewColorableStdout(), log15.TerminalFormat()))
	} else {
		logger.SetHandler(log15.StreamHandler(os.Stdout, log15.JsonFormat()))
	}

	cmd := &cli.Command{
		Name:    "ncps",
		Usage:   "Nix Binary Cache Proxy Service",
		Version: Version,
		Commands: []*cli.Command{
			serveCommand,
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Printf("error running the application: %s", err)

		return 1
	}

	return 0
}
