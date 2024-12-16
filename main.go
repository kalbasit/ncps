package main

import (
	"context"
	"io"
	"log"
	"os"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/term"

	"github.com/kalbasit/ncps/cmd"
)

func main() {
	os.Exit(realMain())
}

func realMain() int {
	c := cmd.New(newLogger())

	if err := c.Run(context.Background(), os.Args); err != nil {
		log.Printf("error running the application: %s", err)

		return 1
	}

	return 0
}

func newLogger() zerolog.Logger {
	var output io.Writer = os.Stdout

	if term.IsTerminal(int(os.Stdout.Fd())) {
		output = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	}

	return zerolog.New(output)
}
