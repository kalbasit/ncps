package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var errIncalculableMigrationDir = errors.New("error calculating the migration directory")

func main() {
	os.Exit(run())
}

func run() int {
	// Parse the database URL from the environment.
	dbURL := os.Getenv("DATABASE_URL")

	for i, arg := range os.Args[1:] {
		if arg == "--url" && i+1 < len(os.Args)-1 {
			dbURL = os.Args[i+2]
		} else if strings.HasPrefix(arg, "--url=") {
			dbURL = strings.TrimPrefix(arg, "--url=")
		}
	}

	// Set DBMATE_MIGRATIONS_DIR for dbmate to use, but only if not already set.
	if os.Getenv("DBMATE_MIGRATIONS_DIR") == "" {
		fullMigrationsPath, err := computeMigrationsDir(dbURL)
		if err != nil {
			log.Printf("%s", err)

			return execDbmate(os.Args[1:])
		}

		if err := os.Setenv("DBMATE_MIGRATIONS_DIR", fullMigrationsPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting DBMATE_MIGRATIONS_DIR: %v\n", err)

			return 1
		}
	}

	// Set DBMATE_SCHEMA_FILE for dbmate to use, but only if not already set.
	if os.Getenv("DBMATE_SCHEMA_FILE") == "" {
		fullSchemaPath, err := computeSchemaFile(dbURL)
		if err != nil {
			log.Printf("%s", err)

			return execDbmate(os.Args[1:])
		}

		if err := os.Setenv("DBMATE_SCHEMA_FILE", fullSchemaPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting DBMATE_SCHEMA_FILE: %v\n", err)

			return 1
		}
	}

	return execDbmate(os.Args[1:])
}

func computeMigrationsDir(dbURL string) (string, error) {
	// Determine database type from URL scheme
	dbType, err := getDatabaseType(dbURL)
	if err != nil {
		return "", err
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
	return filepath.Join(basePath, dbType), nil
}

func computeSchemaFile(dbURL string) (string, error) {
	// Determine database type from URL scheme
	dbType, err := getDatabaseType(dbURL)
	if err != nil {
		return "", err
	}

	// Determine the base schema directory
	// Priority order:
	// 1. NCPS_DB_SCHEMA_DIR environment variable (set by devshell or Docker)
	// 2. Fallback to /share/ncps/db/schema (Docker default)
	// 3. Fallback to db/schema (relative path, only works from repo root)
	basePath := os.Getenv("NCPS_DB_SCHEMA_DIR")
	if basePath == "" {
		basePath = "/share/ncps/db/schema"
		if _, err := os.Stat(basePath); os.IsNotExist(err) {
			basePath = "db/schema"
		}
	}

	// Build the full migrations path with database-specific subdirectory
	return filepath.Join(basePath, dbType+".sql"), nil
}

func getDatabaseType(dbURL string) (string, error) {
	var dbType string

	switch {
	case strings.HasPrefix(dbURL, "sqlite:"):
		dbType = "sqlite"
	case strings.HasPrefix(dbURL, "postgresql:"), strings.HasPrefix(dbURL, "postgres:"):
		dbType = "postgres"
	case strings.HasPrefix(dbURL, "mysql:"):
		dbType = "mysql"
	default:
		return dbType, errIncalculableMigrationDir
	}

	return dbType, nil
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

	//nolint:noctx
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
