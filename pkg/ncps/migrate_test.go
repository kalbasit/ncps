//nolint:testpackage // Test needs access to unexported types (registerShutdownFn, shutdownFn) and MigrateCommand.
package ncps

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/kalbasit/ncps/pkg/database"
)

// TestMigrateUp tests the migrate up command.
func TestMigrateUp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "test.db")
	dbURL := "sqlite:" + dbFile

	flagSources := func(_, envVar string) cli.ValueSourceChain {
		return cli.NewValueSourceChain(cli.EnvVar(envVar))
	}

	registerShutdown := registerShutdownFn(func(_ string, _ shutdownFn) {})

	migrateCmd := MigrateCommand(flagSources, registerShutdown)

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Run migrate up
	err := migrateCmd.Run(ctx, []string{
		"migrate", "up",
		"--cache-database-url", dbURL,
	})
	require.NoError(t, err)

	// Verify database has migrations applied
	db, err := database.Open(dbURL, nil)
	require.NoError(t, err)

	defer db.Close()

	// Check that migrations were applied
	migrator := database.Migrations(db)
	require.NotNil(t, migrator)

	applied, err := migrator.AppliedMigrations(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, applied, "expected migrations to be applied")

	// Cleanup
	os.RemoveAll(dir)
}

// TestMigrateStatus tests the migrate status command.
func TestMigrateStatus(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "test.db")
	dbURL := "sqlite:" + dbFile

	flagSources := func(_, envVar string) cli.ValueSourceChain {
		return cli.NewValueSourceChain(cli.EnvVar(envVar))
	}

	registerShutdown := registerShutdownFn(func(_ string, _ shutdownFn) {})

	migrateCmd := MigrateCommand(flagSources, registerShutdown)

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Run migrate up first
	err := migrateCmd.Run(ctx, []string{
		"migrate", "up",
		"--cache-database-url", dbURL,
	})
	require.NoError(t, err)

	// Run migrate status
	err = migrateCmd.Run(ctx, []string{
		"migrate", "status",
		"--cache-database-url", dbURL,
	})
	require.NoError(t, err)

	// Cleanup
	os.RemoveAll(dir)
}

// TestMigrateDown tests the migrate down command.
func TestMigrateDown(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "test.db")
	dbURL := "sqlite:" + dbFile

	flagSources := func(_, envVar string) cli.ValueSourceChain {
		return cli.NewValueSourceChain(cli.EnvVar(envVar))
	}

	registerShutdown := registerShutdownFn(func(_ string, _ shutdownFn) {})

	migrateCmd := MigrateCommand(flagSources, registerShutdown)

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Run migrate up first
	err := migrateCmd.Run(ctx, []string{
		"migrate", "up",
		"--cache-database-url", dbURL,
	})
	require.NoError(t, err)

	// Run migrate down
	err = migrateCmd.Run(ctx, []string{
		"migrate", "down",
		"--cache-database-url", dbURL,
	})
	require.NoError(t, err)

	// Cleanup
	os.RemoveAll(dir)
}

// TestMigrateUpWithMissingURL tests that migrate up fails with proper error when URL is missing.
func TestMigrateUpWithMissingURL(t *testing.T) {
	t.Parallel()

	flagSources := func(_, envVar string) cli.ValueSourceChain {
		return cli.NewValueSourceChain(cli.EnvVar(envVar))
	}

	registerShutdown := registerShutdownFn(func(_ string, _ shutdownFn) {})

	migrateCmd := MigrateCommand(flagSources, registerShutdown)

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Run migrate up without URL
	err := migrateCmd.Run(ctx, []string{
		"migrate", "up",
	})
	require.ErrorContains(t, err, "Required flag")
}

// TestMigrateUpWithInvalidDialect tests that migrate up fails with unsupported dialect.
func TestMigrateUpWithInvalidDialect(t *testing.T) {
	t.Parallel()

	flagSources := func(_, envVar string) cli.ValueSourceChain {
		return cli.NewValueSourceChain(cli.EnvVar(envVar))
	}

	registerShutdown := registerShutdownFn(func(_ string, _ shutdownFn) {})

	migrateCmd := MigrateCommand(flagSources, registerShutdown)

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())

	// Run migrate up with invalid URL
	err := migrateCmd.Run(ctx, []string{
		"migrate", "up",
		"--cache-database-url", "invalid://localhost",
	})
	require.ErrorContains(t, err, "unsupported")
}
