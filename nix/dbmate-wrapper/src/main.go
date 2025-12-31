package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	os.Exit(run())
}

func run() int {
	// Parse args to find --url value
	var dbURL string
	hasMigrationsDir := false

	for i, arg := range os.Args[1:] {
		if arg == "--url" && i+1 < len(os.Args)-1 {
			dbURL = os.Args[i+2]
		} else if strings.HasPrefix(arg, "--url=") {
			dbURL = strings.TrimPrefix(arg, "--url=")
		}

		if arg == "--migrations-dir" || strings.HasPrefix(arg, "--migrations-dir=") {
			hasMigrationsDir = true
		}
	}

	// If --migrations-dir is already provided, don't override it
	if hasMigrationsDir {
		return execDbmate(os.Args[1:])
	}

	// Determine database type from URL scheme
	var migrationsDir string
	switch {
	case strings.HasPrefix(dbURL, "sqlite:"):
		migrationsDir = "sqlite"
	case strings.HasPrefix(dbURL, "postgresql:"), strings.HasPrefix(dbURL, "postgres:"):
		migrationsDir = "postgres"
	case strings.HasPrefix(dbURL, "mysql:"):
		migrationsDir = "mysql"
	default:
		// If we can't determine the database type, just pass through
		return execDbmate(os.Args[1:])
	}

	// Determine the base migrations directory
	// Try Docker path first, fall back to local dev path
	basePath := "/share/ncps/db/migrations"
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		basePath = "db/migrations"
	}

	fullMigrationsPath := filepath.Join(basePath, migrationsDir)

	// Build new args with --migrations-dir inserted
	newArgs := append([]string{"--migrations-dir", fullMigrationsPath}, os.Args[1:]...)

	return execDbmate(newArgs)
}

// execDbmate executes the real dbmate binary with the given arguments.
func execDbmate(args []string) int {
	// Look for the real dbmate binary
	// Consistently named as "dbmate.real" in both dev and Docker environments
	dbmatePath, err := exec.LookPath("dbmate.real")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not find dbmate.real in PATH: %v\n", err)
		return 1
	}

	cmd := exec.Command(dbmatePath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "Error executing dbmate: %v\n", err)
		return 1
	}

	return 0
}
