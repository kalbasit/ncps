package main

import (
	"context"
	"log"
	"os"

	"github.com/kalbasit/ncps/cmd"
)

func main() {
	os.Exit(realMain())
}

func realMain() int {
	c := cmd.New()

	if err := c.Run(context.Background(), os.Args); err != nil {
		log.Printf("error running the application: %s", err)

		return 1
	}

	return 0
}
