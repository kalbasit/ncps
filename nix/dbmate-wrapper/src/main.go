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
	// Parse args to find --url value and check if --migrations-dir is provided
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

	// If --migrations-dir is already provided via flag, don't override it
	// If DBMATE_MIGRATIONS_DIR is already set, respect it (user has explicitly configured it)
	if hasMigrationsDir || os.Getenv("DBMATE_MIGRATIONS_DIR") != "" {
		return execDbmate(os.Args[1:])
	}

	// Determine database type from URL scheme
	var migrationsSubdir string
	switch {
	case strings.HasPrefix(dbURL, "sqlite:"):
		migrationsSubdir = "sqlite"
	case strings.HasPrefix(dbURL, "postgresql:"), strings.HasPrefix(dbURL, "postgres:"):
		migrationsSubdir = "postgres"
	case strings.HasPrefix(dbURL, "mysql:"):
		migrationsSubdir = "mysql"
	default:
		// If we can't determine the database type, just pass through
		return execDbmate(os.Args[1:])
	}

	// Determine the base migrations directory
	// Priority order:
	// 1. NCPS_DB_MIGRATIONS_DIR environment variable (set by devshell or Docker)
	// 2. Fallback to /share/ncps/db/migrations (Docker default)
	// 3. Fallback to db/migrations (relative path, only works from repo root)
	basePath := os.Getenv("NCPS_DB_MIGRATIONS_DIR")
	if basePath == "" {
		basePath = "/share/ncps/db/migrations"
		if _, err := os.Stat(basePath); os.IsNotExist(err) {
			basePath = "db/migrations"
		}
	}

	// Build the full migrations path with database-specific subdirectory
	fullMigrationsPath := filepath.Join(basePath, migrationsSubdir)

	// Set DBMATE_MIGRATIONS_DIR for dbmate to use
	if err := os.Setenv("DBMATE_MIGRATIONS_DIR", fullMigrationsPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting DBMATE_MIGRATIONS_DIR: %v\n", err)
		return 1
	}

	return execDbmate(os.Args[1:])
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
	cmd.Env = os.Environ() // Pass through all environment variables including DBMATE_MIGRATIONS_DIR

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "Error executing dbmate: %v\n", err)
		return 1
	}

	return 0
}
