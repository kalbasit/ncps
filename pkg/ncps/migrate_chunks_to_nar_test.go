package ncps_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/nixhash"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/ncps"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// setupChunkedNar prepares a CDC-chunked NAR via the real CLI path
// (migrate-narinfo then migrate-nar-to-chunks) and returns the app + db URL.
// The narinfo's recorded NarHash is left as testdata's literal value, which does
// NOT match the random NarText — callers that exercise the success path must fix
// it up to the true content hash first.
func setupChunkedNar(
	ctx context.Context, t *testing.T, dbClient *database.Client, dir, dbURL string,
) *cli.Command {
	t.Helper()

	narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

	narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
	require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

	ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
	require.NoError(t, err)
	require.NoError(t, testhelper.RegisterNarInfoAsUnmigrated(ctx, dbClient, testdata.Nar1.NarInfoHash, ni))

	app, err := ncps.New()
	require.NoError(t, err)

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-narinfo",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
		"--concurrency", "1",
	}))

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-nar-to-chunks",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
	}))

	var chunks int
	require.NoError(t, dbClient.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&chunks))
	require.Positive(t, chunks, "precondition: NAR should be chunked")

	return app
}

func fixupNarHash(ctx context.Context, t *testing.T, dbClient *database.Client) {
	t.Helper()

	sum := sha256.Sum256([]byte(testdata.Nar1.NarText))
	narHash := nixhash.MustNewHashWithEncoding(nixhash.SHA256, sum[:], nixhash.NixBase32, true).String()

	_, err := dbClient.Ent().NarInfo.Update().
		Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).
		SetNarHash(narHash).
		Save(ctx)
	require.NoError(t, err)
}

func countChunks(ctx context.Context, t *testing.T, dbClient *database.Client) int {
	t.Helper()

	var n int
	require.NoError(t, dbClient.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&n))

	return n
}

// TestMigrateChunksToNar_CLI_NothingToMigrate verifies that the command exits cleanly
// when no chunked NARs exist — regardless of whether cdc_enabled is in the database.
// This covers both the "CDC never used" case and the "drain complete" case.
func TestMigrateChunksToNar_CLI_NothingToMigrate(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	_, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)

	// No configureCDCInDatabase call — cdc_enabled absent from DB.
	// No setupChunkedNar call — no nar_file rows with total_chunks > 0.

	app, err := ncps.New()
	require.NoError(t, err)

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
	}))
}

func TestMigrateChunksToNar_CLI_Success(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	dbClient, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)
	configureCDCInDatabase(ctx, t, dbClient)

	app := setupChunkedNar(ctx, t, dbClient, dir, dbURL)
	fixupNarHash(ctx, t, dbClient)

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
	}))

	var totalChunks int
	require.NoError(t, dbClient.DB().QueryRowContext(ctx,
		"SELECT total_chunks FROM nar_files WHERE hash = ?", testdata.Nar1.NarHash).Scan(&totalChunks))
	assert.Zero(t, totalChunks, "nar_file should be flipped to whole-file")
	assert.Positive(t, countChunks(ctx, t, dbClient),
		"the default run leaves now-orphaned chunks for the GC (no --force-reclaim)")
}

func TestMigrateChunksToNar_CLI_ForceReclaim(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	dbClient, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)
	configureCDCInDatabase(ctx, t, dbClient)

	app := setupChunkedNar(ctx, t, dbClient, dir, dbURL)
	fixupNarHash(ctx, t, dbClient)

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
		"--force-reclaim",
	}))

	assert.Zero(t, countChunks(ctx, t, dbClient), "--force-reclaim must reclaim the now-orphaned chunks")
}

func TestMigrateChunksToNar_CLI_DryRunMakesNoChanges(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	dbClient, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)
	configureCDCInDatabase(ctx, t, dbClient)

	app := setupChunkedNar(ctx, t, dbClient, dir, dbURL)
	fixupNarHash(ctx, t, dbClient)

	before := countChunks(ctx, t, dbClient)

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
		"--dry-run",
	}))

	assert.Equal(t, before, countChunks(ctx, t, dbClient), "--dry-run must not delete any chunks")
}

//nolint:paralleltest // redirects os.Stdout and overrides global ticker interval; cannot run in parallel
func TestMigrateChunksToNar_CLI_ProgressLogEmitted(t *testing.T) {
	orig := *ncps.MigrateChunksToNarProgressIntervalForTest
	*ncps.MigrateChunksToNarProgressIntervalForTest = 1 * time.Millisecond

	t.Cleanup(func() { *ncps.MigrateChunksToNarProgressIntervalForTest = orig })

	// The CLI's getZeroLogger writes to os.Stdout; capture it via a pipe.
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = w

	t.Cleanup(func() {
		os.Stdout = oldStdout
		_ = r.Close()
	})

	ctx := context.Background()

	dbClient, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)
	configureCDCInDatabase(ctx, t, dbClient)

	app := setupChunkedNar(ctx, t, dbClient, dir, dbURL)
	fixupNarHash(ctx, t, dbClient)

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
	}))

	require.NoError(t, w.Close())

	var logBuf bytes.Buffer

	_, err = io.Copy(&logBuf, r)
	require.NoError(t, err)

	logged := logBuf.String()
	assert.Contains(t, logged, "migration progress", "expected at least one progress log line")
	assert.Contains(t, logged, `"total"`, "progress log must include total field")
	assert.Contains(t, logged, `"processed"`, "progress log must include processed field")
	assert.Contains(t, logged, `"succeeded"`, "progress log must include succeeded field")
	assert.Contains(t, logged, `"failed"`, "progress log must include failed field")
	assert.Contains(t, logged, `"skipped"`, "progress log must include skipped field")
	assert.Contains(t, logged, `"percent"`, "progress log must include percent field")
	assert.Contains(t, logged, `"elapsed"`, "progress log must include elapsed field")
	assert.Contains(t, logged, `"rate"`, "progress log must include rate field")
}

//nolint:paralleltest // redirects os.Stdout; cannot run in parallel
func TestMigrateChunksToNar_CLI_NoProgressLogOnEmptyRun(t *testing.T) {
	// The CLI's getZeroLogger writes to os.Stdout; capture it via a pipe.
	oldStdout := os.Stdout
	r, w, pipeErr := os.Pipe()
	require.NoError(t, pipeErr)

	os.Stdout = w

	t.Cleanup(func() {
		os.Stdout = oldStdout
		_ = r.Close()
	})

	ctx := context.Background()

	_, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)

	app, err := ncps.New()
	require.NoError(t, err)

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
	}))

	require.NoError(t, w.Close())

	var logBuf bytes.Buffer

	_, err = io.Copy(&logBuf, r)
	require.NoError(t, err)

	assert.NotContains(t, logBuf.String(), "migration progress", "no progress line expected when no chunked NARs exist")
}

func TestMigrateChunksToNar_CLI_HashMismatchFailsWithoutDestroyingData(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	dbClient, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)
	configureCDCInDatabase(ctx, t, dbClient)

	// No hash fixup: testdata's literal NarHash does not match the content, so
	// verification must fail and the NAR must be left chunked.
	app := setupChunkedNar(ctx, t, dbClient, dir, dbURL)

	before := countChunks(ctx, t, dbClient)

	err := app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
	})
	require.Error(t, err, "a hash mismatch must make the command exit non-zero")

	assert.Equal(t, before, countChunks(ctx, t, dbClient),
		"a NAR that fails verification must NOT have its chunks deleted")
}
