package ncps

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"

	"github.com/kalbasit/ncps/migrations"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/database/migrate"
)

// migrateCommand wires `ncps migrate` with `up` and `down` subcommands.
// Per design D10/D11, down is intentionally unsupported and prints the
// expand-contract recipe pointer.
func migrateCommand(flagSources flagSourcesFn) *cli.Command {
	return &cli.Command{
		Name:  "migrate",
		Usage: "Apply database schema migrations (forward-only).",
		Description: "Adopts the ncps schema_migrations tracking table " +
			"(converting it from dbmate format if needed) and applies any " +
			"pending migrations via goose. Fresh databases bypass the " +
			"historical migration files and use Ent's Schema.Create.",
		Commands: []*cli.Command{
			migrateUpCommand(flagSources),
			migrateDownCommand(flagSources),
		},
	}
}

func migrateUpCommand(flagSources flagSourcesFn) *cli.Command {
	return &cli.Command{
		Name:        "up",
		Usage:       "Apply all pending migrations to the configured cache database.",
		Description: "On first invocation adopts the dbmate-era schema_migrations table to goose's canonical shape.",
		Flags: []cli.Flag{
			cacheDatabaseURLFlag(flagSources),
			&cli.BoolFlag{
				Name:  flagNameDryRun,
				Usage: "Print the detected state + pending migrations without issuing any DDL.",
				Value: false,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			dbURL := cmd.String("cache-database-url")
			if dbURL == "" {
				//nolint:err113 // diagnostic
				return errors.New("migrate up: --cache-database-url is required")
			}

			dialect, err := database.DetectFromDatabaseURL(dbURL)
			if err != nil {
				return fmt.Errorf("migrate up: %w", err)
			}

			db, closeFn, err := openRawDB(dbURL, dialect)
			if err != nil {
				return fmt.Errorf("migrate up: open db: %w", err)
			}
			defer closeFn()

			sub, err := fs.Sub(migrations.FS, dialectSubdir(dialect))
			if err != nil {
				return fmt.Errorf("migrate up: dialect sub-fs: %w", err)
			}

			opts := migrate.Options{DB: db, Dialect: dialect, MigrationsFS: sub}

			if cmd.Bool("dry-run") {
				plan, err := migrate.DryRun(ctx, opts)
				if err != nil {
					return fmt.Errorf("migrate up --dry-run: %w", err)
				}

				w := cmd.Writer
				if w == nil {
					w = os.Stdout
				}

				printPlan(w, plan)

				return nil
			}

			zerolog.Ctx(ctx).Info().Msg("running migrate up")

			if err := migrate.Up(ctx, opts); err != nil {
				return fmt.Errorf("migrate up: %w", err)
			}

			zerolog.Ctx(ctx).Info().Msg("migrate up complete")

			return nil
		},
	}
}

func migrateDownCommand(flagSources flagSourcesFn) *cli.Command {
	return &cli.Command{
		Name:  "down",
		Usage: "(unsupported) ncps uses a forward-only migration policy.",
		Description: "Down migrations are not supported. See the expand-contract " +
			"recipe and the four-step NOT NULL promotion procedure in CLAUDE.md.",
		Flags: []cli.Flag{cacheDatabaseURLFlag(flagSources)},
		Action: func(_ context.Context, _ *cli.Command) error {
			return migrate.ErrDownNotSupported
		},
	}
}

// cacheDatabaseURLFlag returns the standard cache-database-url flag used
// by every migrate subcommand. Keeps the wiring DRY.
func cacheDatabaseURLFlag(flagSources flagSourcesFn) cli.Flag {
	return &cli.StringFlag{
		Name:     flagNameDBURL,
		Usage:    "Database URL: sqlite:/path, postgresql://..., mysql://...",
		Sources:  flagSources("cache.database.url", "CACHE_DATABASE_URL"),
		Required: true,
	}
}

// openRawDB returns the underlying *sql.DB for the configured cache
// database URL. The migrate package operates against *sql.DB directly
// (not the Querier interface) so it can stay independent of the
// sqlc-generated wrappers — which are being removed in §10/§12.
func openRawDB(dbURL string, _ database.Type) (*sql.DB, func(), error) {
	q, err := database.Open(dbURL, nil)
	if err != nil {
		return nil, nil, err
	}

	return q.DB(), func() { _ = q.DB().Close() }, nil
}

// dialectSubdir maps the database type to the corresponding sub-FS
// name under migrations/.
func dialectSubdir(d database.Type) string {
	switch d {
	case database.TypeSQLite:
		return "sqlite"
	case database.TypePostgreSQL:
		return "postgres"
	case database.TypeMySQL:
		return "mysql"
	case database.TypeUnknown:
		fallthrough
	default:
		return ""
	}
}

// printPlan writes the migrate.Plan to w. Accepting io.Writer (rather
// than hardcoding os.Stdout) makes the helper testable and lets the
// cli.Command's configured Writer flow through.
func printPlan(w io.Writer, plan migrate.Plan) {
	fmt.Fprintf(w, "migrate up --dry-run\n")
	fmt.Fprintf(w, "  state           : %s\n", plan.State)
	fmt.Fprintf(w, "  adoption action : %s\n", plan.AdoptionAction)
	fmt.Fprintf(w, "  applied versions: %d\n", plan.AppliedCount)
	fmt.Fprintf(w, "  pending versions: %d\n", len(plan.PendingVersions))

	for _, v := range plan.PendingVersions {
		fmt.Fprintf(w, "    - %d\n", v)
	}
}
