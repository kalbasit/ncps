package ncps

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
)

// ErrStorageIterationNotSupported is returned when the storage backend does not support iteration.
var ErrStorageIterationNotSupported = errors.New("storage backend does not support iteration")

// ErrMigrationFailed is returned when one or more NarInfos fail to migrate.
var ErrMigrationFailed = errors.New("narinfos failed to migrate")

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
			&cli.IntFlag{
				Name:    "concurrency",
				Usage:   "Number of concurrent migration workers",
				Value:   10,
				Sources: flagSources("concurrency", "CONCURRENCY"),
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

			// 3. Setup Migrated Hashes Map
			logger.Info().Msg("fetching existing narinfo hashes from the database")

			migratedHashes, err := db.GetMigratedNarInfoHashes(ctx)
			if err != nil {
				return fmt.Errorf("failed to fetch migrated hashes from database: %w", err)
			}

			migratedHashesMap := make(map[string]struct{}, len(migratedHashes))
			for _, hash := range migratedHashes {
				migratedHashesMap[hash] = struct{}{}
			}

			logger.Info().Int("count", len(migratedHashesMap)).Msg("loaded migrated hashes from database")

			// 4. Migrate
			logger.Info().Msg("starting migration")

			count := 0

			var errorsCount int32

			g, ctx := errgroup.WithContext(ctx)
			g.SetLimit(cmd.Int("concurrency"))

			err = walker.WalkNarInfos(ctx, func(hash string) error {
				count++

				if _, ok := migratedHashesMap[hash]; ok {
					if !deleteAfter {
						// Skip
						return nil
					}

					// We need to delete it from storage, but we skip the DB migration part.
					g.Go(func() error {
						log := logger.With().Str("hash", hash).Logger()
						log.Info().Msg("narinfo already migrated, deleting from storage")

						if dryRun {
							log.Info().Msg("[DRY-RUN] would delete from storage")

							return nil
						}

						if err := narInfoStore.DeleteNarInfo(ctx, hash); err != nil {
							log.Error().Err(err).Msg("failed to delete from store")

							atomic.AddInt32(&errorsCount, 1)
						}

						return nil
					})

					return nil
				}

				g.Go(func() error {
					log := logger.With().Str("hash", hash).Logger()
					log.Info().Msg("processing narinfo")

					ctxWithLog := log.WithContext(ctx)
					if err := migrateOne(ctxWithLog, db, narInfoStore, hash, dryRun, deleteAfter); err != nil {
						log.Error().Err(err).Msg("failed to migrate narinfo")

						atomic.AddInt32(&errorsCount, 1)
						// Continue migration even if one fails
						return nil
					}

					return nil
				})

				return nil
			})
			if err != nil {
				return err
			}

			if err := g.Wait(); err != nil {
				return err
			}

			logger.Info().Int("total", count).Int32("errors", atomic.LoadInt32(&errorsCount)).Msg("migration completed")

			if atomic.LoadInt32(&errorsCount) > 0 {
				return fmt.Errorf("%d %w", errorsCount, ErrMigrationFailed)
			}

			return nil
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
	ni *narinfo.NarInfo,
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
	nir, err := getOrCreateNarInfo(ctx, qtx, hash, ni)
	if err != nil {
		return err
	}

	// References
	if len(ni.References) > 0 {
		err := qtx.AddNarInfoReferences(ctx, database.AddNarInfoReferencesParams{
			NarInfoID: nir.ID,
			Reference: ni.References,
		})
		if err != nil {
			return fmt.Errorf("failed to add references: %w", err)
		}
	}

	// Signatures
	sigStrings := make([]string, len(ni.Signatures))
	for i, sig := range ni.Signatures {
		sigStrings[i] = sig.String()
	}

	if len(sigStrings) > 0 {
		err := qtx.AddNarInfoSignatures(ctx, database.AddNarInfoSignaturesParams{
			NarInfoID: nir.ID,
			Signature: sigStrings,
		})
		if err != nil {
			return fmt.Errorf("failed to add signatures: %w", err)
		}
	}

	// NarFile
	narURL, err := nar.ParseURL(ni.URL)
	if err != nil {
		return fmt.Errorf("error parsing the nar URL: %w", err)
	}

	narFile, err := getOrCreateNarFile(ctx, qtx, &narURL, ni.FileSize)
	if err != nil {
		return err
	}

	// Link NarInfo to NarFile
	if err := qtx.LinkNarInfoToNarFile(ctx, database.LinkNarInfoToNarFileParams{
		NarInfoID: nir.ID,
		NarFileID: narFile.ID,
	}); err != nil {
		if !database.IsDuplicateKeyError(err) {
			return fmt.Errorf("failed to link narinfo to narfile: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func getOrCreateNarInfo(
	ctx context.Context,
	qtx database.Querier,
	hash string,
	ni *narinfo.NarInfo,
) (database.NarInfo, error) {
	// First, try to get the record.
	existing, err := qtx.GetNarInfoByHash(ctx, hash)
	if err == nil {
		// Found it, return.
		zerolog.Ctx(ctx).Info().Msg("narinfo already in DB, skipping insert")

		return existing, nil
	}
	// If the error is anything other than "not found", it's a real error.
	if !errors.Is(err, sql.ErrNoRows) {
		return database.NarInfo{}, fmt.Errorf("failed to get narinfo record: %w", err)
	}

	// Not found, so let's create it.
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
		// If we get a duplicate key error, it means another worker created it between our GET and CREATE.
		if database.IsDuplicateKeyError(err) {
			zerolog.Ctx(ctx).Info().Msg("narinfo created by another worker, fetching again")
			// Fetch the record again. This time it should exist.
			existing, errGet := qtx.GetNarInfoByHash(ctx, hash)
			if errGet != nil {
				return database.NarInfo{}, fmt.Errorf("failed to get existing record after race: %w", errGet)
			}

			return existing, nil
		}
		// Another error occurred during creation.
		return database.NarInfo{}, fmt.Errorf("failed to create narinfo record: %w", err)
	}

	return nir, nil
}

func getOrCreateNarFile(
	ctx context.Context,
	qtx database.Querier,
	narURL *nar.URL,
	narSize uint64,
) (database.NarFile, error) {
	// First, try to get the record.
	existing, err := qtx.GetNarFileByHash(ctx, narURL.Hash)
	if err == nil {
		// Found it, return.
		return existing, nil
	}
	// If the error is anything other than "not found", it's a real error.
	if !errors.Is(err, sql.ErrNoRows) {
		return database.NarFile{}, fmt.Errorf("failed to get existing nar file record: %w", err)
	}

	// Not found, so let's create it.
	narFile, err := qtx.CreateNarFile(ctx, database.CreateNarFileParams{
		Hash:        narURL.Hash,
		Compression: narURL.Compression.String(),
		Query:       narURL.Query.Encode(),
		FileSize:    narSize,
	})
	if err != nil {
		// If we get a duplicate key error, it means another worker created it.
		if database.IsDuplicateKeyError(err) {
			// Fetch the record again. This time it should exist.
			existing, errGet := qtx.GetNarFileByHash(ctx, narURL.Hash)
			if errGet != nil {
				return database.NarFile{}, fmt.Errorf("failed to get existing nar file record after race: %w", errGet)
			}

			return existing, nil
		}
		// Another error occurred during creation.
		return database.NarFile{}, fmt.Errorf("error creating the nar file record: %w", err)
	}

	return narFile, nil
}
