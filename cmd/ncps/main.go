package main

import (
	"log"
	"os"

	"github.com/urfave/cli/v2"
)

func main() {
	os.Exit(realMain())
}

func realMain() int {
	app := &cli.App{
		Name:  "ncps",
		Usage: "Nix Binary Cache Proxy Service",
		Commands: []*cli.Command{
			serveCommand,
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Printf("error running the application: %s", err)
		return 1
	}

	return 0
}
