package main

import (
	"context"
	"log"
	"os"

	"github.com/inconshreveable/log15/v3"
	"github.com/kalbasit/ncps/cmd"
	"github.com/mattn/go-colorable"
	"golang.org/x/term"
)

func main() {
	os.Exit(realMain())
}

func realMain() int {
	logger := log15.New()
	if term.IsTerminal(int(os.Stdout.Fd())) {
		logger.SetHandler(log15.StreamHandler(colorable.NewColorableStdout(), log15.TerminalFormat()))
	} else {
		logger.SetHandler(log15.StreamHandler(os.Stdout, log15.JsonFormat()))
	}

	c := cmd.New(logger)

	if err := c.Run(context.Background(), os.Args); err != nil {
		log.Printf("error running the application: %s", err)

		return 1
	}

	return 0
}
