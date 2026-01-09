package main

import (
	"context"
	"log"
	"os"

	"github.com/kalbasit/ncps/pkg/ncps"
)

func main() {
	os.Exit(realMain())
}

func realMain() int {
	c, err := ncps.New()
	if err != nil {
		log.Printf("error creating the application: %s", err)

		return 1
	}

	if err := c.Run(context.Background(), os.Args); err != nil {
		log.Printf("error running the application: %s", err)

		return 1
	}

	return 0
}
