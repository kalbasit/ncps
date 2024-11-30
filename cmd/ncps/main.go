package main

import (
	"context"
	"log"
	"os"

	"github.com/urfave/cli/v3"
)

func main() {
	os.Exit(realMain())
}

func realMain() int {
	cmd := &cli.Command{
		Name:  "ncps",
		Usage: "Nix Binary Cache Proxy Service",
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
