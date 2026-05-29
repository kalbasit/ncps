package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"

	"github.com/kalbasit/ncps/pkg/database"
)

// ErrDownNotSupported is returned by Down to signal callers that ncps
// uses a forward-only migration policy (design D10: expand-contract +
// four-step NOT NULL recipe).
var ErrDownNotSupported = errors.New(
	"migrate: down migrations are not supported — use the expand-contract recipe " +
		"and the four-step NOT NULL promotion procedure documented in CLAUDE.md",
)

// ErrOptionsDBNil / ErrOptionsMigrationsFSNil signal misuse of the
// migrate.Options struct.
var (
	ErrOptionsDBNil           = errors.New("migrate: Options.DB is nil")
	ErrOptionsMigrationsFSNil = errors.New("migrate: Options.MigrationsFS is nil")
)

// Options bundles the inputs to Up / DryRun. Constructed by the caller
// (typically the CLI) so this package stays free of urfave/cli imports.
type Options struct {
	// DB is the database connection. The caller owns its lifecycle.
	DB *sql.DB

	// Dialect identifies which SQL dialect DB speaks.
	Dialect database.Type

	// MigrationsFS is the dialect-specific sub-FS — i.e. the result of
	// `fs.Sub(migrations.FS, "<dialect>")`. The caller is responsible
	// for the sub-FS lookup so this package does not import the
	// migrations package directly.
	MigrationsFS fs.FS
}

// Plan is the result of DryRun: what Up *would* do without actually
// touching the database.
type Plan struct {
	// State is the detected adoption state.
	State State

	// AdoptionAction is a human-readable description of what the
	// adoption step would do (or "no adoption needed").
	AdoptionAction string

	// PendingVersions is the list of migration version stamps that
	// goose would apply after adoption (empty for fresh-install and
	// adopted-but-current states).
	PendingVersions []int64

	// AppliedCount is the number of versions already recorded in
	// schema_migrations at probe time (0 for empty / dbmate states).
	AppliedCount int
}

// Up runs the full `ncps migrate up` flow: probe state, run any needed
// adoption / fresh-install work, then hand off to goose for incremental
// migrations.
//
// Returns nil on success. The caller logs progress; this function is
// quiet by design so a future programmatic caller (tests, fsck) can
// surface results without parsing log lines.
func Up(ctx context.Context, opts Options) error {
	if err := validateOptions(opts); err != nil {
		return err
	}

	state, err := Detect(ctx, opts.DB, opts.Dialect)
	if err != nil {
		return fmt.Errorf("migrate: detect state: %w", err)
	}

	if state == StateEmpty {
		return freshInstall(ctx, opts.DB, opts.Dialect, opts.MigrationsFS)
	}

	if err := adopt(ctx, opts.DB, opts.Dialect, state); err != nil {
		return fmt.Errorf("migrate: adopt: %w", err)
	}

	return runGoose(ctx, opts)
}

// DryRun returns the Plan that Up would execute without touching the
// database. Side-effect free except for read-only catalog probes.
func DryRun(ctx context.Context, opts Options) (Plan, error) {
	if err := validateOptions(opts); err != nil {
		return Plan{}, err
	}

	state, err := Detect(ctx, opts.DB, opts.Dialect)
	if err != nil {
		return Plan{}, fmt.Errorf("migrate: detect state: %w", err)
	}

	plan := Plan{State: state}

	switch state {
	case StateEmpty:
		plan.AdoptionAction = "fresh install via Schema.Create + seed schema_migrations"
		// All embedded versions are "pending" only in the sense that
		// they will be SEEDED as applied — not executed. We surface the
		// count via PendingVersions so operators see scope.
		versions, err := listEmbeddedVersions(opts.MigrationsFS)
		if err != nil {
			return plan, fmt.Errorf("list versions: %w", err)
		}

		plan.PendingVersions = versions
	case StateDbmate:
		plan.AdoptionAction = "convert schema_migrations from dbmate shape to goose shape"
	case StateMySQLS4:
		plan.AdoptionAction = "resume mysql adoption from S4 (create + copy + drop backup)"
	case StateMySQLS5:
		plan.AdoptionAction = "resume mysql adoption from S5 (verify + drop backup)"
	case StateImpossibleS6:
		plan.AdoptionAction = "ABORT — impossible state requires manual intervention"
	case StateAdopted:
		plan.AdoptionAction = "no adoption needed"
	case StateUnknown:
		fallthrough
	default:
		plan.AdoptionAction = "unknown — abort"
	}

	if state == StateAdopted {
		applied, pending, err := goosePending(ctx, opts)
		if err != nil {
			return plan, fmt.Errorf("compute pending: %w", err)
		}

		plan.AppliedCount = applied
		plan.PendingVersions = pending
	}

	return plan, nil
}

// Down is the explicit "we don't support down migrations" entry point.
// CLI callers wire this to `ncps migrate down` and let the error message
// guide operators to the expand-contract recipe.
func Down(_ context.Context, _ Options) error {
	return ErrDownNotSupported
}

func validateOptions(opts Options) error {
	if opts.DB == nil {
		return ErrOptionsDBNil
	}

	if opts.MigrationsFS == nil {
		return ErrOptionsMigrationsFSNil
	}

	switch opts.Dialect {
	case database.TypeSQLite, database.TypePostgreSQL, database.TypeMySQL:
		return nil
	case database.TypeUnknown:
		fallthrough
	default:
		return fmt.Errorf("migrate: %w %v", ErrUnknownDialect, opts.Dialect)
	}
}

func runGoose(ctx context.Context, opts Options) error {
	gooseDia, err := gooseDialectFor(opts.Dialect)
	if err != nil {
		return err
	}

	provider, err := goose.NewProvider(
		gooseDia, opts.DB, opts.MigrationsFS,
		goose.WithTableName("schema_migrations"),
	)
	if err != nil {
		return fmt.Errorf("goose.NewProvider: %w", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("goose.Up: %w", err)
	}

	return nil
}

// goosePending returns (applied, pending-versions, error) by listing
// the versions goose would still apply.
func goosePending(ctx context.Context, opts Options) (int, []int64, error) {
	gooseDia, err := gooseDialectFor(opts.Dialect)
	if err != nil {
		return 0, nil, err
	}

	provider, err := goose.NewProvider(
		gooseDia, opts.DB, opts.MigrationsFS,
		goose.WithTableName("schema_migrations"),
	)
	if err != nil {
		return 0, nil, fmt.Errorf("goose.NewProvider: %w", err)
	}

	status, err := provider.Status(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("goose.Status: %w", err)
	}

	var (
		applied int
		pending []int64
	)

	for _, s := range status {
		if s.State == goose.StatePending {
			pending = append(pending, s.Source.Version)
		} else {
			applied++
		}
	}

	return applied, pending, nil
}

func gooseDialectFor(d database.Type) (goose.Dialect, error) {
	switch d {
	case database.TypeSQLite:
		return goose.DialectSQLite3, nil
	case database.TypePostgreSQL:
		return goose.DialectPostgres, nil
	case database.TypeMySQL:
		return goose.DialectMySQL, nil
	case database.TypeUnknown:
		fallthrough
	default:
		return "", fmt.Errorf("gooseDialectFor: %w %v", ErrUnknownDialect, d)
	}
}
