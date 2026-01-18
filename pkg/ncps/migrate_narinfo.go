package ncps

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
)

// ErrStorageIterationNotSupported is returned when the storage backend does not support iteration.
var ErrStorageIterationNotSupported = errors.New("storage backend does not support iteration")

type NarInfoWalker interface {
	WalkNarInfos(ctx context.Context, fn func(hash string) error) error
}

func migrateNarInfoCommand(
	flagSources flagSourcesFn,
) *cli.Command {
	return &cli.Command{
		Name:  "migrate-narinfo",
		Usage: "Migrate NarInfo files from storage to the database",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Simulate migration without writing to DB",
			},
			&cli.BoolFlag{
				Name:  "delete",
				Usage: "Delete .narinfo file after successful migration",
			},

			// Storage Flags
			&cli.StringFlag{
				Name:    "cache-storage-local",
				Usage:   "The local data path used for configuration and cache storage (use this OR S3 storage)",
				Sources: flagSources("cache.storage.local", "CACHE_STORAGE_LOCAL"),
			},
			&cli.StringFlag{
				Name:    "cache-storage-s3-bucket",
				Usage:   "S3 bucket name for storage (use this OR --cache-storage-local for local storage)",
				Sources: flagSources("cache.storage.s3.bucket", "CACHE_STORAGE_S3_BUCKET"),
			},
			&cli.StringFlag{
				Name:    "cache-storage-s3-endpoint",
				Usage:   "S3-compatible endpoint URL with scheme",
				Sources: flagSources("cache.storage.s3.endpoint", "CACHE_STORAGE_S3_ENDPOINT"),
			},
			&cli.StringFlag{
				Name:    "cache-storage-s3-region",
				Usage:   "S3 region (optional)",
				Sources: flagSources("cache.storage.s3.region", "CACHE_STORAGE_S3_REGION"),
			},
			&cli.StringFlag{
				Name:    "cache-storage-s3-access-key-id",
				Usage:   "S3 access key ID",
				Sources: flagSources("cache.storage.s3.access-key-id", "CACHE_STORAGE_S3_ACCESS_KEY_ID"),
			},
			&cli.StringFlag{
				Name:    "cache-storage-s3-secret-access-key",
				Usage:   "S3 secret access key",
				Sources: flagSources("cache.storage.s3.secret-access-key", "CACHE_STORAGE_S3_SECRET_ACCESS_KEY"),
			},
			&cli.BoolFlag{
				Name:    "cache-storage-s3-force-path-style",
				Usage:   "Force path-style S3 addressing",
				Sources: flagSources("cache.storage.s3.force-path-style", "CACHE_STORAGE_S3_FORCE_PATH_STYLE"),
			},

			// Database Flags
			&cli.StringFlag{
				Name:     "cache-database-url",
				Usage:    "The URL of the database",
				Sources:  flagSources("cache.database-url", "CACHE_DATABASE_URL"),
				Required: true,
			},
			&cli.IntFlag{
				Name:    "cache-database-pool-max-open-conns",
				Usage:   "Maximum number of open connections to the database",
				Sources: flagSources("cache.database.pool.max-open-conns", "CACHE_DATABASE_POOL_MAX_OPEN_CONNS"),
			},
			&cli.IntFlag{
				Name:    "cache-database-pool-max-idle-conns",
				Usage:   "Maximum number of idle connections in the pool",
				Sources: flagSources("cache.database.pool.max-idle-conns", "CACHE_DATABASE_POOL_MAX_IDLE_CONNS"),
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			logger := zerolog.Ctx(ctx).With().Str("cmd", "migrate-narinfo").Logger()
			ctx = logger.WithContext(ctx)

			dryRun := cmd.Bool("dry-run")
			deleteAfter := cmd.Bool("delete")

			// 1. Setup Database
			db, err := createDatabaseQuerier(cmd)
			if err != nil {
				logger.Error().Err(err).Msg("error creating database querier")

				return err
			}

			// 2. Setup Storage
			_, narInfoStore, _, err := getStorageBackend(ctx, cmd)
			if err != nil {
				logger.Error().Err(err).Msg("error creating storage backend")

				return err
			}

			walker, ok := narInfoStore.(NarInfoWalker)
			if !ok {
				return ErrStorageIterationNotSupported
			}

			// 3. Migrate
			logger.Info().Msg("starting migration")

			count := 0
			errorsCount := 0

			err = walker.WalkNarInfos(ctx, func(hash string) error {
				count++
				log := logger.With().Str("hash", hash).Logger()
				log.Info().Msg("processing narinfo")

				if err := migrateOne(ctx, db, narInfoStore, hash, dryRun, deleteAfter); err != nil {
					log.Error().Err(err).Msg("failed to migrate narinfo")

					errorsCount++
					// Continue migration even if one fails
					return nil
				}

				return nil
			})

			logger.Info().Int("total", count).Int("errors", errorsCount).Msg("migration completed")

			return err
		},
	}
}

