package ncps

import (
	"context"
	"errors"

	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"

	"github.com/kalbasit/ncps/pkg/database"
)

var (
	errUnsupportedDialect = errors.New("unsupported database dialect")
	errVersionRequired    = errors.New("version argument is required")
)

// MigrateCommand returns the migrate CLI command.
func MigrateCommand(
	flagSources flagSourcesFn,
	_ registerShutdownFn,
) *cli.Command {
	return &cli.Command{
		Name:  "migrate",
		Usage: "Manage database migrations",
		Description: `Manage database migrations using bun/migrate.

This command applies or rolls back database schema migrations based on the
embedded SQL migration files. The appropriate migration files are selected
automatically based on the database dialect (SQLite, PostgreSQL, MySQL).`,
		Commands: []*cli.Command{
			{
				Name:  "up",
				Usage: "Apply all pending migrations",
				Description: `Apply all pending migrations to the database.

This runs all unapplied migration files in order.`,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "cache-database-url",
						Usage:    "The URL of the database",
						Sources:  flagSources("cache.database-url", "CACHE_DATABASE_URL"),
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					logger := zerolog.Ctx(ctx).With().Str("cmd", "migrate up").Logger()
					ctx = logger.WithContext(ctx)

					db, err := createDatabaseQuerier(cmd)
					if err != nil {
						logger.Error().Err(err).Msg("error creating database connection")

						return err
					}
					defer db.Close()

					migrator := database.Migrations(db)
					if migrator == nil {
						logger.Error().Msg("unsupported database dialect")

						return errUnsupportedDialect
					}

					if err := migrator.Init(ctx); err != nil {
						logger.Error().Err(err).Msg("error initializing migrations")

						return err
					}

					if _, err := migrator.Migrate(ctx); err != nil {
						logger.Error().Err(err).Msg("error applying migrations")

						return err
					}

					logger.Info().Msg("all migrations applied successfully")

					return nil
				},
			},
			{
				Name:  "up-to",
				Usage: "Apply migrations up to and including the specified version",
				Description: `Apply migrations up to and including the specified version.

Arguments:
  VERSION is the migration version to apply up to (e.g., 20260101000000)`,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "cache-database-url",
						Usage:    "The URL of the database",
						Sources:  flagSources("cache.database-url", "CACHE_DATABASE_URL"),
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					logger := zerolog.Ctx(ctx).With().Str("cmd", "migrate up-to").Logger()
					ctx = logger.WithContext(ctx)

					db, err := createDatabaseQuerier(cmd)
					if err != nil {
						logger.Error().Err(err).Msg("error creating database connection")

						return err
					}
					defer db.Close()

					migrator := database.Migrations(db)
					if migrator == nil {
						logger.Error().Msg("unsupported database dialect")

						return errUnsupportedDialect
					}

					if err := migrator.Init(ctx); err != nil {
						logger.Error().Err(err).Msg("error initializing migrations")

						return err
					}

					version := cmd.Args().First()
					if version == "" {
						logger.Error().Msg("version argument is required")

						return errVersionRequired
					}

					if err := migrator.RunMigration(ctx, version); err != nil {
						logger.Error().Err(err).Str("version", version).Msg("error applying migration")

						return err
					}

					logger.Info().Str("version", version).Msg("migration applied successfully")

					return nil
				},
			},
			{
				Name:  "down",
				Usage: "Roll back the last applied migration",
				Description: `Roll back the last applied migration.

This rolls back the most recently applied migration file.`,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "cache-database-url",
						Usage:    "The URL of the database",
						Sources:  flagSources("cache.database-url", "CACHE_DATABASE_URL"),
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					logger := zerolog.Ctx(ctx).With().Str("cmd", "migrate down").Logger()
					ctx = logger.WithContext(ctx)

					db, err := createDatabaseQuerier(cmd)
					if err != nil {
						logger.Error().Err(err).Msg("error creating database connection")

						return err
					}
					defer db.Close()

					migrator := database.Migrations(db)
					if migrator == nil {
						logger.Error().Msg("unsupported database dialect")

						return errUnsupportedDialect
					}

					if err := migrator.Init(ctx); err != nil {
						logger.Error().Err(err).Msg("error initializing migrations")

						return err
					}

					if _, err := migrator.Rollback(ctx); err != nil {
						logger.Error().Err(err).Msg("error rolling back migration")

						return err
					}

					logger.Info().Msg("migration rolled back successfully")

					return nil
				},
			},
			{
				Name:  "down-to",
				Usage: "Roll back to but not including the specified version",
				Description: `Roll back to but not including the specified version.

Arguments:
  VERSION is the migration version to roll back to (exclusive)`,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "cache-database-url",
						Usage:    "The URL of the database",
						Sources:  flagSources("cache.database-url", "CACHE_DATABASE_URL"),
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					logger := zerolog.Ctx(ctx).With().Str("cmd", "migrate down-to").Logger()
					ctx = logger.WithContext(ctx)

					db, err := createDatabaseQuerier(cmd)
					if err != nil {
						logger.Error().Err(err).Msg("error creating database connection")

						return err
					}
					defer db.Close()

					migrator := database.Migrations(db)
					if migrator == nil {
						logger.Error().Msg("unsupported database dialect")

						return errUnsupportedDialect
					}

					if err := migrator.Init(ctx); err != nil {
						logger.Error().Err(err).Msg("error initializing migrations")

						return err
					}

					version := cmd.Args().First()
					if version == "" {
						logger.Error().Msg("version argument is required")

						return errVersionRequired
					}

					// RollbackTo is not directly available; we use MarkUnapplied
					// to mark migrations as unapplied up to (but not including) the target version.
					// First, get all applied migrations.
					applied, err := migrator.AppliedMigrations(ctx)
					if err != nil {
						logger.Error().Err(err).Msg("error fetching applied migrations")

						return err
					}

					// Find migrations to rollback (those after the target version)
					var toRollback []string

					for _, m := range applied {
						if m.Name >= version {
							toRollback = append(toRollback, m.Name)
						}
					}

					if len(toRollback) == 0 {
						logger.Info().Str("version", version).Msg("no migrations to roll back")

						return nil
					}

					// Rollback each migration in reverse order
					for _, name := range toRollback {
						if err := migrator.RunMigration(ctx, name); err != nil {
							logger.Error().Err(err).Str("migration", name).Msg("error rolling back migration")

							return err
						}
					}

					logger.Info().Str("version", version).Int("count", len(toRollback)).Msg("migrations rolled back successfully")

					return nil
				},
			},
			{
				Name:  "status",
				Usage: "Print all migrations and their applied/pending state",
				Description: `Print all migrations and their applied/pending state.

Shows each migration version with its status (applied or pending).`,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "cache-database-url",
						Usage:    "The URL of the database",
						Sources:  flagSources("cache.database-url", "CACHE_DATABASE_URL"),
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					logger := zerolog.Ctx(ctx).With().Str("cmd", "migrate status").Logger()
					ctx = logger.WithContext(ctx)

					db, err := createDatabaseQuerier(cmd)
					if err != nil {
						logger.Error().Err(err).Msg("error creating database connection")

						return err
					}
					defer db.Close()

					migrator := database.Migrations(db)
					if migrator == nil {
						logger.Error().Msg("unsupported database dialect")

						return errUnsupportedDialect
					}

					if err := migrator.Init(ctx); err != nil {
						logger.Error().Err(err).Msg("error initializing migrations")

						return err
					}

					migrations, err := migrator.MigrationsWithStatus(ctx)
					if err != nil {
						logger.Error().Err(err).Msg("error loading migrations")

						return err
					}

					if len(migrations) == 0 {
						logger.Info().Msg("no migrations found")

						return nil
					}

					for _, m := range migrations {
						status := "pending"
						if m.IsApplied() {
							status = "applied"
						}

						logger.Info().
							Str("version", m.Name).
							Str("status", status).
							Msg("migration")
					}

					return nil
				},
			},
		},
	}
}
