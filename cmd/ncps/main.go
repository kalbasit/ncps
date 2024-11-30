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

var Version string = "dev"
var logger log15.Logger

func init() {
	logger = log15.New()
	if term.IsTerminal(int(os.Stdout.Fd())) {
		logger.SetHandler(log15.StreamHandler(colorable.NewColorableStdout(), log15.TerminalFormat()))
	} else {
		logger.SetHandler(log15.StreamHandler(os.Stdout, log15.JsonFormat()))
	}
}

func main() {
	os.Exit(realMain())
}

func realMain() int {
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