func migrateOne(
	ctx context.Context,
	db database.Querier,
	store storage.NarInfoStore,
	hash string,
	dryRun bool,
	deleteAfter bool,
) error {
	// Fetch from storage
	ni, err := store.GetNarInfo(ctx, hash)
	if err != nil {
		return fmt.Errorf("failed to get narinfo from store: %w", err)
	}

	if dryRun {
		zerolog.Ctx(ctx).Info().Msg("[DRY-RUN] would store in DB")
	} else {
		if err := migrateOneToDatabase(ctx, db, hash, ni); err != nil {
			return err
		}
	}

	if deleteAfter {
		if dryRun {
			zerolog.Ctx(ctx).Info().Msg("[DRY-RUN] would delete from storage")
		} else {
			if err := store.DeleteNarInfo(ctx, hash); err != nil {
				return fmt.Errorf("failed to delete from store: %w", err)
			}

			zerolog.Ctx(ctx).Info().Msg("deleted from storage")
		}
	}

	return nil
}

func migrateOneToDatabase(
	ctx context.Context,
	db database.Querier,
	hash string,
	ni *narinfo.NarInfo, // using fully qualified or alias? "narinfo" package is imported as "narinfo"
) error {
	// Explicit transaction
	sqlDB := db.DB()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	qtx := db.WithTx(tx)

	// Create NarInfo
	nir, err := qtx.CreateNarInfo(ctx, database.CreateNarInfoParams{
		Hash:        hash,
		StorePath:   sql.NullString{String: ni.StorePath, Valid: ni.StorePath != ""},
		URL:         sql.NullString{String: ni.URL, Valid: ni.URL != ""},
		Compression: sql.NullString{String: ni.Compression, Valid: ni.Compression != ""},
		FileHash:    sql.NullString{String: ni.FileHash.String(), Valid: ni.FileHash != nil},
		FileSize:    sql.NullInt64{Int64: int64(ni.FileSize), Valid: true}, //nolint:gosec
		NarHash:     sql.NullString{String: ni.NarHash.String(), Valid: ni.NarHash != nil},
		NarSize:     sql.NullInt64{Int64: int64(ni.NarSize), Valid: true}, //nolint:gosec
		Deriver:     sql.NullString{String: ni.Deriver, Valid: ni.Deriver != ""},
		System:      sql.NullString{String: ni.System, Valid: ni.System != ""},
		Ca:          sql.NullString{String: ni.CA, Valid: ni.CA != ""},
	})
	if err != nil {
		if database.IsDuplicateKeyError(err) {
			zerolog.Ctx(ctx).Info().Msg("narinfo already in DB, skipping insert")

			existing, errGet := qtx.GetNarInfoByHash(ctx, hash)
			if errGet != nil {
				return fmt.Errorf("failed to get existing record: %w", errGet)
			}

			nir = existing
		} else {
			return fmt.Errorf("failed to create narinfo record: %w", err)
		}
	}

	// References
	for _, ref := range ni.References {
		if err := qtx.AddNarInfoReference(ctx, database.AddNarInfoReferenceParams{
			NarInfoID: nir.ID,
			Reference: ref,
		}); err != nil {
			if !database.IsDuplicateKeyError(err) {
				return fmt.Errorf("failed to add reference: %w", err)
			}
		}
	}

	// Signatures
	for _, sig := range ni.Signatures {
		if err := qtx.AddNarInfoSignature(ctx, database.AddNarInfoSignatureParams{
			NarInfoID: nir.ID,
			Signature: sig.String(),
		}); err != nil {
			if !database.IsDuplicateKeyError(err) {
				return fmt.Errorf("failed to add signature: %w", err)
			}
		}
	}

	// NarFile
	narURL, err := nar.ParseURL(ni.URL)
	if err != nil {
		return fmt.Errorf("error parsing the nar URL: %w", err)
	}

	if _, err := qtx.DeleteNarFileByHash(ctx, narURL.Hash); err != nil {
		return fmt.Errorf("error deleting the existing nar file record: %w", err)
	}

	if _, err := qtx.CreateNarFile(ctx, database.CreateNarFileParams{
		Hash:        narURL.Hash,
		Compression: narURL.Compression.String(),
		Query:       narURL.Query.Encode(),
		FileSize:    ni.NarSize,
	}); err != nil {
		if !database.IsDuplicateKeyError(err) {
			return fmt.Errorf("error creating the nar file record: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// Mock interface for helper.NarInfoFilePath used in other packages, here just for reference logic if needed.
var _ = helper.NarInfoFilePath
