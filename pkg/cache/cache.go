package cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/nix-community/go-nix/pkg/nixhash"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	entchunk "github.com/kalbasit/ncps/ent/chunk"
	entconfigentry "github.com/kalbasit/ncps/ent/configentry"
	entnarfile "github.com/kalbasit/ncps/ent/narfile"
	entnarfilechunk "github.com/kalbasit/ncps/ent/narfilechunk"
	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"
	entnarinfonarfile "github.com/kalbasit/ncps/ent/narinfonarfile"
	entnarinforeference "github.com/kalbasit/ncps/ent/narinforeference"
	entnarinfosignature "github.com/kalbasit/ncps/ent/narinfosignature"
	entpinnedclosure "github.com/kalbasit/ncps/ent/pinnedclosure"

	"github.com/kalbasit/ncps/ent"
	"github.com/kalbasit/ncps/pkg/analytics"
	"github.com/kalbasit/ncps/pkg/cache/healthcheck"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/chunker"
	"github.com/kalbasit/ncps/pkg/config"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/pkg/zstd"
)

const (
	recordAgeIgnoreTouch = 5 * time.Minute
	otelPackageName      = "github.com/kalbasit/ncps/pkg/cache"
	cacheLockKey         = "cache"

	// Buffer size of 16 allows multiple chunks to be fetched ahead while earlier
	// chunks are being copied, overlapping I/O latency with data transfer.
	prefetchBufferSize = 16

	// progressivePollBatchSize is the number of chunks to query at once when
	// polling for newly-available chunks in streamProgressiveChunks.
	// A larger value reduces the number of DB round-trips for large NARs,
	// which is critical for databases like MySQL under parallel test load.
	progressivePollBatchSize = 256

	// cdcCleanupHashBatchSize bounds the size of `WHERE hash IN (…)`
	// batches issued by the CDC delayed-cleanup job. Stays well under
	// driver parameter limits (Postgres ≤ 65535, modern SQLite ≤
	// 32766, older SQLite ≤ 999) while staying well above typical
	// chunked-NAR-hash counts so the job remains a single query for
	// almost every install.
	cdcCleanupHashBatchSize = 500

	// Migration operation constants for metrics.
	migrationOperationMigrate = "migrate"
	migrationOperationDelete  = "delete"

	// Migration result constants for metrics.
	migrationResultSuccess = "success"
	migrationResultFailure = "failure"
	migrationResultSkipped = "skipped"

	// Migration type constants for metrics.
	migrationTypeNarInfoToDB = "narinfo-to-db"
	migrationTypeNarToChunks = "nar-to-chunks"

	// cdcFirstBatchDelay is how long to wait before committing the first chunk batch to the
	// database. Keeping this short unblocks piggybacking clients on other instances quickly.
	cdcFirstBatchDelay = 100 * time.Millisecond

	// cdcSubsequentBatchDelay is how long to wait between subsequent chunk batch commits.
	cdcSubsequentBatchDelay = 500 * time.Millisecond

	// cdcMaxBatchSize is a safety cap to avoid unbounded memory accumulation if chunks
	// arrive faster than the timer fires.
	cdcMaxBatchSize = 100
)

// narInfoJobKey returns the key used for tracking narinfo download jobs.
func narInfoJobKey(hash string) string { return "download:narinfo:" + hash }

// narJobKey returns the key used for tracking NAR download jobs.
func narJobKey(hash string) string { return "download:nar:" + hash }

// narInfoLockKey returns the lock key used for narinfo operations.
func narInfoLockKey(hash string) string { return "narinfo:" + hash }

// migrationLockKey returns the lock key used for migration operations.
func migrationLockKey(hash string) string { return "migration:" + hash }

// errorLogLevelForContextErrors returns the appropriate log level for errors,
// downgrading context-related errors to debug level.
func errorLogLevelForContextErrors(err error) zerolog.Level {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return zerolog.DebugLevel
	}

	return zerolog.ErrorLevel
}

var (
	// ErrHostnameRequired is returned if the given hostName to New is not given.
	ErrHostnameRequired = errors.New("hostName is required")

	// ErrHostnameMustNotContainScheme is returned if the given hostName to New contained a scheme.
	ErrHostnameMustNotContainScheme = errors.New("hostName must not contain scheme")

	// ErrHostnameNotValid is returned if the given hostName to New is not valid.
	ErrHostnameNotValid = errors.New("hostName is not valid")

	// ErrHostnameMustNotContainPath is returned if the given hostName to New contained a path.
	ErrHostnameMustNotContainPath = errors.New("hostName must not contain a path")

	// ErrMigrationInProgress is returned if a migration is already in progress.
	ErrMigrationInProgress = errors.New("migration is already in progress")

	// errNarInfoPurged is returned if the narinfo was purged.
	errNarInfoPurged = errors.New("the narinfo was purged")

	// ErrCDCDisabled is returned when CDC is required but not enabled.
	ErrCDCDisabled = errors.New("CDC must be enabled and chunk store configured for migration")

	// ErrNarAlreadyChunked is returned when the nar is already chunked.
	ErrNarAlreadyChunked = errors.New("nar is already chunked")

	// ErrNarAlreadyWholeFile is returned by MigrateChunksToNar when the nar is
	// already stored as a whole file (nothing chunked to migrate back).
	ErrNarAlreadyWholeFile = errors.New("nar is already a whole file")

	// ErrNoNarHashToVerify is returned by MigrateChunksToNar when a chunked nar's
	// linked narinfo has no NarHash, so the reconstructed bytes cannot be
	// content-verified before de-chunking. Such nars are skipped, not migrated.
	ErrNoNarHashToVerify = errors.New("no narinfo NarHash to verify reconstructed nar against")

	// ErrNarHashMismatch is returned by MigrateChunksToNar when the bytes
	// reconstructed from chunks do not match the recorded NarHash or size.
	ErrNarHashMismatch = errors.New("reconstructed nar does not match recorded hash or size")

	errMissingChunkEdge = errors.New("nar_file_chunk is missing eager-loaded chunk edge")

	errChunkIDFetchMismatch = errors.New("chunk count mismatch after bulk insert")

	//nolint:gochecknoglobals
	meter metric.Meter

	//nolint:gochecknoglobals
	tracer trace.Tracer

	//nolint:gochecknoglobals
	narServedCount metric.Int64Counter

	//nolint:gochecknoglobals
	narInfoServedCount metric.Int64Counter

	//nolint:gochecknoglobals
	totalSizeMetric metric.Int64ObservableGauge

	// Object count metrics
	//nolint:gochecknoglobals
	narInfoCountMetric metric.Int64ObservableGauge

	//nolint:gochecknoglobals
	narFileCountMetric metric.Int64ObservableGauge

	// Cache eviction metrics
	//nolint:gochecknoglobals
	lruCleanupRunsTotal metric.Int64Counter

	//nolint:gochecknoglobals
	lruNarInfosEvictedTotal metric.Int64Counter

	//nolint:gochecknoglobals
	lruNarFilesEvictedTotal metric.Int64Counter

	//nolint:gochecknoglobals
	lruChunksEvictedTotal metric.Int64Counter

	//nolint:gochecknoglobals
	lruBytesFreedTotal metric.Int64Counter

	//nolint:gochecknoglobals
	lruCleanupDuration metric.Float64Histogram

	// Cache utilization metrics
	//nolint:gochecknoglobals
	cacheUtilizationRatio metric.Float64ObservableGauge

	//nolint:gochecknoglobals
	cacheMaxSizeBytes metric.Int64ObservableGauge

	// Upstream fetch duration metrics
	//nolint:gochecknoglobals
	upstreamNarInfoFetchDuration metric.Float64Histogram

	//nolint:gochecknoglobals
	upstreamNarFetchDuration metric.Float64Histogram

	// Background migration metrics
	//nolint:gochecknoglobals
	backgroundMigrationObjectsTotal metric.Int64Counter

	//nolint:gochecknoglobals
	backgroundMigrationDuration metric.Float64Histogram

	// Download coordination metrics
	//nolint:gochecknoglobals // package-level OTel metric instrument, initialized once in init() and reused.
	downloadCoordinationFallbackTotal metric.Int64Counter
)

//nolint:gochecknoinits
func init() {
	meter = otel.Meter(otelPackageName)
	tracer = otel.Tracer(otelPackageName)

	var err error

	narServedCount, err = meter.Int64Counter(
		"ncps_nar_served_total",
		metric.WithDescription("Counts the number of NAR files served."),
		metric.WithUnit("{file}"),
	)
	if err != nil {
		panic(err)
	}

	narInfoServedCount, err = meter.Int64Counter(
		"ncps_narinfo_served_total",
		metric.WithDescription("Counts the number of NAR info files served."),
		metric.WithUnit("{file}"),
	)
	if err != nil {
		panic(err)
	}

	totalSizeMetric, err = meter.Int64ObservableGauge(
		"ncps_store_total_size_bytes",
		metric.WithDescription("The total size of all NAR files in the store."),
		metric.WithUnit("By"),
	)
	if err != nil {
		panic(err)
	}

	// Initialize object count metrics
	narInfoCountMetric, err = meter.Int64ObservableGauge(
		"ncps_narinfo_count",
		metric.WithDescription("Number of narinfo objects currently in cache."),
		metric.WithUnit("{object}"),
	)
	if err != nil {
		panic(err)
	}

	narFileCountMetric, err = meter.Int64ObservableGauge(
		"ncps_nar_file_count",
		metric.WithDescription("Number of NAR file objects currently in cache."),
		metric.WithUnit("{object}"),
	)
	if err != nil {
		panic(err)
	}

	// Initialize cache eviction metrics
	lruCleanupRunsTotal, err = meter.Int64Counter(
		"ncps_lru_cleanup_runs_total",
		metric.WithDescription("Total number of LRU cleanup executions."),
		metric.WithUnit("{run}"),
	)
	if err != nil {
		panic(err)
	}

	lruNarInfosEvictedTotal, err = meter.Int64Counter(
		"ncps_lru_narinfos_evicted_total",
		metric.WithDescription("Total number of narinfos evicted by LRU."),
		metric.WithUnit("{object}"),
	)
	if err != nil {
		panic(err)
	}

	lruNarFilesEvictedTotal, err = meter.Int64Counter(
		"ncps_lru_nar_files_evicted_total",
		metric.WithDescription("Total number of NAR files evicted by LRU."),
		metric.WithUnit("{object}"),
	)
	if err != nil {
		panic(err)
	}

	lruChunksEvictedTotal, err = meter.Int64Counter(
		"ncps_lru_chunks_evicted_total",
		metric.WithDescription("Total number of chunks evicted by LRU."),
		metric.WithUnit("{object}"),
	)
	if err != nil {
		panic(err)
	}

	lruBytesFreedTotal, err = meter.Int64Counter(
		"ncps_lru_bytes_freed_total",
		metric.WithDescription("Total bytes freed by LRU eviction."),
		metric.WithUnit("By"),
	)
	if err != nil {
		panic(err)
	}

	lruCleanupDuration, err = meter.Float64Histogram(
		"ncps_lru_cleanup_duration_seconds",
		metric.WithDescription("Duration of LRU cleanup operations."),
		metric.WithUnit("s"),
	)
	if err != nil {
		panic(err)
	}

	// Initialize cache utilization metrics
	cacheUtilizationRatio, err = meter.Float64ObservableGauge(
		"ncps_cache_utilization_ratio",
		metric.WithDescription("Current cache size as a ratio of maximum size (0.0 to 1.0)."),
		metric.WithUnit("1"),
	)
	if err != nil {
		panic(err)
	}

	cacheMaxSizeBytes, err = meter.Int64ObservableGauge(
		"ncps_cache_max_size_bytes",
		metric.WithDescription("Configured maximum cache size in bytes."),
		metric.WithUnit("By"),
	)
	if err != nil {
		panic(err)
	}

	// Initialize upstream fetch duration metrics
	upstreamNarInfoFetchDuration, err = meter.Float64Histogram(
		"ncps_upstream_narinfo_fetch_duration_seconds",
		metric.WithDescription("Duration of narinfo fetches from upstream caches."),
		metric.WithUnit("s"),
	)
	if err != nil {
		panic(err)
	}

	upstreamNarFetchDuration, err = meter.Float64Histogram(
		"ncps_upstream_nar_fetch_duration_seconds",
		metric.WithDescription("Duration of NAR fetches from upstream caches."),
		metric.WithUnit("s"),
	)
	if err != nil {
		panic(err)
	}

	backgroundMigrationObjectsTotal, err = meter.Int64Counter(
		"ncps_background_migration_objects_total",
		metric.WithDescription("Total number of objects processed during background migration"),
		metric.WithUnit("{object}"),
	)
	if err != nil {
		panic(err)
	}

	backgroundMigrationDuration, err = meter.Float64Histogram(
		"ncps_background_migration_duration_seconds",
		metric.WithDescription("Duration of background object migration operations"),
		metric.WithUnit("s"),
	)
	if err != nil {
		panic(err)
	}

	downloadCoordinationFallbackTotal, err = meter.Int64Counter(
		"ncps_download_coordination_fallback_total",
		metric.WithDescription(
			"Counts download-lock contention fallbacks by outcome "+
				"(served_by_peer, take_over, give_up, caller_canceled).",
		),
		metric.WithUnit("{event}"),
	)
	if err != nil {
		panic(err)
	}
}

// Cache represents the main cache service.
type Cache struct {
	hostName      string
	secretKey     signature.SecretKey
	healthChecker *healthcheck.HealthChecker
	maxSize       uint64

	dbClient *database.Client

	// tempDir is used to store nar files temporarily.
	tempDir string
	// stores
	config *config.Config
	//nolint:staticcheck // deprecated: migration support
	configStore  storage.ConfigStore
	narInfoStore storage.NarInfoStore
	narStore     storage.NarStore
	chunkStore   chunk.Store

	// CDC configuration
	cdcMu      sync.RWMutex
	cdcEnabled bool
	chunker    chunker.Chunker

	// Lazy chunking configuration
	cdcLazyChunkingEnabled bool
	cdcBackgroundWorkers   int
	cdcDeleteDelay         time.Duration

	// Should the cache sign the narinfos?
	shouldSignNarinfo bool

	// recordAgeIgnoreTouch represents the duration at which a record is
	// considered up to date and a touch is not invoked. This helps avoid
	// repetitive touching of records in the database which are causing `database
	// is locked` errors
	recordAgeIgnoreTouch time.Duration

	// Lock abstraction (can be local or distributed)
	downloadLocker      lock.Locker
	cacheLocker         lock.RWLocker
	downloadLockTTL     time.Duration
	downloadPollTimeout time.Duration
	cacheLockTTL        time.Duration

	// chunkWaitTimeout bounds how long progressive CDC streaming waits for the
	// next chunk to be produced/become readable before treating the transfer as
	// failed. Defaults to defaultChunkWaitTimeout; operators on high-latency
	// storage can raise or lower it (and align it with their gateway timeout).
	chunkWaitTimeout time.Duration

	// upstreamJobs is used to store in-progress jobs for pulling nars from
	// upstream cache so incoming requests for the same nar can find and wait
	// for jobs. Protected by upstreamJobsMu for local synchronization.
	upstreamJobsMu sync.Mutex
	upstreamJobs   map[string]*downloadState
	cron           *cron.Cron
	// upstreamCachesMu protects upstreamCaches
	upstreamCachesMu sync.RWMutex
	upstreamCaches   []*upstream.Cache

	// Wait group to track background operations
	backgroundWG sync.WaitGroup
}

type downloadState struct {
	// Mutex and Condition is used to gate access to this downloadState as well as broadcast chunks
	mu   sync.Mutex
	cond *sync.Cond

	// Information about the asset being downloaded
	wg                  sync.WaitGroup // Tracks active readers streaming from the temp file
	cleanupWg           sync.WaitGroup // Tracks download completion to trigger cleanup
	cdcWg               sync.WaitGroup // Tracks CDC background goroutine; zero by default (non-CDC)
	closed              bool           // Indicates whether new readers are allowed (protected by mu)
	assetPath           string
	compressedAssetPath string // If non-empty, we are decompressing from here to assetPath
	bytesWritten        int64
	finalSize           int64
	tempFileCompression nar.CompressionType // Actual compression of bytes written to the temp file

	// Store any download errors in this field
	downloadError error

	// Track which upstream served this download (for metrics)
	upstreamHostname string

	// Channel to signal starting the pull and its completion
	done   chan struct{} // Signals download fully complete (including database updates)
	start  chan struct{} // Signals streaming can begin (temp file ready)
	stored chan struct{} // Signals asset is in final storage (for distributed lock release)

	doneOnce   sync.Once
	startOnce  sync.Once
	storedOnce sync.Once
}

func newDownloadState() *downloadState {
	ds := &downloadState{
		done:   make(chan struct{}),
		start:  make(chan struct{}),
		stored: make(chan struct{}),
	}

	ds.cond = sync.NewCond(&ds.mu)

	return ds
}

// fileAvailableReader is an io.Reader that reads from a file as bytes become available,
// blocking (via ds.cond.Wait) when the download is still in progress. Returns io.EOF
// once all expected bytes (ds.finalSize) have been consumed. Used to drive streaming
// decompression from a temp file while a download is still writing to it.
type fileAvailableReader struct {
	f      *os.File
	ds     *downloadState
	offset int64
	ctx    context.Context
}

func (r *fileAvailableReader) Read(p []byte) (int, error) {
	r.ds.mu.Lock()

	for r.offset >= r.ds.bytesWritten && r.ds.finalSize == 0 && r.ds.downloadError == nil {
		// Check before Wait so a broadcast that already fired is not missed.
		// sync.Cond has no memory: a Broadcast that arrives before Wait() is lost.
		if r.ctx != nil && r.ctx.Err() != nil {
			r.ds.mu.Unlock()

			return 0, r.ctx.Err()
		}

		r.ds.cond.Wait()
	}

	if r.ds.downloadError != nil {
		r.ds.mu.Unlock()

		return 0, r.ds.downloadError
	}

	if r.ds.finalSize != 0 && r.offset >= r.ds.finalSize {
		r.ds.mu.Unlock()

		return 0, io.EOF
	}

	available := r.ds.bytesWritten - r.offset

	r.ds.mu.Unlock()

	toRead := int64(len(p))
	if toRead > available {
		toRead = available
	}

	n, readErr := r.f.ReadAt(p[:toRead], r.offset)
	r.offset += int64(n)

	return n, readErr
}

// setError safely sets the download error with mutex protection.
func (ds *downloadState) setError(err error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	ds.downloadError = err
}

// getError safely retrieves the download error with mutex protection.
func (ds *downloadState) getError() error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	return ds.downloadError
}

// setUpstreamHostname safely sets the upstream hostname with mutex protection.
func (ds *downloadState) setUpstreamHostname(hostname string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	ds.upstreamHostname = hostname
}

// getUpstreamHostname safely retrieves the upstream hostname with mutex protection.
func (ds *downloadState) getUpstreamHostname() string {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	return ds.upstreamHostname
}

type contextKey string

const uploadOnlyKey contextKey = "upload_only"

// WithUploadOnly returns a context that instructs the cache to skip upstream checks.
func WithUploadOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, uploadOnlyKey, true)
}

// IsUploadOnly checks if the context specifies skipping upstream checks.
func IsUploadOnly(ctx context.Context) bool {
	val, ok := ctx.Value(uploadOnlyKey).(bool)

	return ok && val
}

// New returns a new Cache.
func New(
	ctx context.Context,
	hostName string,
	dbClient *database.Client,
	//nolint:staticcheck // deprecated: migration support
	configStore storage.ConfigStore,
	narInfoStore storage.NarInfoStore,
	narStore storage.NarStore,
	secretKeyPath string,
	downloadLocker lock.Locker,
	cacheLocker lock.RWLocker,
	downloadLockTTL time.Duration,
	downloadPollTimeout time.Duration,
	cacheLockTTL time.Duration,
) (*Cache, error) {
	c := &Cache{
		dbClient:             dbClient,
		config:               config.New(dbClient, cacheLocker),
		configStore:          configStore,
		narInfoStore:         narInfoStore,
		narStore:             narStore,
		shouldSignNarinfo:    true,
		downloadLocker:       downloadLocker,
		cacheLocker:          cacheLocker,
		downloadLockTTL:      downloadLockTTL,
		downloadPollTimeout:  downloadPollTimeout,
		cacheLockTTL:         cacheLockTTL,
		chunkWaitTimeout:     defaultChunkWaitTimeout,
		upstreamJobs:         make(map[string]*downloadState),
		upstreamCaches:       make([]*upstream.Cache, 0),
		recordAgeIgnoreTouch: recordAgeIgnoreTouch,
	}

	if err := c.validateHostname(hostName); err != nil {
		return c, err
	}

	c.hostName = hostName

	if err := c.setupSecretKey(ctx, secretKeyPath); err != nil {
		return c, fmt.Errorf("error setting up the secret key: %w", err)
	}

	// Configure metric callbacks
	if err := c.setupMetricCallbacks(); err != nil {
		return c, fmt.Errorf("error registering metric callback: %w", err)
	}

	c.healthChecker = healthcheck.New()

	// Set up health change notifications for dynamic management
	healthChangeCh := make(chan healthcheck.HealthStatusChange, 100)
	c.healthChecker.SetHealthChangeNotifier(healthChangeCh)

	// Start the health checker
	c.healthChecker.Start(ctx)

	// Start the health change processor
	analytics.SafeGo(ctx, func() {
		c.processHealthChanges(ctx, healthChangeCh)
	})

	return c, nil
}

// SetCDCConfiguration enables and configures CDC.
func (c *Cache) SetCDCConfiguration(enabled bool, minSize, avgSize, maxSize uint32) error {
	c.cdcMu.Lock()
	defer c.cdcMu.Unlock()

	c.cdcEnabled = enabled
	if enabled {
		var err error

		c.chunker, err = chunker.NewCDCChunker(minSize, avgSize, maxSize)
		if err != nil {
			return fmt.Errorf("failed to create CDC chunker: %w", err)
		}
	}

	return nil
}

// SetChunkStore sets the chunk store.
func (c *Cache) SetChunkStore(cs chunk.Store) {
	c.cdcMu.Lock()
	defer c.cdcMu.Unlock()

	c.chunkStore = cs
}

// SetCDCLazyChunking configures lazy chunking behavior.
func (c *Cache) SetCDCLazyChunking(enabled bool, workers int) {
	c.cdcMu.Lock()
	defer c.cdcMu.Unlock()

	c.cdcLazyChunkingEnabled = enabled
	c.cdcBackgroundWorkers = workers
}

// GetCDCLazyChunkingEnabled returns whether lazy chunking is enabled.
func (c *Cache) GetCDCLazyChunkingEnabled() bool {
	c.cdcMu.RLock()
	defer c.cdcMu.RUnlock()

	return c.cdcLazyChunkingEnabled
}

// GetCDCBackgroundWorkers returns the number of background workers for lazy chunking.
func (c *Cache) GetCDCBackgroundWorkers() int {
	c.cdcMu.RLock()
	defer c.cdcMu.RUnlock()

	return c.cdcBackgroundWorkers
}

// SetCDCDeleteDelay sets the delay before deleting compressed NAR files after chunking.
func (c *Cache) SetCDCDeleteDelay(delay time.Duration) {
	c.cdcMu.Lock()
	defer c.cdcMu.Unlock()

	c.cdcDeleteDelay = delay
}

// GetCDCDeleteDelay returns the delay before deleting compressed NAR files after chunking.
func (c *Cache) GetCDCDeleteDelay() time.Duration {
	c.cdcMu.RLock()
	defer c.cdcMu.RUnlock()

	return c.cdcDeleteDelay
}

func (c *Cache) setupMetricCallbacks() error {
	_, err := meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		// Observe total size: SUM(file_size) over nar_files (0 when the
		// table is empty). totalSize is reused below for the cache
		// utilization ratio, so it stays in the callback scope.
		totalSize, err := totalNarFileSize(ctx, c.dbClient.Ent().NarFile)
		if err != nil {
			zerolog.Ctx(ctx).
				Warn().
				Err(err).
				Msg("failed to get total nar size for metrics")
		} else {
			o.ObserveInt64(totalSizeMetric, totalSize)
		}

		// Observe narinfo count
		narInfoCount, err := c.dbClient.Ent().NarInfo.Query().Count(ctx)
		if err != nil {
			zerolog.Ctx(ctx).
				Warn().
				Err(err).
				Msg("failed to get narinfo count for metrics")
		} else {
			o.ObserveInt64(narInfoCountMetric, int64(narInfoCount))
		}

		// Observe nar file count
		narFileCount, err := c.dbClient.Ent().NarFile.Query().Count(ctx)
		if err != nil {
			zerolog.Ctx(ctx).
				Warn().
				Err(err).
				Msg("failed to get nar file count for metrics")
		} else {
			o.ObserveInt64(narFileCountMetric, int64(narFileCount))
		}

		// Observe cache max size (static value)
		//nolint:gosec // G115: Cache max size is configured and unlikely to exceed int64 max (9.2 exabytes)
		o.ObserveInt64(cacheMaxSizeBytes, int64(c.maxSize))

		// Observe cache utilization ratio
		if c.maxSize > 0 && totalSize > 0 {
			utilizationRatio := float64(totalSize) / float64(c.maxSize)
			o.ObserveFloat64(cacheUtilizationRatio, utilizationRatio)
		} else {
			o.ObserveFloat64(cacheUtilizationRatio, 0.0)
		}

		return nil
	}, totalSizeMetric, narInfoCountMetric, narFileCountMetric, cacheMaxSizeBytes, cacheUtilizationRatio)
	if err != nil {
		return err
	}

	return c.RegisterUpstreamMetrics(meter)
}

// SetTempDir sets the temporary directory.
func (c *Cache) SetTempDir(d string) error {
	if err := helper.EnsureDirWritable(d); err != nil {
		return err
	}

	c.tempDir = d

	return nil
}

// AddUpstreamCaches adds one or more upstream caches with lazy loading support.
func (c *Cache) AddUpstreamCaches(ctx context.Context, ucs ...*upstream.Cache) {
	hostnames := make([]string, 0, len(ucs))

	for _, uc := range ucs {
		hostnames = append(hostnames, uc.GetHostname())
	}

	zerolog.Ctx(ctx).
		Debug().
		Strs("hostnames", hostnames).
		Msg("adding upstream caches")

	c.upstreamCachesMu.Lock()
	c.upstreamCaches = append(c.upstreamCaches, ucs...)
	c.upstreamCachesMu.Unlock()
	c.healthChecker.AddUpstreams(ucs)
}

// RegisterUpstreamMetrics register metrics related to upstream caches.
func (c *Cache) RegisterUpstreamMetrics(m metric.Meter) error {
	totalGauge, err := m.Int64ObservableGauge(
		"ncps_upstream_count_total",
		metric.WithDescription("Total number of configured upstream caches"),
		metric.WithUnit("{upstream}"),
	)
	if err != nil {
		return fmt.Errorf("failed to create total upstream gauge: %w", err)
	}

	healthyGauge, err := m.Int64ObservableGauge(
		"ncps_upstream_count_healthy",
		metric.WithDescription("Number of healthy upstream caches"),
		metric.WithUnit("{upstream}"),
	)
	if err != nil {
		return fmt.Errorf("failed to create healthy upstream gauge: %w", err)
	}

	_, err = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		c.upstreamCachesMu.RLock()
		defer c.upstreamCachesMu.RUnlock()

		// Observe Total
		o.ObserveInt64(totalGauge, int64(len(c.upstreamCaches)))

		// Observe Healthy
		o.ObserveInt64(healthyGauge, int64(c.GetHealthyUpstreamCountLocked()))

		return nil
	}, totalGauge, healthyGauge)

	return err
}

// GetHealthyUpstreamCount returns the number of healthy upstream caches.
func (c *Cache) GetHealthyUpstreamCount() int {
	c.upstreamCachesMu.RLock()
	defer c.upstreamCachesMu.RUnlock()

	return c.GetHealthyUpstreamCountLocked()
}

// GetHealthyUpstreamCountLocked returns the number of healthy upstream caches.
// It assumes the caller holds at least a read lock on upstreamCachesMu.
func (c *Cache) GetHealthyUpstreamCountLocked() int {
	var count int

	for _, u := range c.upstreamCaches {
		if u.IsHealthy() {
			count++
		}
	}

	return count
}

// GetHealthChecker returns the instance of haelth checker used by the cache.
// It's useful for testing the behavior of ncps.
func (c *Cache) GetHealthChecker() *healthcheck.HealthChecker {
	return c.healthChecker
}

// GetConfig returns the configuration instance.
// It's useful for testing the behavior of ncps.
func (c *Cache) GetConfig() *config.Config {
	return c.config
}

// SetCacheSignNarinfo configure ncps to sign or not sign narinfos.
func (c *Cache) SetCacheSignNarinfo(shouldSignNarinfo bool) { c.shouldSignNarinfo = shouldSignNarinfo }

// SetMaxSize sets the maxsize of the cache. This will be used by the LRU
// cronjob to automatically clean-up the store.
func (c *Cache) SetMaxSize(maxSize uint64) { c.maxSize = maxSize }

func (c *Cache) isCDCEnabled() bool {
	c.cdcMu.RLock()
	defer c.cdcMu.RUnlock()

	return c.cdcEnabled && c.chunkStore != nil
}

func (c *Cache) getChunkStore() chunk.Store {
	c.cdcMu.RLock()
	defer c.cdcMu.RUnlock()

	return c.chunkStore
}

func (c *Cache) getCDCInfo() (bool, chunk.Store, chunker.Chunker) {
	c.cdcMu.RLock()
	defer c.cdcMu.RUnlock()

	return c.cdcEnabled, c.chunkStore, c.chunker
}

// SetupCron creates a cron instance in the cache.
func (c *Cache) SetupCron(ctx context.Context, timezone *time.Location) {
	var opts []cron.Option
	if timezone != nil {
		opts = append(opts, cron.WithLocation(timezone))
	}

	c.cron = cron.New(opts...)

	zerolog.Ctx(ctx).
		Info().
		Msg("cron setup complete")
}

// AddLRUCronJob adds a job for LRU.
func (c *Cache) AddLRUCronJob(ctx context.Context, schedule cron.Schedule) {
	zerolog.Ctx(ctx).
		Info().
		Time("next-run", schedule.Next(time.Now())).
		Msg("adding a cronjob for LRU")

	c.cron.Schedule(schedule, cron.FuncJob(c.runLRU(ctx)))
}

// AddCDCDeletedCleanupCronJob adds a periodic job to delete old compressed NAR files
// after chunking is complete and the delay has passed.
func (c *Cache) AddCDCDeletedCleanupCronJob(ctx context.Context, schedule cron.Schedule) {
	zerolog.Ctx(ctx).
		Info().
		Time("next-run", schedule.Next(time.Now())).
		Msg("adding a cronjob for CDC delayed cleanup")

	c.cron.Schedule(schedule, cron.FuncJob(c.runCDCDeletedCleanup(ctx)))
}

// AddCDCLazyRecoveryCronJob adds a periodic job to recover stuck NAR files
// that failed to chunk due to restart or other issues.
func (c *Cache) AddCDCLazyRecoveryCronJob(
	ctx context.Context,
	schedule cron.Schedule,
	batchSize int,
) {
	zerolog.Ctx(ctx).
		Info().
		Time("next-run", schedule.Next(time.Now())).
		Int("batch_size", batchSize).
		Msg("adding a cronjob for CDC lazy recovery")

	c.cron.Schedule(schedule, cron.FuncJob(c.runCDCLazyRecovery(ctx, schedule, batchSize)))
}

// StartCron starts the cron scheduler in its own go-routine, or no-op if already started.
func (c *Cache) StartCron(ctx context.Context) {
	zerolog.Ctx(ctx).
		Info().
		Msg("starting the cron scheduler")

	c.cron.Start()
}

// Close waits for all background operations to complete.
func (c *Cache) Close() {
	// Stop the cron first and wait for any in-flight scheduled job (e.g. CDC lazy
	// recovery) to return: cron.Stop() prevents new scheduling and returns a context
	// that completes once running jobs finish. This guarantees no scheduled job can
	// enqueue more backgroundWG-tracked work (BackgroundMigrateNarToChunks) after we
	// begin draining, avoiding an Add-concurrent-with-Wait race on backgroundWG.
	if c.cron != nil {
		<-c.cron.Stop().Done()
	}

	c.backgroundWG.Wait()
}

// SetRecordAgeIgnoreTouch changes the duration at which a record is considered
// up to date and a touch is not invoked.
func (c *Cache) SetRecordAgeIgnoreTouch(d time.Duration) { c.recordAgeIgnoreTouch = d }

// GetHostname returns the hostname.
func (c *Cache) GetHostname() string { return c.hostName }

// PublicKey returns the public key of the server.
func (c *Cache) PublicKey() signature.PublicKey { return c.secretKey.ToPublicKey() }

// GetNar returns the nar given a hash and compression from the store. If the
// nar is not found in the store, it's pulled from an upstream, stored in the
// stored and finally returned. The returned narURL reflects any mutations made
// during serving (e.g. TransparentZstd cleared when zstd stream not available).
// NOTE: It's the caller responsibility to close the body.
func (c *Cache) GetNar(ctx context.Context, narURL nar.URL) (nar.URL, int64, io.ReadCloser, error) {
	ctx, span := tracer.Start(
		ctx,
		"cache.GetNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
		),
	)
	defer span.End()

	var metricAttrs []attribute.KeyValue

	defer func() {
		narServedCount.Add(ctx, 1, metric.WithAttributes(metricAttrs...))
	}()

	var (
		size   int64
		reader io.ReadCloser
	)

	err := c.withReadLock(ctx, "GetNar", narJobKey(narURL.Hash), func() error {
		ctx = narURL.
			NewLogger(*zerolog.Ctx(ctx)).
			WithContext(ctx)

		hasNarInStore := c.HasNarInStore(ctx, narURL)

		c.upstreamJobsMu.Lock()
		_, hasActiveLocalJob := c.upstreamJobs[narJobKey(narURL.Hash)]
		c.upstreamJobsMu.Unlock()

		// hasNar decides whether we can serve immediately (whole-file in store, fully
		// chunked, or chunking actively in progress) versus falling through to a
		// download. isServable is the single source of truth; see its doc comment.
		// A backing-less placeholder row is therefore never served — it falls through
		// to prePullNar below and re-downloads instead of returning a 404.
		hasNar, err := c.isServable(ctx, narURL)
		if err != nil {
			return err
		}

		// When a local download job is already active, prefer the faster temp-file
		// streaming path (prePullNar below) over progressive CDC streaming. A NAR that
		// is only "actively chunking" (not in store and not yet fully chunked) must not
		// count as servable here, so re-evaluate without the active-chunking term.
		if hasActiveLocalJob && !hasNarInStore {
			hasNar, err = c.HasNarInChunks(ctx, narURL)
			if err != nil {
				return fmt.Errorf("failed to check if nar exists in chunks: %w", err)
			}
		}

		if hasNar {
			if hasNarInStore {
				c.maybeBackgroundMigrateNarToChunks(ctx, narURL)
			}

			size, reader, err = c.serveNarFromStorageViaPipe(ctx, &narURL, hasNarInStore)
			if err != nil {
				metricAttrs = append(metricAttrs, attribute.String("status", "error"))
			} else {
				metricAttrs = append(metricAttrs, attribute.String("status", "success"))
			}

			return err
		}

		// If the artifact is not in the DB or Store, check if we are in "Upload Only" mode.
		// If so, we return ErrNotFound immediately to let the client know we don't have it locally,
		// triggering the PUT (push) operation.
		if IsUploadOnly(ctx) {
			metricAttrs = append(metricAttrs, attribute.String("result", "miss"))

			return storage.ErrNotFound
		}

		zerolog.Ctx(ctx).
			Debug().
			Msg("pulling nar in a go-routine and will stream the file back to the client")

		// Look up the original NAR URL from the narinfo in the database.
		// The client requests the normalized hash, but the upstream may require
		// the original (prefixed) hash (e.g., nix-serve style upstreams).
		narURL = c.lookupOriginalNarURL(ctx, narURL)

		// For CDC mode, narURL still has CompressionTypeNone after lookupOriginalNarURL
		// because nar_files records don't exist yet for first pulls.
		// To avoid downloading uncompressed NARs from upstream (slow TTFB due to
		// on-the-fly decompression), look up the original compressed URL from upstream.
		// We use this as a "preferred download URL" while keeping narURL as noneURL
		// so the decompressor path in the streaming goroutine triggers correctly
		// (it checks narURL.Compression == none).
		preferredUpstreamURL, ni := c.lookupPreferredUpstreamURL(ctx, narURL)
		detachedCtx := context.WithoutCancel(ctx)
		narURLCopy := narURL
		ds := c.prePullNar(ctx, detachedCtx, &narURLCopy, preferredUpstreamURL, nil, ni)

		// Check if download is complete (closed=true) before adding to WaitGroup
		// This prevents race with cleanup goroutine calling ds.wg.Wait()
		ds.mu.Lock()

		canStream := !ds.closed
		if canStream {
			ds.wg.Add(1)
		}

		ds.mu.Unlock()

		hasNarInStore = c.HasNarInStore(ctx, narURL)

		// If download is complete (canStream=false) or NAR is already in store, serve from storage.
		// When canStream=true (an active download is in progress), always stream from the temp file
		// so the client gets bytes as they download — without waiting for CDC chunking to finish.
		// Cross-server CDC coordination (progressive streaming) is handled by the !canStream path:
		// coordinateDownload returns a completed ds when hasAsset() is true (HasNarFileRecord),
		// so concurrent servers will enter getNarFromChunks → streamProgressiveChunks correctly.
		if !canStream || hasNarInStore {
			if canStream {
				ds.wg.Done()
			}

			metricAttrs = append(
				metricAttrs,
				attribute.String("result", "hit"),
				attribute.String("status", "success"),
			)

			var err error

			size, reader, err = c.serveNarFromStorageViaPipe(ctx, &narURL, hasNarInStore)
			if err != nil {
				metricAttrs = append(metricAttrs, attribute.String("status", "error"))
			}

			return err
		}

		metricAttrs = append(
			metricAttrs,
			attribute.String("result", "miss"),
			attribute.String("status", "success"),
		)

		select {
		case <-ds.start:
			// Download has started. Update the requested NAR URL to match what's
			// actually being streamed from the temp file.
			narURL.Compression = ds.tempFileCompression
		case <-ctx.Done():
			// Context canceled before download started
			metricAttrs = append(metricAttrs, attribute.String("status", "error"))

			ds.wg.Done()

			return ctx.Err()
		}

		err = ds.getError()
		if err != nil {
			metricAttrs = append(metricAttrs, attribute.String("status", "error"))

			// Add upstream hostname to metrics even on error
			if upstreamHostname := ds.getUpstreamHostname(); upstreamHostname != "" {
				metricAttrs = append(metricAttrs,
					attribute.String("upstream_hostname", upstreamHostname))
			}

			ds.wg.Done()

			return err
		}

		// Add upstream hostname to metrics on success
		if upstreamHostname := ds.getUpstreamHostname(); upstreamHostname != "" {
			metricAttrs = append(metricAttrs,
				attribute.String("upstream_hostname", upstreamHostname))
		}

		// create a pipe to stream file down to the http client
		r, writer := io.Pipe()

		analytics.SafeGo(ctx, func() {
			defer ds.wg.Done()
			defer writer.Close()

			// tempFileCompression is safe to read without a lock because it is set
			// before ds.start is closed, and this goroutine runs after <-ds.start.
			tempFileCompression := ds.tempFileCompression

			// If the client's context is cancelled while this goroutine is blocked
			// in cond.Wait, the watcher below broadcasts to wake it immediately.
			// Without this, a cancelled client can hold the goroutine open until
			// the next data broadcast from the download goroutine. See ncps #1252.
			watcherDone := make(chan struct{})
			defer close(watcherDone)

			go func() {
				select {
				case <-ctx.Done():
					ds.cond.Broadcast()
				case <-watcherDone:
				}
			}()

			// When the temp file holds compressed data but the client expects
			// uncompressed bytes (CDC-normalized URL), decompress on-the-fly.
			// This happens when GetNar piggybacks on a download started by
			// prePullNarInfo, which always downloads the upstream compression format
			// (e.g. xz) for faster TTFB and better CDC chunk deduplication.
			if tempFileCompression != nar.CompressionTypeNone &&
				narURL.Compression == nar.CompressionTypeNone {
				f, err := os.Open(ds.assetPath)
				if err != nil {
					zerolog.Ctx(ctx).Error().Err(err).Msg("error opening asset path for decompression")

					return
				}

				defer f.Close()

				fileReader := &fileAvailableReader{f: f, ds: ds, ctx: ctx}

				decompReader, err := nar.DecompressReader(ctx, fileReader, tempFileCompression)
				if err != nil {
					zerolog.Ctx(ctx).Error().Err(err).
						Str("compression", tempFileCompression.String()).
						Msg("error creating decompression reader for streaming")

					return
				}

				defer decompReader.Close()

				if _, err := io.Copy(writer, decompReader); err != nil {
					zerolog.Ctx(ctx).Error().Err(err).Msg("error streaming decompressed bytes to client")

					return
				}

				// Wait for the asset to be recorded in storage before completing.
				select {
				case <-ds.stored:
					// Asset successfully stored.
				case <-ds.done:
					// Download completed — check for errors.
					if err := ds.getError(); err != nil {
						zerolog.Ctx(ctx).Warn().
							Err(err).
							Str("nar_url", narURL.String()).
							Msg("download completed with error during decompressed streaming")
					}
				case <-ctx.Done():
					zerolog.Ctx(ctx).Debug().
						Str("nar_url", narURL.String()).
						Msg("client context cancelled while waiting for NAR storage (decompress path)")
				}

				return
			}

			var f *os.File

			var bytesSent int64

			for {
				ds.mu.Lock()

				for bytesSent >= ds.bytesWritten && ds.finalSize == 0 {
					ds.cond.Wait() // Put this goroutine to sleep until a broadcast is received from the downloader

					// Exit on download error or client context cancellation so the
					// goroutine does not block indefinitely. The watcher goroutine above
					// calls cond.Broadcast when ctx is cancelled, ensuring this loop
					// wakes promptly rather than waiting for the next data broadcast.
					if ds.downloadError != nil || ctx.Err() != nil {
						ds.mu.Unlock()

						return
					}
				}

				// On first read, open the file. It's not guaranteed the file is even
				// available prior to this point.
				if f == nil {
					var err error

					f, err = os.Open(ds.assetPath)
					if err != nil {
						zerolog.Ctx(ctx).
							Error().
							Err(err).
							Msg("error opening the asset path")

						ds.mu.Unlock()

						return
					}
				}

				// Determine how much data is now available to read
				bytesToRead := ds.bytesWritten - bytesSent
				isDownloadComplete := ds.finalSize != 0

				ds.mu.Unlock() // Unlock while doing I/O

				// If there's data to read, read it from the file
				if bytesToRead > 0 {
					// Use io.LimitReader to only read the new chunk
					lr := io.LimitReader(f, bytesToRead)

					n, err := io.Copy(writer, lr)
					if err != nil {
						zerolog.Ctx(ctx).
							Error().
							Err(err).
							Msg("error writing the response to the client")

						return
					}

					bytesSent += n
				}

				if isDownloadComplete && bytesSent >= ds.finalSize {
					// Wait for the asset to be fully stored before closing the stream
					// This avoids a race condition where the client finishes reading
					// but the asset is not yet in storage (HasNar would return false).
					select {
					case <-ds.stored:
						// Asset successfully stored
					case <-ds.done:
						// Download completed - check for errors
						if err := ds.getError(); err != nil {
							zerolog.Ctx(ctx).Warn().
								Err(err).
								Str("nar_url", narURL.String()).
								Msg("download completed with error during streaming")
						}
					case <-ctx.Done():
						// Client cancelled — all bytes were already delivered; skip
						// waiting for storage so this goroutine does not block cleanup.
						zerolog.Ctx(ctx).Debug().
							Str("nar_url", narURL.String()).
							Msg("client context cancelled while waiting for NAR storage")
					}

					return
				}
			}
		})

		size = -1
		reader = r

		return nil
	})
	if err != nil {
		return narURL, 0, nil, err
	}

	return narURL, size, reader, nil
}

// GetNarFileSize returns the size of the NAR file from the database if it exists.
func (c *Cache) GetNarFileSize(ctx context.Context, nu nar.URL) (int64, error) {
	ctx, span := tracer.Start(
		ctx,
		"cache.GetNarFileSize",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", nu.String()),
		),
	)
	defer span.End()

	nr, err := c.getNarFileFromDB(ctx, c.dbClient.Ent().NarFile, nu)
	if err != nil {
		if !database.IsNotFoundError(err) {
			zerolog.Ctx(ctx).Error().Err(err).Msg("error querying nar file size from database")
		}

		return 0, err
	}

	zerolog.Ctx(ctx).
		Debug().
		Uint64("file_size", nr.FileSize).
		Msg("got nar file size from database")

	//nolint:gosec // G115: File size is non-negative
	return int64(nr.FileSize), nil
}

// lookupOriginalNarURL looks up the original (potentially prefixed) NAR URL from the database
// by matching the narinfo that references a nar_file with the given hash.
// This is used to recover the original upstream URL (with prefix) when fetching from
// nix-serve style upstreams that use prefixed URLs (e.g., narinfohash-narhash).
func (c *Cache) lookupOriginalNarURL(ctx context.Context, normalizedNarURL nar.URL) nar.URL {
	ni, err := c.dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.HasNarInfoNarFilesWith(
			entnarinfonarfile.HasNarFileWith(
				entnarfile.HashEQ(normalizedNarURL.Hash),
				entnarfile.CompressionEQ(normalizedNarURL.Compression.String()),
				entnarfile.QueryEQ(normalizedNarURL.Query.Encode()),
			),
		)).
		First(ctx)
	if err != nil {
		// Not found is an expected case. We should log any other database errors.
		if !database.IsNotFoundError(err) {
			zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to lookup original nar URL")
		}

		return normalizedNarURL
	}

	if ni.URL != nil && *ni.URL != "" {
		originalURL, parseErr := nar.ParseURL(*ni.URL)
		if parseErr == nil {
			return originalURL
		}
		// Log if we have a URL in the DB but can't parse it.
		zerolog.Ctx(ctx).Warn().Err(parseErr).Str("url", *ni.URL).Msg("Failed to parse original nar URL from DB")
	}

	// If parsing fails or URL is invalid/empty, return the normalized URL unchanged
	return normalizedNarURL
}

// lookupPreferredUpstreamURL returns the original compressed URL for a CDC NAR
// (e.g. the xz URL) by looking up the narinfo hash in the DB and fetching the
// Returns nil, nil if CDC is not enabled, there is an active local download, or the
// original URL cannot be found.
func (c *Cache) lookupPreferredUpstreamURL(ctx context.Context, narURL nar.URL) (*nar.URL, *narinfo.NarInfo) {
	if !c.isCDCEnabled() || narURL.Compression != nar.CompressionTypeNone || c.hasUpstreamJob(narURL.Hash) {
		return nil, nil
	}

	ni, err := c.dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.URL(narURL.String())).
		First(ctx)
	if err != nil {
		if !database.IsNotFoundError(err) {
			zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to lookup narinfo hash by nar URL")
		}

		return nil, nil
	}

	if ni.Hash == "" {
		return nil, nil
	}

	_, upstreamNarInfo, err := c.getNarInfoFromUpstream(ctx, ni.Hash)
	if err != nil || upstreamNarInfo == nil {
		return nil, nil
	}

	originalURL, err := nar.ParseURL(upstreamNarInfo.URL)
	if err != nil {
		zerolog.Ctx(ctx).
			Warn().
			Err(err).
			Str("url", upstreamNarInfo.URL).
			Msg("Failed to parse preferred upstream nar URL")

		return nil, upstreamNarInfo
	}

	if originalURL.Compression == nar.CompressionTypeNone {
		return nil, upstreamNarInfo
	}

	return &originalURL, upstreamNarInfo
}

// ensureNarFileRecord ensures a NarFile record exists with the correct size.
// It creates the record if it doesn't exist, or updates the size if it's incorrect.
func (c *Cache) ensureNarFileRecord(ctx context.Context, narURL nar.URL, written int64, txName string) error {
	zerolog.Ctx(ctx).Debug().
		Str("hash", narURL.Hash).
		Str("compression", narURL.Compression.String()).
		Int64("written", written).
		Msg("ensureNarFileRecord: starting transaction")

	//nolint:gosec // G115: conversion is safe because size is non-negative
	fileSize := uint64(written)

	return c.withEntTransaction(ctx, txName, func(tx *ent.Tx) error {
		// Insert; on (hash, compression, query) conflict, overwrite
		// the file_size (and touch updated_at) to match the legacy
		// CreateNarFile+UpdateNarFileFileSize sequence. TotalChunks
		// stays at its existing value on conflict — only set to 0 on
		// fresh insert (the Create's default).
		id, err := tx.NarFile.Create().
			SetHash(narURL.Hash).
			SetCompression(narURL.Compression.String()).
			SetQuery(narURL.Query.Encode()).
			SetFileSize(fileSize).
			OnConflictColumns(
				entnarfile.FieldHash,
				entnarfile.FieldCompression,
				entnarfile.FieldQuery,
			).
			Update(func(u *ent.NarFileUpsert) {
				u.SetFileSize(fileSize)
				u.SetUpdatedAt(time.Now())
			}).
			ID(ctx)
		if err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Str("hash", narURL.Hash).
				Str("compression", narURL.Compression.String()).
				Msg("ensureNarFileRecord: CreateNarFile failed")

			return err
		}

		zerolog.Ctx(ctx).Debug().
			Int("nar_file_id", id).
			Str("hash", narURL.Hash).
			Str("compression", narURL.Compression.String()).
			Msg("ensureNarFileRecord: CreateNarFile succeeded")

		return nil
	})
}

// PutNar records the NAR (given as an io.Reader) into the store.
func (c *Cache) PutNar(ctx context.Context, narURL nar.URL, r io.ReadCloser) error {
	ctx, span := tracer.Start(
		ctx,
		"cache.PutNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
		),
	)
	defer span.End()

	return c.withReadLock(ctx, "PutNar", narJobKey(narURL.Hash), func() error {
		// TODO: The context already has these keys from the server (caller), should this be removed?
		ctx = narURL.
			NewLogger(*zerolog.Ctx(ctx)).
			WithContext(ctx)

		defer func() {
			// It's important to read the entire body to allow connection reuse.
			_, _ = io.Copy(io.Discard, r)
			r.Close()
		}()

		if c.isCDCEnabled() {
			return c.putNarWithCDC(ctx, narURL, r)
		}

		written, err := c.narStore.PutNar(ctx, narURL, r, -1)
		if err != nil {
			if errors.Is(err, storage.ErrAlreadyExists) {
				zerolog.Ctx(ctx).Debug().Msg("nar already exists in storage, getting size to ensure db record")

				// We still need the size to ensure the DB record is correct.
				var getErr error

				var reader io.ReadCloser

				written, reader, getErr = c.narStore.GetNar(ctx, narURL)
				if getErr != nil {
					return fmt.Errorf("nar exists in storage but failed to get its metadata: %w", getErr)
				}

				reader.Close()
			} else {
				return err
			}
		}

		// Ensure we have a NarFile record for it.
		// fileSize is 'written'.
		err = c.ensureNarFileRecord(ctx, narURL, written, "PutNar.ensureNarFile")
		if err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Msg("failed to ensure nar file record in PutNar")

			return err
		}

		if err := c.checkAndFixNarInfosForNar(context.WithoutCancel(ctx), narURL); err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Msg("failed to fix narinfos after PutNar")
		}

		return nil
	})
}

func (c *Cache) putNarWithCDC(ctx context.Context, narURL nar.URL, r io.Reader) error {
	f, err := os.CreateTemp(c.tempDir, fmt.Sprintf("%s-*.nar", filepath.Base(narURL.Hash)))
	if err != nil {
		return fmt.Errorf("failed to create temp file for CDC: %w", err)
	}

	tempPath := f.Name()
	defer os.Remove(tempPath)

	_, err = io.Copy(f, r)
	f.Close()

	if err != nil {
		return fmt.Errorf("failed to write to temp file: %w", err)
	}

	err = c.storeNarWithCDC(ctx, tempPath, &narURL, nil)
	if err != nil {
		if errors.Is(err, storage.ErrAlreadyExists) {
			zerolog.Ctx(ctx).Debug().Msg("nar already exists in chunk storage, skipping")
		} else {
			return err
		}
	}

	if err := c.checkAndFixNarInfosForNar(context.WithoutCancel(ctx), narURL); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("failed to fix narinfos after PutNar")
	}

	return nil
}

// DeleteNar deletes the nar from the store.
func (c *Cache) DeleteNar(ctx context.Context, narURL nar.URL) error {
	ctx, span := tracer.Start(
		ctx,
		"cache.DeleteNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
		),
	)
	defer span.End()

	return c.withReadLock(ctx, "DeleteNar", narJobKey(narURL.Hash), func() error {
		ctx = narURL.
			NewLogger(*zerolog.Ctx(ctx)).
			WithContext(ctx)

		zerolog.Ctx(ctx).Debug().Msg("deleting nar from store")

		if err := c.narStore.DeleteNar(ctx, narURL); err != nil {
			return err
		}

		zerolog.Ctx(ctx).Debug().Msg("nar deleted from store")

		return nil
	})
}

// createTempNarFile creates a temporary file for storing the NAR during download.
// It sets up cleanup to remove the file once all readers are done.
func (c *Cache) createTempNarFile(ctx context.Context, narURL *nar.URL, ds *downloadState) (*os.File, error) {
	f, err := c.createTempFile(ctx, narURL.Hash, narURL.Compression)
	if err != nil {
		return nil, err
	}

	ds.assetPath = f.Name()

	return f, nil
}

func (c *Cache) createTempFile(ctx context.Context, hash string, compression nar.CompressionType) (*os.File, error) {
	pattern := filepath.Base(hash) + "-*.nar"
	if cext := compression.String(); cext != "" {
		pattern += "." + cext
	}

	f, err := os.CreateTemp(c.tempDir, pattern)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error creating the nar file in the temporary directory")

		return nil, err
	}

	return f, nil
}

// streamResponseToFile streams the HTTP response body to a file in chunks,
// updating download state and broadcasting progress to waiting clients.
func (c *Cache) streamResponseToFile(ctx context.Context, resp *http.Response, f *os.File, ds *downloadState) error {
	return c.streamReaderToFile(ctx, resp.Body, f, ds)
}

// streamReaderToFile streams a reader to a file in chunks,
// updating download state and broadcasting progress to waiting clients.
func (c *Cache) streamReaderToFile(ctx context.Context, r io.Reader, f *os.File, ds *downloadState) error {
	buf := make([]byte, 32*1024)

	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := buf[:n]

			// Write the chunk read to the file
			written, writeErr := f.Write(chunk)
			if writeErr != nil {
				zerolog.Ctx(ctx).
					Error().
					Err(writeErr).
					Msg("error storing the chunk in the temporary file")

				return writeErr
			}

			// Update the state and signal waiting clients
			ds.mu.Lock()
			ds.bytesWritten += int64(written)
			ds.mu.Unlock()
			ds.cond.Broadcast()
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			return err
		}
	}

	// Writing to the temporary file is now done, final notification to watchers
	ds.mu.Lock()
	ds.finalSize = ds.bytesWritten
	ds.mu.Unlock()
	ds.cond.Broadcast()

	return nil
}

// storeNarFromTempFile reopens the temporary file and stores it in the NAR store.
// Used for non-CDC paths and CDC lazy-chunking (where the nar_file record is written
// directly without chunking and a background job chunks it later).
func (c *Cache) storeNarFromTempFile(ctx context.Context, tempPath string, narURL *nar.URL) error {
	// Get file size before opening for use in PutNar
	fi, err := os.Stat(tempPath)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error stating the nar temp file")

		return err
	}

	fileSize := fi.Size()

	f, err := os.Open(tempPath)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error opening the nar from the temporary file")

		return err
	}

	defer f.Close()

	var reader io.Reader = f

	// For Compression:none NARs the temp file holds raw bytes (the upstream package
	// transparently decompresses any content-encoding). We re-compress them as zstd
	// before storing so all "uncompressed" NARs are uniformly stored as .nar.zst.
	// Other compression types (zstd, xz, etc.) are stored as-is under their original
	// extension.
	storeURL := *narURL

	var putSize int64

	if narURL.Compression == nar.CompressionTypeNone {
		zerolog.Ctx(ctx).Debug().Msg("re-compressing uncompressed NAR as zstd before storing")

		// When re-compressing, we don't know the final compressed size,
		// so pass -1 to trigger multipart upload path in S3 storage.
		putSize = -1

		pr, pw := io.Pipe()

		analytics.SafeGo(ctx, func() {
			zw := zstd.NewPooledWriter(pw)

			_, copyErr := io.Copy(zw, f)

			closeErr := zw.Close()

			if copyErr != nil {
				pw.CloseWithError(copyErr)
			} else {
				pw.CloseWithError(closeErr)
			}
		})

		reader = pr
		storeURL.Compression = nar.CompressionTypeZstd
	} else {
		// For pre-compressed NARs, we know the file size
		putSize = fileSize
	}

	written, err := c.narStore.PutNar(ctx, storeURL, reader, putSize)
	if err != nil {
		if errors.Is(err, storage.ErrAlreadyExists) {
			// The NAR was already in storage — another request beat us to it, or the
			// process previously crashed after writing to storage but before the DB
			// record was committed. In both cases we still need to ensure the DB record
			// exists, so fetch the file size and fall through to ensureNarFileRecord.
			zerolog.Ctx(ctx).Debug().Msg("nar already exists in storage, getting size to ensure db record")

			var getErr error

			var r io.ReadCloser

			written, r, getErr = c.narStore.GetNar(ctx, storeURL)
			if getErr != nil {
				return fmt.Errorf("nar exists in storage but failed to get its metadata: %w", getErr)
			}

			r.Close()
		} else {
			zerolog.Ctx(ctx).
				Error().
				Err(err).
				Msg("error storing the nar in the store")

			return err
		}
	}

	zerolog.Ctx(ctx).Debug().Int64("written", written).Msg("nar stored successfully")

	// Ensure we have a NarFile record for it, and that it reflects the truth.
	if err = c.ensureNarFileRecord(ctx, *narURL, written, "storeNarFromTempFile.ensureNarFile"); err != nil {
		zerolog.Ctx(ctx).Error().Err(err).Msg("failed to ensure nar file record in storeNarFromTempFile")

		return err
	}

	return nil
}

// storeNarWithCDC stores the NAR from a temporary file using CDC.
// For CDC mode, NARs are always stored as raw uncompressed chunks.
// If the input file is compressed, it will be decompressed before chunking.
func (c *Cache) storeNarWithCDC(ctx context.Context, tempPath string, narURL *nar.URL, onNarFileReady func()) error {
	ctx, span := tracer.Start(
		ctx,
		"cache.storeNarWithCDC",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
			attribute.String("temp_path", tempPath),
		),
	)
	defer span.End()

	f, err := os.Open(tempPath)
	if err != nil {
		return fmt.Errorf("error opening temp file: %w", err)
	}
	defer f.Close()

	// Pass fileSize=0: the compressed file size on disk does not equal the uncompressed
	// NarSize, so we skip the size validation (which requires the narinfo's NarSize).
	// The NarSize is only known at download time (pullNarIntoStore), not here.
	return c.storeNarWithCDCFromReader(ctx, f, 0, narURL, onNarFileReady)
}

// maybeDecompressReader wraps r in a decompression reader if compression is not none.
// Returns the (possibly decompressed) reader, a cleanup function, and an error.
// If decompression setup fails, it logs a warning and returns the original reader so
// chunking can proceed with raw data (e.g., when stored metadata mismatches actual compression).
func maybeDecompressReader(
	ctx context.Context, r io.Reader, compression nar.CompressionType,
) (io.Reader, func(), error) {
	if compression == nar.CompressionTypeNone {
		return r, func() {}, nil
	}

	decompressed, err := nar.DecompressReader(ctx, r, compression)
	if err != nil {
		// If decompression setup fails, log a warning and proceed with raw data.
		// This can happen when stored metadata doesn't match the actual data compression.
		zerolog.Ctx(ctx).Warn().
			Err(err).
			Str("compression_type", compression.String()).
			Msg("failed to create decompression reader for CDC, proceeding with raw data")

		// Seek back to the beginning if the reader supports seeking,
		// since the failed reader creation may have consumed bytes.
		if seeker, ok := r.(io.Seeker); ok {
			if _, seekErr := seeker.Seek(0, io.SeekStart); seekErr != nil {
				return nil, func() {}, fmt.Errorf("error seeking reader after failed decompression: %w", seekErr)
			}
		}

		return r, func() {}, nil
	}

	return decompressed, func() { decompressed.Close() }, nil
}

// storeNarWithCDCFromReader is the core CDC chunking implementation. It accepts an
// io.Reader and explicit fileSize instead of opening a temp file, enabling concurrent
// use with fileAvailableReader (where bytes arrive progressively during decompression).
// narURL must already have Compression set to the compression of the data in r
// (typically CompressionTypeNone when called from the concurrent decompress+chunk path).
func (c *Cache) storeNarWithCDCFromReader(
	ctx context.Context,
	r io.Reader,
	fileSize uint64,
	narURL *nar.URL,
	onNarFileReady func(),
) error {
	ctx, span := tracer.Start(
		ctx,
		"cache.storeNarWithCDCFromReader",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
		),
	)
	defer span.End()

	zerolog.Ctx(ctx).Debug().
		Str("nar_url", narURL.String()).
		Str("original_compression", narURL.Compression.String()).
		Uint64("file_size", fileSize).
		Msg("storeNarWithCDCFromReader: starting")

	// For CDC, always store raw uncompressed data in chunks.
	// Save original compression before normalizing narURL.
	originalCompression := narURL.Compression
	narURL.Compression = nar.CompressionTypeNone

	// 1. Create or get NarFile record
	zerolog.Ctx(ctx).Debug().
		Str("nar_url", narURL.String()).
		Str("compression", narURL.Compression.String()).
		Uint64("file_size", fileSize).
		Msg("storeNarWithCDCFromReader: calling findOrCreateNarFileForCDC")

	narFileID, staleLockChunks, err := c.findOrCreateNarFileForCDC(ctx, narURL, fileSize)
	if err != nil {
		if errors.Is(err, storage.ErrAlreadyExists) {
			// The nar_file record already exists (NAR is fully chunked). Signal the caller
			// that the record is ready so the HTTP response can be completed without waiting.
			if onNarFileReady != nil {
				onNarFileReady()
			}

			zerolog.Ctx(ctx).Debug().Msg("nar already exists in chunk storage, skipping")

			return nil
		}

		return err
	}

	// Ensure chunking_started_at is cleared if we return an error before completion.
	// UpdateNarFileTotalChunks (on success) also clears it.
	var success bool

	defer func() {
		if !success {
			if _, err := c.dbClient.Ent().NarFile.UpdateOneID(int(narFileID)).
				ClearChunkingStartedAt().
				SetUpdatedAt(time.Now()).
				Save(context.WithoutCancel(ctx)); err != nil {
				zerolog.Ctx(context.WithoutCancel(ctx)).
					Warn().
					Err(err).
					Int64("nar_file_id", narFileID).
					Msg("failed to clear chunking_started_at after chunking failure")
			}
		}
	}()

	// Signal the caller that the nar_file DB record has been created. This allows the
	// HTTP response to complete (by unblocking the streaming goroutine's ds.stored wait)
	// without waiting for the full CDC chunking process to finish.
	if onNarFileReady != nil {
		onNarFileReady()
	}

	// 2. Start chunking
	cdcEnabled, chunkStore, cdcChunker := c.getCDCInfo()
	if !cdcEnabled || chunkStore == nil || cdcChunker == nil {
		return ErrCDCDisabled
	}

	// If a stale lock was detected and cleaned up, immediately remove the orphaned chunk
	// files and DB records. This avoids waiting for the next RunLRU GC cycle to reclaim space.
	if len(staleLockChunks) > 0 {
		c.cleanupStaleLockChunks(ctx, chunkStore, staleLockChunks)
	}

	// If the NAR is compressed, decompress it before chunking.
	// For CDC, we want to chunk the raw uncompressed data, not the compressed bytes.
	reader, decompCleanup, err := maybeDecompressReader(ctx, r, originalCompression)
	if err != nil {
		return err
	}

	defer decompCleanup()

	chunksChan, errChan := cdcChunker.Chunk(ctx, reader)

	var (
		totalSize  int64
		chunkCount int64
	)

	var batch []*chunker.Chunk

	flushTimer := time.NewTimer(cdcFirstBatchDelay)
	defer flushTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errChan:
			if err != nil {
				return fmt.Errorf("chunking error: %w", err)
			}
		case <-flushTimer.C:
			// Timer fired — flush if we have accumulated chunks
			if len(batch) > 0 {
				if err := c.recordChunkBatch(ctx, narFileID, chunkCount, batch); err != nil {
					return err
				}

				chunkCount += int64(len(batch))
				batch = batch[:0]
			}

			flushTimer.Reset(cdcSubsequentBatchDelay)
		case chunkMetadata, ok := <-chunksChan:
			if !ok { //nolint:nestif // TODO: Improve this later.
				// Process remaining batch
				if err := c.recordChunkBatch(ctx, narFileID, chunkCount, batch); err != nil {
					return err
				}

				chunkCount += int64(len(batch))

				// Validate that the total bytes consumed by the chunker match the
				// narinfo's declared NarSize. A clean io.EOF from a decompressor
				// can mask an upstream truncation (e.g. HTTP/2 GOAWAY), so we
				// enforce the invariant here before committing the result.
				if fileSize > 0 && uint64(totalSize) != fileSize { //nolint:gosec // G115: totalSize is non-negative
					zerolog.Ctx(ctx).Error().
						Str("nar_url", narURL.String()).
						Uint64("expected_bytes", fileSize).
						Int64("actual_bytes", totalSize).
						Msg("storeNarWithCDCFromReader: CDC chunking truncated, aborting commit")

					return fmt.Errorf(
						"CDC chunking truncated: expected %d uncompressed bytes, got %d: %w",
						fileSize, totalSize, io.ErrUnexpectedEOF,
					)
				}

				// All chunks processed - mark as complete and update file_size
				// to the actual uncompressed size (may differ from original compressed file size).
				err := c.withEntTransaction(ctx, "storeNarWithCDC.MarkComplete", func(tx *ent.Tx) error {
					//nolint:gosec // G115: nar_file IDs are non-negative
					_, err := tx.NarFile.UpdateOneID(int(narFileID)).
						SetTotalChunks(chunkCount).
						//nolint:gosec // G115: totalSize is non-negative
						SetFileSize(uint64(totalSize)).
						SetUpdatedAt(time.Now()).
						ClearChunkingStartedAt().
						Save(ctx)

					return err
				})
				if err != nil {
					return fmt.Errorf("error marking chunking complete: %w", err)
				}

				success = true

				// If compression was normalized (e.g., xz → none), atomically clean up the old
				// NarFile record and re-link narinfos to the new one in a single transaction.
				//
				// ATOMICITY REQUIREMENT: DeleteNarFileByHash CASCADE-deletes narinfo_nar_files.
				// relinkNarInfosToNarFile must run in the same transaction so that narinfos are
				// never left without a nar_file link if the process is killed between the two ops.
				//
				// When lazy chunking is enabled, skip the deletion of the old nar_file record
				// to allow for delayed cleanup. The background cleanup job will handle deletion
				// after the configured delay.
				if originalCompression != nar.CompressionTypeNone && !c.GetCDCLazyChunkingEnabled() {
					oldNarURL := nar.URL{
						Hash:        narURL.Hash,
						Compression: originalCompression,
						Query:       narURL.Query,
					}

					if err := c.withEntTransaction(ctx, "storeNarWithCDC.RelinkAndCleanup", func(tx *ent.Tx) error {
						if _, err := tx.NarFile.Delete().
							Where(
								entnarfile.HashEQ(narURL.Hash),
								entnarfile.CompressionEQ(originalCompression.String()),
								entnarfile.QueryEQ(narURL.Query.Encode()),
							).
							Exec(ctx); err != nil {
							return fmt.Errorf("failed to delete old nar_file record: %w", err)
						}

						return c.relinkNarInfosToNarFileWithEntTx(ctx, tx, oldNarURL, narFileID)
					}); err != nil {
						zerolog.Ctx(ctx).Warn().
							Err(err).
							Int64("nar_file_id", narFileID).
							Msg("failed to atomically clean up old NarFile and re-link narinfos after CDC normalization")
					}
				}

				return nil
			}

			// Store in chunkStore if new.
			//
			// NOTE (known limitation): The physical chunk file is written here before
			// recordChunkBatch writes the DB record. If the process crashes between these
			// two operations, the chunk file will be an unreferenced orphan on disk with no
			// corresponding DB record. The GC (RunLRU/GetOrphanedChunks) cannot find it
			// because it operates on DB records, not the filesystem. However, if the same NAR
			// is re-requested, the stale chunking_started_at lock will trigger cleanup and
			// a fresh chunking attempt that reuses existing chunk files via PutChunk.
			// For truly abandoned NARs (never re-requested after a crash), the orphaned
			// chunk files will persist until a filesystem-level cleanup is performed.
			_, compressedSize, err := chunkStore.PutChunk(ctx, chunkMetadata.Hash, chunkMetadata.Data)
			if err != nil {
				chunkMetadata.Free()

				return fmt.Errorf("error storing chunk: %w", err)
			}

			chunkMetadata.Free()
			//nolint:gosec // G115: Chunk size is small enough to fit in uint32
			chunkMetadata.CompressedSize = uint32(compressedSize)

			totalSize += int64(chunkMetadata.Size)

			batch = append(batch, chunkMetadata)

			// Flush if safety cap (cdcMaxBatchSize) reached
			if len(batch) >= cdcMaxBatchSize {
				if err := c.recordChunkBatch(ctx, narFileID, chunkCount, batch); err != nil {
					return err
				}

				chunkCount += int64(len(batch))
				batch = batch[:0]
				// Reset timer to subsequent delay after manual flush
				flushTimer.Reset(cdcSubsequentBatchDelay)
			}
		}
	}
}

// cdcChunkingLockTTL is the maximum time a chunking operation is allowed to hold
// the implicit lock (chunking_started_at IS NOT NULL, total_chunks = 0) before
// it is considered stale and a new attempt may clean up the partial state and restart.
const cdcChunkingLockTTL = time.Hour

// findOrCreateNarFileForCDC creates or retrieves a nar_file record for CDC chunking.
// It returns the narFileID, a list of chunk records that were removed from the junction
// table during stale lock cleanup (may be orphaned and need immediate cleanup), and an error.
// Callers should pass the returned staleLockChunks to cleanupStaleLockChunks after this
// function returns, so orphaned chunk files are reclaimed without waiting for RunLRU.
func (c *Cache) findOrCreateNarFileForCDC(
	ctx context.Context,
	narURL *nar.URL,
	fileSize uint64,
) (narFileID int64, staleLockChunks []*ent.Chunk, err error) {
	zerolog.Ctx(ctx).Debug().
		Str("hash", narURL.Hash).
		Str("compression", narURL.Compression.String()).
		Uint64("file_size", fileSize).
		Msg("findOrCreateNarFileForCDC: starting transaction")

	err = c.withEntTransaction(ctx, "storeNarWithCDC.CreateNarFile", func(tx *ent.Tx) error {
		// UPSERT on (hash, compression, query). Ent's OnConflict
		// returns the row id, but not the existing-row fields we need
		// for the state machine below — do a follow-up Get to retrieve
		// the current TotalChunks / ChunkingStartedAt / FileSize.
		nrID, err := tx.NarFile.Create().
			SetHash(narURL.Hash).
			SetCompression(narURL.Compression.String()).
			SetQuery(narURL.Query.Encode()).
			SetFileSize(fileSize).
			OnConflictColumns(
				entnarfile.FieldHash,
				entnarfile.FieldCompression,
				entnarfile.FieldQuery,
			).
			Update(func(u *ent.NarFileUpsert) {
				u.SetUpdatedAt(time.Now())
			}).
			ID(ctx)
		if err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Str("hash", narURL.Hash).
				Str("compression", narURL.Compression.String()).
				Msg("findOrCreateNarFileForCDC: CreateNarFile failed")

			return err
		}

		nr, err := tx.NarFile.Get(ctx, nrID)
		if err != nil {
			return fmt.Errorf("failed to fetch upserted nar_file %d: %w", nrID, err)
		}

		zerolog.Ctx(ctx).Debug().
			Int("nar_file_id", nr.ID).
			Str("hash", narURL.Hash).
			Str("compression", narURL.Compression.String()).
			Msg("findOrCreateNarFileForCDC: CreateNarFile succeeded")

		// If the record existed but had a different size, update it to reflect the truth.
		// However, in CDC mode, once chunked, FileSize holds the uncompressed size.
		// We should only update it if it's not yet fully chunked.
		if nr.FileSize != fileSize && nr.TotalChunks == 0 {
			if _, err := tx.NarFile.UpdateOneID(nr.ID).
				SetFileSize(fileSize).
				SetUpdatedAt(time.Now()).
				Save(ctx); err != nil {
				return err
			}
		}

		narFileID = int64(nr.ID)

		// Determine whether chunking has already been completed or is in progress.
		//
		//   total_chunks > 0                          → fully chunked, skip
		//   total_chunks = 0, chunking_started_at set → in progress or interrupted
		//     └─ lock still fresh (< TTL)             → another goroutine is working, skip
		//     └─ lock is stale   (≥ TTL)              → previous attempt crashed; clean up and restart
		//   total_chunks = 0, chunking_started_at nil → not yet started, proceed
		if nr.TotalChunks > 0 {
			return storage.ErrAlreadyExists
		}

		if nr.ChunkingStartedAt != nil {
			age := time.Since(*nr.ChunkingStartedAt)
			if age < cdcChunkingLockTTL {
				// Another in-progress attempt is still within the TTL — skip.
				return storage.ErrAlreadyExists
			}

			// Lock is stale: a previous attempt was interrupted mid-chunking.
			// Collect the partial chunk records so we can clean them up after the
			// transaction commits (chunk files live outside the DB transaction).
			partialChunks, err := tx.Chunk.Query().
				Where(entchunk.HasNarFileLinksWith(entnarfilechunk.NarFileID(nr.ID))).
				All(ctx)
			if err != nil {
				return fmt.Errorf("failed to get chunks for stale nar_file %d: %w", narFileID, err)
			}

			staleLockChunks = partialChunks

			zerolog.Ctx(ctx).Warn().
				Dur("age", age).
				Int64("narFileID", narFileID).
				Int("stale_chunk_count", len(staleLockChunks)).
				Msg("stale CDC chunking lock detected; cleaning up partial chunks and restarting")

			if _, err := tx.NarFileChunk.Delete().
				Where(entnarfilechunk.NarFileID(nr.ID)).
				Exec(ctx); err != nil {
				return fmt.Errorf("failed to delete partial chunks for nar_file %d: %w", narFileID, err)
			}
		}

		// Mark this nar_file as having chunking in progress.
		now := time.Now()
		if _, err := tx.NarFile.UpdateOneID(nr.ID).
			SetChunkingStartedAt(now).
			SetUpdatedAt(now).
			Save(ctx); err != nil {
			return fmt.Errorf("failed to set chunking_started_at for nar_file %d: %w", narFileID, err)
		}

		return nil
	})
	if err != nil {
		return 0, nil, err
	}

	return narFileID, staleLockChunks, nil
}

// cleanupStaleLockChunks immediately removes chunk files and database records that became
// orphaned as a result of a stale CDC chunking lock cleanup. This is an optimization over
// waiting for the regular GC cycle (RunLRU) to reclaim the space.
//
// A chunk is only deleted if it is truly orphaned (no remaining nar_file_chunks references),
// to avoid accidentally removing chunks that are shared with other completed NAR files.
func (c *Cache) cleanupStaleLockChunks(ctx context.Context, cs chunk.Store, staleLockChunks []*ent.Chunk) {
	if len(staleLockChunks) == 0 {
		return
	}

	log := zerolog.Ctx(ctx).With().
		Int("stale_chunk_count", len(staleLockChunks)).
		Logger()

	// Build a map from chunk ID → hash for O(1) lookup. Now that
	// GetOrphanedChunks is on Ent (§11.5), the chunk PK type is
	// plain int on both sides — no conversion needed.
	staleByID := make(map[int]string, len(staleLockChunks))

	ids := make([]int, 0, len(staleLockChunks))
	for _, ch := range staleLockChunks {
		staleByID[ch.ID] = ch.Hash
		ids = append(ids, ch.ID)
	}

	// Restrict the orphan-check query to the candidate IDs we already
	// know about, so we don't pull (and filter in memory) every orphan
	// in the database. Run this outside the previous transaction so we
	// see the committed deletion. Ent equivalent of the legacy LEFT
	// JOIN nar_file_chunks WHERE chunk_id IS NULL: chunks that have no
	// NarFileLinks edge.
	//
	// IDIn is batched (cdcCleanupHashBatchSize) to stay below driver
	// parameter limits: a very large NAR file split into small CDC
	// chunks can produce many thousands of partial-chunk IDs after a
	// stale-lock cleanup, which would otherwise exceed SQLite's ≤ 999
	// placeholder ceiling on older builds.
	var orphanedChunks []*ent.Chunk

	for start := 0; start < len(ids); start += cdcCleanupHashBatchSize {
		end := start + cdcCleanupHashBatchSize
		if end > len(ids) {
			end = len(ids)
		}

		batch, err := c.dbClient.Ent().Chunk.Query().
			Where(
				entchunk.IDIn(ids[start:end]...),
				entchunk.Not(entchunk.HasNarFileLinks()),
			).
			All(ctx)
		if err != nil {
			log.Warn().Err(err).Msg("failed to get orphaned chunks during stale lock cleanup; will rely on GC")

			return
		}

		orphanedChunks = append(orphanedChunks, batch...)
	}

	for _, oc := range orphanedChunks {
		hash := staleByID[oc.ID]

		chunkLog := log.With().Str("chunk_hash", hash).Int("chunk_id", oc.ID).Logger()
		chunkLog.Debug().Msg("immediately cleaning up chunk from stale CDC lock")

		// Delete the DB record first; if this fails, leave the physical file for GC.
		if _, err := c.dbClient.Ent().Chunk.Delete().
			Where(entchunk.IDEQ(oc.ID)).
			Exec(ctx); err != nil {
			chunkLog.Warn().Err(err).Msg("failed to delete orphaned chunk record during stale lock cleanup")

			continue
		}

		// Delete the physical chunk file.
		if err := cs.DeleteChunk(ctx, hash); err != nil && !errors.Is(err, chunk.ErrNotFound) {
			chunkLog.Warn().Err(err).Msg("failed to delete orphaned chunk file during stale lock cleanup")
		}
	}
}

// relinkNarInfosToNarFileWithEntTx is the Ent counterpart to the legacy
// LinkNarInfosByURLToNarFile bulk INSERT ... SELECT. Called after CDC
// migration to repair narinfo_nar_files entries that were
// CASCADE-deleted when the old nar_file record was removed.
//
// Ent's edge model doesn't express INSERT ... SELECT directly so we
// do it in two steps inside the caller's transaction:
//  1. Query all narinfo IDs whose URL matches the old narURL.
//  2. CreateBulk narinfo_nar_files rows for each, with
//     OnConflictColumns(...).Ignore() reproducing the legacy
//     ON CONFLICT (narinfo_id, nar_file_id) DO NOTHING semantics.
func (c *Cache) relinkNarInfosToNarFileWithEntTx(
	ctx context.Context,
	tx *ent.Tx,
	narURL nar.URL,
	narFileID int64,
) error {
	narInfoIDs, err := tx.NarInfo.Query().
		Where(entnarinfo.URL(narURL.String())).
		IDs(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch narinfos by URL %q: %w", narURL.String(), err)
	}

	if len(narInfoIDs) == 0 {
		return nil
	}

	// Chunk the CreateBulk to stay below driver placeholder limits.
	// Each row sends 2 parameters (narinfo_id, nar_file_id); 500 rows
	// The upsert binds 2 values per row (narinfoID + narFileID), so
	// 499 rows = 998 placeholders — safely under older SQLite's hard
	// limit of 999. Closures with thousands of narinfos sharing a
	// NAR URL (large multi-output derivations, deep dependency trees)
	// are uncommon but real; without chunking the bulk insert would
	// silently fail on older SQLite.
	const relinkBatchSize = 499

	for start := 0; start < len(narInfoIDs); start += relinkBatchSize {
		end := start + relinkBatchSize
		if end > len(narInfoIDs) {
			end = len(narInfoIDs)
		}

		batch := narInfoIDs[start:end]
		bulk := make([]*ent.NarInfoNarFileCreate, len(batch))

		for i, niID := range batch {
			bulk[i] = tx.NarInfoNarFile.Create().SetNarinfoID(niID).SetNarFileID(int(narFileID))
		}

		if err := tx.NarInfoNarFile.CreateBulk(bulk...).
			OnConflictColumns(entnarinfonarfile.FieldNarinfoID, entnarinfonarfile.FieldNarFileID).
			Ignore().
			Exec(ctx); err != nil {
			return fmt.Errorf("failed to link narinfos batch by URL %q to nar_file %d: %w",
				narURL.String(), narFileID, err)
		}
	}

	return nil
}

func (c *Cache) recordChunkBatch(ctx context.Context, narFileID int64, startIndex int64, batch []*chunker.Chunk) error {
	if len(batch) == 0 {
		return nil
	}

	return c.withEntTransaction(ctx, "recordChunkBatch", func(tx *ent.Tx) error {
		// Collect unique hashes from this batch.
		uniqueHashes := make([]string, 0, len(batch))

		seenInBatch := make(map[string]struct{}, len(batch))
		for _, cm := range batch {
			if _, ok := seenInBatch[cm.Hash]; !ok {
				uniqueHashes = append(uniqueHashes, cm.Hash)
				seenInBatch[cm.Hash] = struct{}{}
			}
		}

		// Bulk-fetch all chunks that already exist in one SELECT. This
		// avoids generating new auto-increment PKs (and hitting the
		// sequence-desync bug) for chunks whose hash is already in the
		// table.
		existing, err := chunksByHashes(ctx, tx.Chunk, uniqueHashes)
		if err != nil {
			return fmt.Errorf("error fetching existing chunks: %w", err)
		}

		idByHash := make(map[string]int, len(uniqueHashes))
		for _, ch := range existing {
			idByHash[ch.Hash] = ch.ID
		}

		// Build CREATE calls only for hashes genuinely absent from the DB.
		var creates []*ent.ChunkCreate

		newHashSet := make(map[string]struct{})

		for _, cm := range batch {
			if _, exists := idByHash[cm.Hash]; exists {
				continue
			}

			if _, queued := newHashSet[cm.Hash]; queued {
				continue // duplicate within batch — only INSERT once
			}

			newHashSet[cm.Hash] = struct{}{}
			creates = append(creates, tx.Chunk.Create().
				SetHash(cm.Hash).
				SetSize(cm.Size).
				SetCompressedSize(cm.CompressedSize))
		}

		if len(creates) > 0 {
			// Bulk-insert new chunks. ON CONFLICT (hash) DO NOTHING handles
			// the narrow race where another goroutine inserted the same hash
			// between our SELECT and this INSERT.
			if err := tx.Chunk.CreateBulk(creates...).
				OnConflictColumns(entchunk.FieldHash).
				Ignore().
				Exec(ctx); err != nil {
				return fmt.Errorf("error creating new chunk records: %w", err)
			}

			// Re-fetch newly inserted (or race-won-by-another) chunks to
			// populate idByHash with their PKs.
			newHashes := make([]string, 0, len(newHashSet))
			for h := range newHashSet {
				newHashes = append(newHashes, h)
			}

			freshChunks, err := chunksByHashes(ctx, tx.Chunk, newHashes)
			if err != nil {
				return fmt.Errorf("error fetching new chunk IDs: %w", err)
			}

			if len(freshChunks) != len(newHashes) {
				return fmt.Errorf("error fetching new chunk IDs: expected %d got %d: %w",
					len(newHashes), len(freshChunks), errChunkIDFetchMismatch)
			}

			for _, ch := range freshChunks {
				idByHash[ch.Hash] = ch.ID
			}
		}

		// Link every batch entry to the NAR file in bulk; ON CONFLICT
		// (nar_file_id, chunk_index) DO NOTHING is idempotent on retry.
		bulk := make([]*ent.NarFileChunkCreate, len(batch))
		for i, cm := range batch {
			bulk[i] = tx.NarFileChunk.Create().
				SetNarFileID(int(narFileID)).
				SetChunkID(idByHash[cm.Hash]).
				SetChunkIndex(int(startIndex) + i)
		}

		if err := tx.NarFileChunk.CreateBulk(bulk...).
			OnConflictColumns(entnarfilechunk.FieldNarFileID, entnarfilechunk.FieldChunkIndex).
			Ignore().
			Exec(ctx); err != nil {
			return fmt.Errorf("error linking chunks in bulk: %w", err)
		}

		return nil
	})
}

func (c *Cache) pullNarIntoStore(
	ctx context.Context,
	narURL *nar.URL,
	preferredUpstreamURL *nar.URL,
	uc *upstream.Cache,
	ds *downloadState,
	narInfo *narinfo.NarInfo, // Added
) {
	// Track download completion for cleanup synchronization
	ds.cleanupWg.Add(1)
	defer ds.cleanupWg.Done()

	// keepJobAlive prevents the deferred cleanup below from removing the job from
	// upstreamJobs and closing ds.done immediately. For CDC, we keep the job alive
	// so concurrent GetNar calls can find the ds and stream from the temp file while
	// CDC chunking is in progress. The CDC goroutine is responsible for cleanup.
	keepJobAlive := false

	defer func() {
		if keepJobAlive {
			// CDC goroutine will handle job removal and ds.done closing.
			ds.startOnce.Do(func() { close(ds.start) })
			ds.cond.Broadcast()

			return
		}

		// Clean up local job tracking
		c.upstreamJobsMu.Lock()
		delete(c.upstreamJobs, narJobKey(narURL.Hash))
		c.upstreamJobsMu.Unlock()

		ds.startOnce.Do(func() { close(ds.start) })

		// Inform watchers that we are fully done and the asset is now in the store.
		ds.doneOnce.Do(func() { close(ds.done) })

		// Final broadcast
		ds.cond.Broadcast()
	}()

	// Store upstream hostname for metrics (early in function)
	if uc != nil {
		ds.setUpstreamHostname(uc.GetHostname())
	}

	ctx, span := tracer.Start(
		ctx,
		"cache.pullNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
		),
	)
	defer span.End()

	now := time.Now()

	// Use preferredUpstreamURL for the actual HTTP download if provided.
	// This allows CDC first pulls to download the original compressed (e.g. xz) NAR
	// from upstream instead of the uncompressed version, reducing TTFB significantly.
	// narURL continues to be used for job key, CDC storage, hasAsset, and serving.
	downloadURL := narURL
	if preferredUpstreamURL != nil {
		downloadURL = preferredUpstreamURL
	}

	zerolog.Ctx(ctx).
		Info().
		Msg("downloading the nar from upstream")

	resp, err := c.getNarFromUpstream(ctx, downloadURL, uc)
	if err != nil {
		if !errors.Is(err, storage.ErrNotFound) {
			zerolog.Ctx(ctx).
				Error().
				Err(err).
				Msg("error getting the nar from upstream caches")
		} else {
			zerolog.Ctx(ctx).
				Debug().
				Err(err).
				Msg("error getting the nar from upstream caches")
		}

		ds.setError(err)

		return
	}

	// bodyOwned is set to true when a background goroutine takes ownership of
	// resp.Body (CDC path). In that case the goroutine is responsible for
	// draining and closing the body; the defer below must not touch it.
	bodyOwned := false

	defer func() {
		if bodyOwned {
			return
		}

		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)

		resp.Body.Close()
	}()

	// Cleanup goroutine: wait for download and all readers to finish, then remove
	// temp files.
	c.backgroundWG.Add(1)
	analytics.SafeGo(ctx, func() {
		defer c.backgroundWG.Done()

		ds.cleanupWg.Wait() // Wait for download to complete

		// Wait for the background CDC goroutine before marking closed. This allows
		// concurrent GetNar calls to join via ds.wg while chunking is in progress.
		// cdcWg is zero for non-CDC downloads, so Wait() returns immediately.
		ds.cdcWg.Wait()

		// Mark as closed to prevent new readers from adding to WaitGroup
		ds.mu.Lock()
		ds.closed = true
		ds.mu.Unlock()

		ds.wg.Wait() // Then wait for all readers to finish

		if ds.assetPath != "" {
			os.Remove(ds.assetPath)
		}

		if ds.compressedAssetPath != "" {
			os.Remove(ds.compressedAssetPath)
		}
	})

	// CDC path for compressed NARs: decompress the HTTP response body into a
	// temp file, then feed the temp file to the CDC chunker concurrently.
	// Concurrent GetNar clients read from the temp file via fileAvailableReader
	// without waiting for CDC chunking to complete, so they get bytes as fast as
	// the download progresses rather than waiting for chunking to finish.
	//
	// Conditions: CDC enabled, NAR is compressed (plain NARs need no decompression
	// so the simpler temp-file path below handles them), narInfo present and non-empty.
	//
	// Lazy Chunking: If lazy chunking is enabled, store the compressed NAR directly
	// without chunking, then trigger background migration later for faster TTFB.
	cdcEnabled := c.isCDCEnabled()
	compressedNar := downloadURL.Compression != nar.CompressionTypeNone
	hasNarInfo := narInfo != nil && narInfo.NarSize != 0
	lazyChunkingDisabled := !c.GetCDCLazyChunkingEnabled()
	//nolint:nestif // CDC download pipeline requires multiple sequential error checks
	if cdcEnabled && compressedNar && hasNarInfo && lazyChunkingDisabled {
		// narURLForCDC uses CompressionTypeNone because the temp file holds raw
		// uncompressed bytes (the decompressor runs in the download goroutine).
		narURLForCDC := *narURL
		narURLForCDC.Compression = nar.CompressionTypeNone

		// Create temp file for decompressed bytes. Concurrent GetNar clients read
		// from this file immediately without waiting for CDC chunking to complete.
		f, err := c.createTempNarFile(ctx, &narURLForCDC, ds)
		if err != nil {
			ds.setError(err)

			return
		}

		defer f.Close()

		ds.tempFileCompression = nar.CompressionTypeNone

		// Signal concurrent clients that the temp file path is ready.
		// ds.tempFileCompression must be set before closing ds.start.
		ds.startOnce.Do(func() { close(ds.start) })

		// Keep the job in upstreamJobs so concurrent GetNar calls on this server
		// find the ds and stream from the temp file while CDC chunking is in progress.
		// The CDC goroutine removes the job and closes ds.done when complete.
		keepJobAlive = true

		ds.cdcWg.Add(1)
		ds.wg.Add(1)

		c.backgroundWG.Add(1)

		analytics.SafeGo(ctx, func() {
			defer c.backgroundWG.Done()

			// LIFO: wg.Done 1st, cdcWg.Done 2nd, cleanup func 3rd.
			defer func() {
				c.upstreamJobsMu.Lock()
				delete(c.upstreamJobs, narJobKey(narURL.Hash))
				c.upstreamJobsMu.Unlock()

				ds.doneOnce.Do(func() { close(ds.done) })
				ds.cond.Broadcast()
			}()
			defer ds.cdcWg.Done()
			defer ds.wg.Done()

			fForCDC, openErr := os.Open(ds.assetPath)
			if openErr != nil {
				ds.setError(openErr)

				return
			}

			defer fForCDC.Close()

			// fileAvailableReader blocks until bytes are available and returns
			// EOF when ds.finalSize is set (download goroutine finished writing).
			cdcReader := &fileAvailableReader{f: fForCDC, ds: ds, ctx: ctx}

			// onNarFileReady fires right after the nar_file DB record is created
			// (before chunking starts). Concurrent GetNar clients waiting for
			// ds.stored can then confirm the NAR is recorded and return.
			onNarFileReady := func() {
				ds.storedOnce.Do(func() { close(ds.stored) })
			}

			cdcErr := c.storeNarWithCDCFromReader(ctx, cdcReader, narInfo.NarSize, &narURLForCDC, onNarFileReady)
			if cdcErr != nil {
				zerolog.Ctx(ctx).
					Error().
					Err(cdcErr).
					Str("nar_url", narURLForCDC.String()).
					Msg("CDC chunking failed in background after pullNarIntoStore")
				ds.setError(cdcErr)

				return
			}

			if err := c.checkAndFixNarInfosForNar(context.WithoutCancel(ctx), narURLForCDC); err != nil {
				zerolog.Ctx(ctx).
					Warn().
					Err(err).
					Msg("failed to fix narinfo file size after pullNarIntoStore (CDC)")
			}
		})

		// Decompress HTTP response and write to the temp file.
		// Updates ds.bytesWritten and ds.finalSize so concurrent GetNar clients
		// and the CDC goroutine can read progressively via fileAvailableReader.
		decompReader, err := nar.DecompressReader(ctx, resp.Body, downloadURL.Compression)
		if err != nil {
			ds.setError(err)

			return
		}

		defer decompReader.Close()

		if err := c.streamReaderToFile(ctx, decompReader, f, ds); err != nil {
			ds.setError(err)

			return
		}

		zerolog.Ctx(ctx).
			Info().
			Dur("elapsed", time.Since(now)).
			Msg("download of nar complete (CDC chunking in background)")

		return
	}

	// Simple (non-pipe) path: download to a temp file, then store.
	f, err := c.createTempNarFile(ctx, narURL, ds)
	if err != nil {
		ds.setError(err)

		return
	}

	defer f.Close()

	// Record the actual compression type of the bytes written to the temp file
	ds.tempFileCompression = downloadURL.Compression

	// Signal that readers can now start reading from f
	ds.startOnce.Do(func() { close(ds.start) })

	if err := c.streamResponseToFile(ctx, resp, f, ds); err != nil {
		ds.setError(err)

		return
	}

	// CDC eager mode: after download is complete, run CDC chunking asynchronously so
	// the HTTP response can complete immediately while chunking continues in the background.
	// This mirrors the CDC compressed path above and avoids blocking the client.
	if cdcEnabled && lazyChunkingDisabled {
		keepJobAlive = true

		ds.cdcWg.Add(1)
		ds.wg.Add(1)

		c.backgroundWG.Add(1)

		analytics.SafeGo(ctx, func() {
			defer c.backgroundWG.Done()

			defer func() {
				c.upstreamJobsMu.Lock()
				delete(c.upstreamJobs, narJobKey(narURL.Hash))
				c.upstreamJobsMu.Unlock()

				ds.doneOnce.Do(func() { close(ds.done) })
				ds.cond.Broadcast()
			}()
			defer ds.cdcWg.Done()
			defer ds.wg.Done()

			onNarFileReady := func() {
				ds.storedOnce.Do(func() { close(ds.stored) })
			}

			cdcErr := c.storeNarWithCDC(context.WithoutCancel(ctx), ds.assetPath, narURL, onNarFileReady)
			if cdcErr != nil {
				zerolog.Ctx(ctx).
					Error().
					Err(cdcErr).
					Str("nar_url", narURL.String()).
					Msg("CDC chunking failed in background after pullNarIntoStore (simple path)")
				ds.setError(cdcErr)

				return
			}

			if err := c.checkAndFixNarInfosForNar(context.WithoutCancel(ctx), *narURL); err != nil {
				zerolog.Ctx(ctx).
					Warn().
					Err(err).
					Msg("failed to fix narinfo file size after pullNarIntoStore (CDC simple path)")
			}
		})

		zerolog.Ctx(ctx).
			Info().
			Dur("elapsed", time.Since(now)).
			Msg("download of nar complete (CDC chunking in background)")

		return
	}

	if err = c.storeNarFromTempFile(ctx, ds.assetPath, narURL); err != nil {
		ds.setError(err)

		return
	}

	// Trigger background migration for lazy chunking
	// This applies when CDC is enabled with lazy chunking, where the nar was stored
	// without chunking (total_chunks=0) and needs to be chunked in the background.
	if c.isCDCEnabled() && c.GetCDCLazyChunkingEnabled() {
		c.maybeBackgroundMigrateNarToChunks(context.WithoutCancel(ctx), *narURL)
	}

	// Signal that the asset is now in final storage and the distributed lock can be released
	ds.storedOnce.Do(func() { close(ds.stored) })

	if err := c.checkAndFixNarInfosForNar(context.WithoutCancel(ctx), *narURL); err != nil {
		zerolog.Ctx(ctx).
			Warn().
			Err(err).
			Msg("failed to fix narinfo file size after pullNarIntoStore")
	}

	zerolog.Ctx(ctx).
		Info().
		Dur("elapsed", time.Since(now)).
		Msg("download of nar complete")
}

// serveNarFromStorageViaPipe wraps storage reading with a pipe pattern to decouple
// it from the HTTP request context. This prevents partial transfers when the request
// context is cancelled (e.g., timeout, client disconnect) while data is still being
// read from storage (especially S3).
func (c *Cache) serveNarFromStorageViaPipe(
	ctx context.Context,
	narURL *nar.URL,
	hasInStore bool,
) (int64, io.ReadCloser, error) {
	ctx, span := tracer.Start(
		ctx,
		"cache.serveNarFromStorageViaPipe",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
			attribute.Bool("has_in_store", hasInStore),
		),
	)
	defer span.End()

	// Get reader from storage using the request context
	// This allows proper cancellation propagation to prevent goroutine leaks
	var (
		storageSize   int64
		storageReader io.ReadCloser
		err           error
	)

	// For CDC with lazy chunking: check if nar is chunked before deciding serving path.
	// - If hasInStore and not chunked (total_chunks=0): serve raw from store (lazy)
	// - If hasInStore and chunked (total_chunks>0): serve from chunks (optimized)
	// - If not in store: serve from chunks (standard CDC path)
	serveFromChunks := !hasInStore
	if hasInStore && c.isCDCEnabled() {
		// Check if the nar is chunked by looking at the nar_file record
		nr, nrErr := c.getNarFileFromDB(ctx, c.dbClient.Ent().NarFile, *narURL)
		// Only upgrade to chunks when the request is for the uncompressed NAR.
		// Chunks are always stored uncompressed; serving them for a compressed
		// request (e.g. .nar.xz) would send raw bytes the client cannot decode.
		if nrErr == nil && nr.TotalChunks > 0 && narURL.Compression == nar.CompressionTypeNone {
			// Nar is chunked, serve from chunks for better performance
			serveFromChunks = true
		}
		// If nrErr (not found), total_chunks=0, or request wants compressed data,
		// serve from store (raw or legacy).
	}

	// Chunks are always uncompressed. If the client requested a compressed NAR
	// (e.g. .nar.xz) but the whole-file is no longer in storage (only chunks
	// remain), we cannot reconstruct the compressed stream. Return not-found so
	// the client falls back to an upstream cache that still has the original
	// compressed file. This prevents "input compression not recognized" errors
	// caused by serving raw chunk bytes to a client expecting xz data.
	if serveFromChunks && narURL.Compression != nar.CompressionTypeNone {
		return 0, nil, fmt.Errorf("NAR %s is only available as chunks, cannot serve as %s: %w",
			narURL.Hash, narURL.Compression, storage.ErrNotFound)
	}

	if serveFromChunks {
		storageSize, storageReader, err = c.getNarFromChunks(ctx, narURL)
	} else {
		storageSize, storageReader, err = c.getNarFromStore(ctx, narURL)
	}

	if err != nil {
		return 0, nil, err
	}

	// Create pipe to decouple storage reading from HTTP request lifecycle
	pipeReader, pipeWriter := io.Pipe()

	// Launch background goroutine to copy from storage reader to pipe
	analytics.SafeGo(ctx, func() {
		defer pipeWriter.Close()
		defer storageReader.Close()

		// Copy from storage to pipe
		_, copyErr := io.Copy(pipeWriter, storageReader)
		if copyErr != nil {
			// Check if this is a benign "pipe closed" error (client disconnected early)
			// This commonly happens when the client receives all data and closes the connection
			// before the background goroutine finishes writing.
			if errors.Is(copyErr, io.ErrClosedPipe) || strings.Contains(copyErr.Error(), "closed pipe") {
				zerolog.Ctx(ctx).
					Debug().
					Err(copyErr).
					Str("nar_url", narURL.String()).
					Int64("storage_size", storageSize).
					Msg("pipe closed during NAR copy (client likely disconnected)")
			} else {
				zerolog.Ctx(ctx).
					Error().
					Err(copyErr).
					Str("nar_url", narURL.String()).
					Int64("storage_size", storageSize).
					Msg("error copying NAR from storage to pipe")
			}

			pipeWriter.CloseWithError(copyErr)
		}
	})

	// Return pipe reader (safe from request context cancellation) with known size
	return storageSize, pipeReader, nil
}

func (c *Cache) getNarFromStore(
	ctx context.Context,
	narURL *nar.URL,
) (int64, io.ReadCloser, error) {
	ctx, span := tracer.Start(
		ctx,
		"cache.getNarFromStore",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
		),
	)
	defer span.End()

	// For Compression:none NARs, the physical file is stored as .nar.zst.
	// Try reading from .nar.zst and decompress transparently.
	storeURL := *narURL

	var decompress bool

	if narURL.Compression == nar.CompressionTypeNone {
		zstdURL := *narURL
		zstdURL.Compression = nar.CompressionTypeZstd

		if c.narStore.HasNar(ctx, zstdURL) {
			storeURL = zstdURL
			decompress = true
		}
	}

	size, r, err := c.narStore.GetNar(ctx, storeURL)
	if err != nil {
		return 0, nil, fmt.Errorf("error fetching the nar from the store: %w", err)
	}

	// storedFileSize is the on-disk size of the stored file (the compressed size
	// for Compression:none NARs stored as .nar.zst). Captured before size is set
	// to -1 below so we can use it when healing a missing DB record.
	storedFileSize := size

	if decompress && !narURL.TransparentZstd {
		decompressed, decompErr := nar.DecompressReader(ctx, r, nar.CompressionTypeZstd)
		if decompErr != nil {
			_ = r.Close()

			return 0, nil, fmt.Errorf("error decompressing nar from store: %w", decompErr)
		}

		r = decompressed
		size = -1 // decompressed size is unknown
	} else if !decompress {
		// File is stored as plain uncompressed .nar (not .nar.zst); we cannot
		// serve a zstd stream even if the caller asked for one.
		narURL.TransparentZstd = false
	}

	var needsDBRecord bool

	err = c.withEntTransaction(ctx, "getNarFromStore", func(tx *ent.Tx) error {
		nr, err := c.getNarFileFromDB(ctx, tx.NarFile, *narURL)
		if err != nil {
			if database.IsNotFoundError(err) {
				// NAR is in storage but has no DB record — this is an orphan left by a
				// crash between narStore.PutNar and ensureNarFileRecord. Schedule healing.
				needsDBRecord = true

				return nil
			}

			return fmt.Errorf("error fetching the nar record: %w", err)
		}

		// Update narURL.Compression to match the record found in DB.
		// For CDC mode, if we requested xz but found a none record (common), we must
		// return none so the caller knows they're receiving uncompressed bytes.
		narURL.Compression = nar.CompressionType(nr.Compression)

		if nr.LastAccessedAt == nil || time.Since(*nr.LastAccessedAt) > c.recordAgeIgnoreTouch {
			if _, err := tx.NarFile.Update().
				Where(
					entnarfile.HashEQ(narURL.Hash),
					entnarfile.CompressionEQ(narURL.Compression.String()),
					entnarfile.QueryEQ(narURL.Query.Encode()),
				).
				SetLastAccessedAt(time.Now()).
				SetUpdatedAt(time.Now()).
				Save(ctx); err != nil {
				return fmt.Errorf("error touching the nar record: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		r.Close()

		return 0, nil, err
	}

	// Heal the orphan: create the missing DB record so LRU tracking works.
	if needsDBRecord {
		if healErr := c.ensureNarFileRecord(ctx, *narURL, storedFileSize, "getNarFromStore.healOrphan"); healErr != nil {
			zerolog.Ctx(ctx).Warn().Err(healErr).
				Str("nar_url", narURL.String()).
				Msg("failed to create missing DB record for orphan NAR in getNarFromStore")
		}
	}

	return size, r, nil
}

func (c *Cache) getNarFromUpstream(
	ctx context.Context,
	narURL *nar.URL,
	uc *upstream.Cache,
) (*http.Response, error) {
	ctx, span := tracer.Start(
		ctx,
		"cache.getNarFromUpstream",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
		),
	)
	defer span.End()

	// Track fetch start time
	startTime := time.Now()

	defer func() {
		duration := time.Since(startTime).Seconds()
		upstreamNarFetchDuration.Record(ctx, duration)
	}()

	ctx = narURL.
		NewLogger(*zerolog.Ctx(ctx)).
		WithContext(ctx)

	var ucs []*upstream.Cache
	if uc != nil {
		ucs = []*upstream.Cache{uc}
	} else {
		ucs = c.getHealthyUpstreams()
	}

	uc, err := c.selectNarUpstream(ctx, narURL, ucs)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error selecting an upstream for the nar")

		return nil, err
	}

	if uc == nil {
		return nil, storage.ErrNotFound
	}

	resp, err := uc.GetNar(ctx, *narURL)
	if err != nil {
		if !errors.Is(err, upstream.ErrNotFound) {
			level := errorLogLevelForContextErrors(err)
			zerolog.Ctx(ctx).
				WithLevel(level).
				Err(err).
				Str("hostname", uc.GetHostname()).
				Msg("error fetching the nar from upstream")
		}

		return nil, err
	}

	return resp, nil
}

func (c *Cache) deleteNarFromStore(ctx context.Context, narURL *nar.URL) error {
	// create a new context not associated with any request because we don't want
	// downstream HTTP request to cancel this.
	ctx = zerolog.Ctx(ctx).WithContext(context.Background())

	if !c.HasNarInStore(ctx, *narURL) {
		return storage.ErrNotFound
	}

	if _, err := c.dbClient.Ent().NarFile.Delete().
		Where(
			entnarfile.HashEQ(narURL.Hash),
			entnarfile.CompressionEQ(narURL.Compression.String()),
			entnarfile.QueryEQ(narURL.Query.Encode()),
		).
		Exec(ctx); err != nil {
		return fmt.Errorf("error deleting narinfo from the database: %w", err)
	}

	return c.narStore.DeleteNar(ctx, *narURL)
}

// GetNarInfo returns the narInfo given a hash from the store. If the narInfo
// is not found in the store, it's pulled from an upstream, stored in the
// stored and finally returned.
func (c *Cache) GetNarInfo(ctx context.Context, hash string) (*narinfo.NarInfo, error) {
	ctx, span := tracer.Start(
		ctx,
		"cache.GetNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	var metricAttrs []attribute.KeyValue

	defer func() {
		narInfoServedCount.Add(ctx, 1, metric.WithAttributes(metricAttrs...))
	}()

	var (
		narInfo *narinfo.NarInfo
		err     error
	)

	ctx = zerolog.Ctx(ctx).
		With().
		Str("narinfo_hash", hash).
		Logger().
		WithContext(ctx)

	narInfo, err = c.getNarInfoFromDatabase(ctx, hash)
	if err == nil {
		metricAttrs = append(
			metricAttrs,
			attribute.String("result", "hit"),
			attribute.String("status", "success"),
			attribute.String("source", "database"),
		)

		if narURL, err := nar.ParseURL(narInfo.URL); err == nil {
			// Only trigger CDC migration for NARs whose URL has non-none
			// compression: these are whole-file NARs that haven't been migrated
			// to chunks yet. NARs already stored as CDC chunks have
			// Compression:none in their URL, so we skip the migration check to
			// avoid a spurious distributed-lock + DB-query round-trip on every
			// cache hit. Note: Compression:none NARs stored as .nar.zst (without
			// CDC) will still be migrated when they are served via GetNar.
			if narURL.Compression != nar.CompressionTypeNone {
				c.maybeBackgroundMigrateNarToChunks(ctx, narURL)
				// If the NAR is already in CDC chunks, normalize in-memory before
				// returning (see maybeCDCNormalizeNarInfoURL for rationale).
				c.maybeCDCNormalizeNarInfoURL(ctx, narURL, narInfo)
			}

			zerolog.Ctx(ctx).
				Debug().
				Str("narinfo", narInfo.String()).
				Msg("fetched this narinfo from the database")
		}

		metricAttrs = append(metricAttrs, attribute.String("status", "success"))

		return narInfo, nil
	}

	if !errors.Is(err, storage.ErrNotFound) && !errors.Is(err, errNarInfoPurged) {
		level := errorLogLevelForContextErrors(err)

		zerolog.Ctx(ctx).
			WithLevel(level).
			Err(err).
			Msg("error fetching the narinfo from the database")

		return nil, fmt.Errorf("error fetching narinfo from database: %w", err)
	}

	if c.narInfoStore.HasNarInfo(ctx, hash) {
		metricAttrs = append(
			metricAttrs,
			attribute.String("result", "hit"),
			attribute.String("status", "success"),
			attribute.String("source", "storage"),
		)

		narInfo, err = c.getNarInfoFromStore(ctx, hash)
		if err == nil {
			zerolog.Ctx(ctx).
				Debug().
				Str("narinfo", narInfo.String()).
				Msg("fetched this narinfo from the store")

			metricAttrs = append(metricAttrs, attribute.String("status", "success"))

			return narInfo, nil
		}

		// If narinfo was purged, continue to fetch from upstream
		if !errors.Is(err, errNarInfoPurged) {
			if retryErr := c.handleStorageFetchError(ctx, hash, err, &narInfo, &metricAttrs); retryErr != nil {
				return nil, retryErr
			}

			metricAttrs = append(metricAttrs, attribute.String("status", "success"))

			return narInfo, nil
		}
	}

	metricAttrs = append(metricAttrs, attribute.String("result", "miss"))

	// If the artifact is not in the DB or Store, check if we are in "Upload Only" mode.
	// If so, we return ErrNotFound immediately to let the client know we don't have it locally,
	// triggering the PUT (push) operation.
	if IsUploadOnly(ctx) {
		return nil, storage.ErrNotFound
	}

	ds := c.prePullNarInfo(ctx, hash)

	zerolog.Ctx(ctx).
		Debug().
		Msg("pulling nar in a go-routing and will wait for it")

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-ds.done:
	}

	err = ds.getError()
	if err != nil {
		metricAttrs = append(metricAttrs, attribute.String("status", "error"))

		// Add upstream hostname to metrics even on error
		if upstreamHostname := ds.getUpstreamHostname(); upstreamHostname != "" {
			metricAttrs = append(metricAttrs,
				attribute.String("upstream_hostname", upstreamHostname))
		}

		return nil, err
	}

	// Add upstream hostname to metrics on success
	if upstreamHostname := ds.getUpstreamHostname(); upstreamHostname != "" {
		metricAttrs = append(metricAttrs,
			attribute.String("upstream_hostname", upstreamHostname))
	}

	// After pulling from upstream, get the narinfo from the database (where it's now stored)
	narInfo, err = c.getNarInfoFromDatabase(ctx, hash)
	if err != nil {
		level := errorLogLevelForContextErrors(err)

		zerolog.Ctx(ctx).
			WithLevel(level).
			Err(err).
			Msg("failed to fetch this narinfo from the database")

		metricAttrs = append(metricAttrs, attribute.String("status", "error"))

		return nil, err
	}

	if zerolog.Ctx(ctx).GetLevel() <= zerolog.DebugLevel {
		zerolog.Ctx(ctx).
			Debug().
			Str("narinfo", narInfo.String()).
			Msg("fetched narinfo from database after upstream pull")
	}

	metricAttrs = append(metricAttrs, attribute.String("status", "success"))

	return narInfo, nil
}

func (c *Cache) pullNarInfo(
	ctx context.Context,
	hash string,
	ds *downloadState,
) {
	done := func() {
		// Clean up local job tracking
		c.upstreamJobsMu.Lock()
		delete(c.upstreamJobs, narInfoJobKey(hash))
		c.upstreamJobsMu.Unlock()

		// Ensure ds.start is closed to unblock waiters
		ds.startOnce.Do(func() { close(ds.start) })

		ds.doneOnce.Do(func() { close(ds.done) })
	}

	defer done()

	ctx, span := tracer.Start(
		ctx,
		"cache.pullNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	now := time.Now()

	// Wait for any active migration or PutNarInfo for this hash.
	// Since PutNarInfo takes a Write Lock on c.cacheLocker, and MigrateNarInfo
	// takes a Lock on c.downloadLocker, we need to wait on both.
	if err := c.downloadLocker.Lock(ctx, migrationLockKey(hash), c.downloadLockTTL); err != nil {
		ds.setError(fmt.Errorf("failed to acquire migration lock: %w", err))

		return
	}

	if err := c.downloadLocker.Unlock(context.WithoutCancel(ctx), migrationLockKey(hash)); err != nil {
		zerolog.Ctx(ctx).
			Warn().
			Err(err).
			Str("key", migrationLockKey(hash)).
			Msg("Failed to unlock migration lock after waiting")
	}

	if err := c.withReadLock(ctx, "pullNarInfo-wait-put", narInfoLockKey(hash), func() error { return nil }); err != nil {
		ds.setError(fmt.Errorf("failed to acquire put lock: %w", err))

		return
	}

	// Check if the record is now in the database after waiting for locks.
	if _, err := c.getNarInfoFromDatabase(ctx, hash); err == nil {
		ds.startOnce.Do(func() { close(ds.start) })
		ds.doneOnce.Do(func() { close(ds.done) })

		return
	}

	uc, narInfo, err := c.getNarInfoFromUpstream(ctx, hash)
	if err != nil {
		if !errors.Is(err, storage.ErrNotFound) {
			zerolog.Ctx(ctx).
				Error().
				Err(err).
				Msg("error getting the narInfo from upstream caches")
		} else {
			zerolog.Ctx(ctx).
				Debug().
				Err(err).
				Msg("error getting the narInfo from upstream caches")
		}

		ds.setError(err)

		return
	}

	// Store upstream hostname for metrics
	if uc != nil {
		ds.setUpstreamHostname(uc.GetHostname())
	}

	narURL, err := nar.ParseURL(narInfo.URL)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Str("nar_url", narInfo.URL).
			Msg("error parsing the nar URL")

		ds.setError(err)

		return
	}

	// Signal that we've successfully fetched the narinfo (no streaming for narinfo)
	ds.startOnce.Do(func() { close(ds.start) })

	ctx = zerolog.Ctx(ctx).
		With().
		Str("nar_url", narInfo.URL).
		Logger().
		WithContext(ctx)

		// Fire and forget: fetch the NAR in the background.
		// The upstream package now handles transparent zstd encoding/decoding, so
		// we always get raw bytes regardless of upstream support. FileSize = NarSize
		// for Compression:none upstreams, so no synchronous wait is needed.
	detachedCtx := context.WithoutCancel(ctx)

	// create a copy of narURL to avoid a race condition when
	// narURL is modified within a background goroutine.
	narURLForBG := narURL

	c.prePullNar(ctx, detachedCtx, &narURLForBG, nil, uc, narInfo)

	// For CDC mode, NARs are stored as raw uncompressed chunks.
	// For Compression:none upstreams, NARs are stored as zstd files and served
	// as Compression:none with transparent HTTP encoding.
	// Normalize narInfo to reflect this regardless of upstream compression.
	// Note: we must NOT modify narURL here since prePullNar may still be using
	// the pointer in a background goroutine. Instead, build the normalized URL string directly.
	// Skip normalization when lazy chunking is enabled - preserve original compression
	// until the NAR is actually chunked.
	if (c.isCDCEnabled() && !c.GetCDCLazyChunkingEnabled()) || narInfo.Compression == nar.CompressionTypeNone.String() {
		normalizedURL := nar.URL{Hash: narURL.Hash, Compression: nar.CompressionTypeNone, Query: narURL.Query}
		narInfo.Compression = nar.CompressionTypeNone.String()
		narInfo.URL = normalizedURL.String() // → "nar/hash.nar"

		// Set the FileHash and FileSize to null, not required for compression=none.
		narInfo.FileHash = nil
		narInfo.FileSize = 0
	}

	if err := c.signNarInfo(ctx, hash, narInfo); err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error signing the narinfo")

		return
	}

	// Narinfos are now stored ONLY in the database, not in the storage backend.
	// The storage backend (S3/filesystem) is used only for NAR files.
	// Legacy narinfos in storage are handled by background migration during GetNarInfo.

	if err := c.storeInDatabase(ctx, hash, narInfo); err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error storing the narinfo in the database")

		ds.setError(err)

		return
	}

	// Signal that the asset is now in final storage and the distributed lock
	// can be released. MUST be after storeInDatabase succeeds: the coordinator
	// at coordinateDownload waits on ds.stored and then releases the download
	// lock; if we signaled before the DB write, another instance polling
	// hasAsset() (which checks the database) would acquire the lock, find the
	// DB empty, and start a redundant upstream download — surfacing as
	// TestGetNarInfoDistributedCoordination failures with two upstream calls
	// instead of one.
	ds.storedOnce.Do(func() { close(ds.stored) })

	zerolog.Ctx(ctx).
		Info().
		Dur("elapsed", time.Since(now)).
		Msg("download of narinfo complete")
}

// PutNarInfo records the narInfo (given as an io.Reader) into the store and signs it.
func (c *Cache) PutNarInfo(ctx context.Context, hash string, r io.ReadCloser) error {
	ctx, span := tracer.Start(
		ctx,
		"cache.PutNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	ctx = zerolog.Ctx(ctx).
		With().
		Str("narinfo_hash", hash).
		Logger().
		WithContext(ctx)

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, r)

		r.Close()
	}()

	// Use hash-specific lock to prevent concurrent writes of the same narinfo
	err := c.withWriteLock(ctx, "PutNarInfo", narInfoLockKey(hash), func() error {
		narInfo, err := narinfo.Parse(r)
		if err != nil {
			return fmt.Errorf("error parsing narinfo: %w", err)
		}

		// For CDC mode, normalize all NARs to Compression: none.
		// CDC chunks are stored uncompressed and re-compressed individually.
		// For Compression:none upstreams, NARs are stored as zstd and served
		// as Compression:none with transparent HTTP encoding.
		if c.isCDCEnabled() || narInfo.Compression == nar.CompressionTypeNone.String() {
			if narInfo.Compression != nar.CompressionTypeNone.String() && narInfo.Compression != "" {
				nu, parseErr := nar.ParseURL(narInfo.URL)
				if parseErr != nil {
					return fmt.Errorf("failed to parse narinfo URL %q for CDC normalization: %w", narInfo.URL, parseErr)
				}

				nu.Compression = nar.CompressionTypeNone
				narInfo.URL = nu.String()
				narInfo.Compression = nar.CompressionTypeNone.String()
			}

			// Set the FileHash and FileSize to null, not required for compression=none.
			narInfo.FileHash = nil
			narInfo.FileSize = 0
		}

		if err := c.signNarInfo(ctx, hash, narInfo); err != nil {
			return fmt.Errorf("error signing the narinfo: %w", err)
		}

		// Narinfos are now stored ONLY in the database, not in the storage backend.
		// The storage backend (S3/filesystem) is used only for NAR files.
		// Legacy narinfos in storage are handled by background migration during GetNarInfo.
		if err := c.storeInDatabase(ctx, hash, narInfo); err != nil {
			return fmt.Errorf("error storing in database: %w", err)
		}

		// Cleanup legacy narinfo from storage if it exists.
		// This handles the race condition where PutNarInfo finishes before a background
		// migration can trigger.
		if c.narInfoStore.HasNarInfo(ctx, hash) {
			if err := c.narInfoStore.DeleteNarInfo(ctx, hash); err != nil && !errors.Is(err, storage.ErrNotFound) {
				zerolog.Ctx(ctx).Warn().
					Err(err).
					Str("hash", hash).
					Msg("failed to delete legacy narinfo from storage after PutNarInfo")
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if err := c.CheckAndFixNarInfo(context.WithoutCancel(ctx), hash); err != nil {
		zerolog.Ctx(ctx).
			Warn().
			Err(err).
			Msg("failed to fix narinfo file size after PutNarInfo")
	}

	return nil
}

// DeleteNarInfo deletes the narInfo from the store.
func (c *Cache) DeleteNarInfo(ctx context.Context, hash string) error {
	ctx, span := tracer.Start(
		ctx,
		"cache.DeleteNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	ctx = zerolog.Ctx(ctx).
		With().
		Str("narinfo_hash", hash).
		Logger().
		WithContext(ctx)

	zerolog.Ctx(ctx).Debug().Msg("deleting narinfo from store")

	if err := c.deleteNarInfoFromStore(ctx, hash); err != nil {
		return err
	}

	zerolog.Ctx(ctx).Debug().Msg("narinfo deleted from store")

	return nil
}

func (c *Cache) prePullNarInfo(ctx context.Context, hash string) *downloadState {
	ctx, span := tracer.Start(
		ctx,
		"cache.prePullNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	return c.coordinateDownload(
		ctx,
		context.WithoutCancel(ctx),
		narInfoJobKey(hash),
		hash,
		true,
		func(ctx context.Context) bool {
			if _, err := c.getNarInfoFromDatabase(ctx, hash); err == nil {
				return true
			}

			return c.narInfoStore.HasNarInfo(ctx, hash)
		},
		func(ds *downloadState) {
			c.pullNarInfo(context.WithoutCancel(ctx), hash, ds)
		},
	)
}

func (c *Cache) prePullNar(
	coordCtx context.Context,
	ctx context.Context,
	narURL *nar.URL,
	preferredUpstreamURL *nar.URL,
	uc *upstream.Cache,
	narInfo *narinfo.NarInfo, // Added
) *downloadState {
	ctx, span := tracer.Start(
		ctx,
		"cache.prePullNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
		),
	)
	defer span.End()

	// coordCtx is the original context for coordination (not detached)
	// ctx is the detached context for the download itself
	// We need both: coordCtx to respond to caller cancellation, ctx for background download

	return c.coordinateDownload(
		coordCtx,
		ctx,
		narJobKey(narURL.Hash),
		narURL.Hash,
		false,
		func(ctx context.Context) bool {
			// A placeholder nar_file row (created by storeInDatabase before chunking
			// starts, or left by a failed download) must NOT count as an available
			// asset: otherwise coordinateDownload returns a completed state and skips
			// the re-download, and streamProgressiveChunks waits 30s for chunks that
			// never arrive, yielding a truncated NAR response. isServable is the single
			// predicate gating this; see its doc comment.
			servable, err := c.isServable(ctx, *narURL)
			if err != nil {
				zerolog.Ctx(ctx).Warn().Err(err).
					Msg("error checking servability for download coordination; treating as cache miss")

				return false
			}

			return servable
		},
		func(ds *downloadState) {
			c.pullNarIntoStore(ctx, narURL, preferredUpstreamURL, uc, ds, narInfo)
		},
	)
}

// getNarFileFromDB looks up a nar_file record by URL using the given
// Ent client. Accepts *ent.NarFileClient, which is the type both
// `c.dbClient.Ent().NarFile` and `tx.NarFile` resolve to — so the same
// helper handles non-tx and in-tx call sites. Tries the most likely
// compression first based on whether CDC is enabled:
//   - CDC enabled:  try "none" first (all CDC files use none), fall back to original
//   - CDC disabled: try original compression first, fall back to "none"
func (c *Cache) getNarFileFromDB(
	ctx context.Context,
	nfc *ent.NarFileClient,
	narURL nar.URL,
) (*ent.NarFile, error) {
	first, second := narURL.Compression, nar.CompressionTypeNone
	if c.isCDCEnabled() {
		first, second = nar.CompressionTypeNone, narURL.Compression
	}

	nr, err := nfc.Query().
		Where(
			entnarfile.HashEQ(narURL.Hash),
			entnarfile.CompressionEQ(first.String()),
			entnarfile.QueryEQ(narURL.Query.Encode()),
		).
		Only(ctx)
	if err == nil {
		return nr, nil
	}

	if database.IsNotFoundError(err) && first != second {
		return nfc.Query().
			Where(
				entnarfile.HashEQ(narURL.Hash),
				entnarfile.CompressionEQ(second.String()),
				entnarfile.QueryEQ(narURL.Query.Encode()),
			).
			Only(ctx)
	}

	return nil, err
}

// narFileServable reports whether an existing nar_file record represents servable
// backing data: it is fully chunked (TotalChunks > 0) or chunking is actively in
// progress within cdcChunkingLockTTL. A placeholder record (TotalChunks == 0 with a
// NULL or stale ChunkingStartedAt) is NOT servable.
func narFileServable(nr *ent.NarFile) bool {
	if nr.TotalChunks > 0 {
		return true
	}

	if nr.ChunkingStartedAt != nil {
		return time.Since(*nr.ChunkingStartedAt) < cdcChunkingLockTTL
	}

	return false
}

// isServable is the single source of truth for whether a NAR can be served right
// now: a whole-file exists in the store, OR (with CDC enabled) a nar_file record
// exists that is fully chunked or actively chunking within the lock TTL.
//
// The mere existence of a nar_file row NEVER makes a NAR servable: a backing-less
// placeholder row (created by storeInDatabase at narinfo-fetch time, or left behind
// by a failed download) is a cache miss that must trigger an upstream (re-)download,
// never a terminal 404. Every read-path servability decision routes through this
// helper so the placeholder regression (ncps #1255/#1263/#1279/#1290), which kept
// recurring via divergent ad-hoc checks, cannot return.
func (c *Cache) isServable(ctx context.Context, narURL nar.URL) (bool, error) {
	if c.HasNarInStore(ctx, narURL) {
		return true, nil
	}

	if !c.isCDCEnabled() {
		return false, nil
	}

	nr, err := c.getNarFileFromDB(ctx, c.dbClient.Ent().NarFile, narURL)
	if err != nil {
		if database.IsNotFoundError(err) {
			return false, nil
		}

		return false, fmt.Errorf("failed to look up nar_file record for servability: %w", err)
	}

	return narFileServable(nr), nil
}

// hasNarInStore checks if the NAR exists in the storage, handling the .nar.zst fallback for CompressionTypeNone.
func (c *Cache) HasNarInStore(ctx context.Context, narURL nar.URL) bool {
	present, _ := c.statNarInStore(ctx, narURL)

	return present
}

// statNarInStore reports whether the NAR is in storage, distinguishing a
// confirmed absence (false, nil) from an undeterminable result (false, err).
// Decision paths that must not treat an ambiguous storage error as "absent"
// (e.g. before purging a narinfo) MUST use this instead of HasNarInStore.
func (c *Cache) statNarInStore(ctx context.Context, narURL nar.URL) (bool, error) {
	// For Compression:none NARs, the physical file is stored as .nar.zst; check that first.
	if narURL.Compression == nar.CompressionTypeNone {
		zstdURL := narURL

		zstdURL.Compression = nar.CompressionTypeZstd

		present, err := c.narStore.StatNar(ctx, zstdURL)
		if err != nil {
			return false, err
		}

		if present {
			return true, nil
		}
	}

	return c.narStore.StatNar(ctx, narURL)
}

func (c *Cache) signNarInfo(ctx context.Context, hash string, narInfo *narinfo.NarInfo) error {
	if !c.shouldSignNarinfo {
		return nil
	}

	_, span := tracer.Start(
		ctx,
		"cache.signNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	var sigs []signature.Signature

	for _, sig := range narInfo.Signatures {
		if sig.Name != c.hostName {
			sigs = append(sigs, sig)
		}
	}

	sig, err := c.secretKey.Sign(nil, narInfo.Fingerprint())
	if err != nil {
		return fmt.Errorf("error signing the fingerprint: %w", err)
	}

	sigs = append(sigs, sig)

	narInfo.Signatures = sigs

	return nil
}

func (c *Cache) getNarInfoFromStore(ctx context.Context, hash string) (*narinfo.NarInfo, error) {
	ctx, span := tracer.Start(
		ctx,
		"cache.getNarInfoFromStore",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	ni, err := c.narInfoStore.GetNarInfo(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("error parsing the narinfo: %w", err)
	}

	narURL, err := nar.ParseURL(ni.URL)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Str("nar_url", ni.URL).
			Msg("error parsing the nar-url")

		// narinfo is invalid, remove it
		if err := c.purgeNarInfo(ctx, hash, &narURL); err != nil {
			zerolog.Ctx(ctx).
				Error().
				Err(err).
				Msg("error purging the narinfo")
		}

		return nil, errNarInfoPurged
	}

	ctx = narURL.
		NewLogger(*zerolog.Ctx(ctx)).
		WithContext(ctx)

	// Verify the NAR exists in storage. Use statNarInStore (which also handles the
	// .nar.zst fallback for Compression:none) so an ambiguous storage error — a
	// timed-out or stale stat on a network filesystem — is NOT mistaken for a
	// confirmed absence and does not drive a destructive purge.
	hasNarInStore, storeErr := c.statNarInStore(ctx, narURL)
	if storeErr != nil {
		zerolog.Ctx(ctx).
			Warn().
			Err(storeErr).
			Msg("ambiguous storage error checking nar presence, skipping purge")

		return ni, nil
	}

	if !hasNarInStore && !c.hasUpstreamJob(narURL.Hash) {
		zerolog.Ctx(ctx).
			Error().
			Msg("narinfo was found in the store but no nar was found, requesting a purge")

		if err := c.purgeNarInfo(ctx, hash, &narURL); err != nil {
			return nil, fmt.Errorf("error purging the narinfo: %w", err)
		}

		return nil, errNarInfoPurged
	}

	err = c.withEntTransaction(ctx, "getNarInfoFromStore", func(tx *ent.Tx) error {
		nir, err := narInfoByHash(ctx, tx.NarInfo, hash)
		if err != nil {
			if database.IsNotFoundError(err) {
				c.backgroundMigrateNarInfo(ctx, hash, ni)

				return nil
			}

			return fmt.Errorf("error fetching the narinfo record: %w", err)
		}

		// Migrate narinfos from storage to the database.
		if nir.URL == nil || *nir.URL == "" {
			c.backgroundMigrateNarInfo(ctx, hash, ni)
		}

		if c.isCDCEnabled() {
			c.BackgroundMigrateNarToChunks(ctx, narURL)
		}

		if nir.LastAccessedAt == nil || time.Since(*nir.LastAccessedAt) > c.recordAgeIgnoreTouch {
			if _, err := tx.NarInfo.Update().
				Where(entnarinfo.HashEQ(hash)).
				SetLastAccessedAt(time.Now()).
				Save(ctx); err != nil {
				return fmt.Errorf("error touching the narinfo record: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return ni, nil
}

// handleStorageFetchError handles errors from storage fetches and implements retry logic for
// race conditions with migration. Returns nil if retry succeeded, otherwise returns error.
//
// RACE CONDITION FIX: If storage read fails with NotFound, the file might have been deleted
// by a concurrent migration. Retry database lookup to see if migration completed successfully.
//
// Race scenario:
// 1. GetNarInfo checks database -> not found (NULL URL)
// 2. GetNarInfo checks HasNarInfo -> true (file exists)
// 3. Migration runs: writes to DB and deletes from storage
// 4. GetNarInfo tries to read storage -> NotFound (file deleted!)
// 5. Retry database -> SUCCESS (migration completed).
func (c *Cache) handleStorageFetchError(
	ctx context.Context,
	hash string,
	storageErr error,
	narInfo **narinfo.NarInfo,
	metricAttrs *[]attribute.KeyValue,
) error {
	// updateAttr is a small helper to reduce duplication in metric updates.
	updateAttr := func(key, value string) {
		for i, attr := range *metricAttrs {
			if attr.Key == attribute.Key(key) {
				(*metricAttrs)[i] = attribute.String(key, value)

				return
			}
		}
	}

	// Only retry on NotFound errors (file deleted by migration)
	//nolint:nestif // TODO: Remove this and fix it
	if errors.Is(storageErr, storage.ErrNotFound) {
		// Wait for any active migration or PutNarInfo for this hash.
		if err := c.downloadLocker.Lock(ctx, migrationLockKey(hash), c.downloadLockTTL); err != nil {
			return fmt.Errorf("failed to acquire migration lock after storage error: %w", err)
		}

		if err := c.downloadLocker.Unlock(context.WithoutCancel(ctx), migrationLockKey(hash)); err != nil {
			zerolog.Ctx(ctx).
				Warn().
				Err(err).
				Str("key", migrationLockKey(hash)).
				Msg("Failed to unlock migration lock after waiting in handleStorageFetchError")
		}

		if err := c.withReadLock(
			ctx,
			"handleStorageFetchError-wait-put",
			narInfoLockKey(hash),
			func() error {
				return nil
			},
		); err != nil {
			return fmt.Errorf("failed to acquire put lock after storage error: %w", err)
		}

		var dbErr error

		// Retry multiple times with a linear backoff. The file might have just been deleted,
		// and the migration might be in progress or just about to commit its transaction.
		const (
			migrationRetryAttempts = 10
			migrationRetryStep     = 10 * time.Millisecond
		)
		for i := range migrationRetryAttempts {
			*narInfo, dbErr = c.getNarInfoFromDatabase(ctx, hash)
			if dbErr == nil {
				// Migration succeeded while we were checking storage!
				// The source is now the database, not storage. We need to update the metric.
				updateAttr("source", "database")

				return nil // Signal success to caller
			}

			if !errors.Is(dbErr, storage.ErrNotFound) {
				// If it's a real database error, don't retry.
				break
			}

			// Wait a bit before retrying.
			delay := (time.Duration(i) + 1) * migrationRetryStep
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
				// continue
			}
		}

		// If the DB retry also fails with a non-NotFound error, it's a more serious issue.
		// We should wrap this error to provide more context for debugging.
		if dbErr != nil && !errors.Is(dbErr, storage.ErrNotFound) {
			storageErr = fmt.Errorf("%w (db retry failed: %v)", storageErr, dbErr)
		}
		// Fall through to return storage error if database also fails
	}

	// The fetch failed, so update the status metric from "success" to "error".
	updateAttr("status", "error")

	return fmt.Errorf("error fetching the narinfo from the store: %w", storageErr)
}

func (c *Cache) getNarInfoFromDatabase(ctx context.Context, hash string) (*narinfo.NarInfo, error) {
	ctx, span := tracer.Start(
		ctx,
		"cache.getNarInfoFromDatabase",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	var (
		ni     *narinfo.NarInfo
		narURL *nar.URL
	)

	err := c.withEntTransaction(ctx, "getNarInfoFromDatabase", func(tx *ent.Tx) error {
		var populateErr error

		ni, narURL, populateErr = c.populateNarInfoFromDatabase(ctx, tx, hash, true)

		return populateErr
	})
	if err != nil {
		return nil, err
	}

	// Verify Nar file exists in storage.
	// For Compression:none NARs, the physical file is stored as .nar.zst; check that first.
	hasNar := c.HasNarInStore(ctx, *narURL)

	if !hasNar {
		var err error

		hasNar, err = c.HasNarInChunks(ctx, *narURL)
		if err != nil {
			return nil, fmt.Errorf("failed to check if nar exists in chunks: %w", err)
		}
	}

	isBeingDownloadedLocally := c.hasUpstreamJob(narURL.Hash)
	isBeingDownloadedRemotely := false

	if !hasNar && !isBeingDownloadedLocally {
		isBeingDownloadedRemotely = c.isRemoteDownloadInProgress(ctx, narURL.Hash)
	}

	// Check if this narinfo should be purged
	if !hasNar && !isBeingDownloadedLocally && !isBeingDownloadedRemotely { //nolint:nestif // deferred
		// Double-check store presence to close the TOCTOU window: if the NAR download
		// completed (os.Rename) between the initial check and hasUpstreamJob
		// returning false (job removed after os.Rename), the NAR is already on disk.
		// Use statNarInStore (not HasNarInStore) so an ambiguous storage error — a
		// timed-out or stale stat on a network filesystem — is NOT mistaken for a
		// confirmed absence and does not drive a destructive purge.
		var storeErr error

		hasNar, storeErr = c.statNarInStore(ctx, *narURL)
		if storeErr != nil {
			zerolog.Ctx(ctx).
				Warn().
				Err(storeErr).
				Msg("ambiguous storage error checking nar presence, skipping purge")

			return ni, nil
		}

		if !hasNar {
			var recheckErr error

			hasNar, recheckErr = c.HasNarInChunks(ctx, *narURL)
			if recheckErr != nil {
				return nil, fmt.Errorf("failed to recheck if nar exists in chunks: %w", recheckErr)
			}
		}

		if hasNar {
			return ni, nil
		}

		// For CDC: if a nar_files record exists with total_chunks=0, CDC chunking is
		// in progress in a background goroutine (the job was removed from upstreamJobs
		// when pullNarIntoStore returned early, before chunking finished). Don't purge.
		if c.isCDCEnabled() {
			hasRecord, err := c.HasNarFileRecord(ctx, *narURL)
			if err != nil {
				// If we can't check the DB, it's safer to assume a download might be in progress.
				// Avoid purging and let the next request re-evaluate.
				zerolog.Ctx(ctx).
					Warn().
					Err(err).
					Msg("failed to check for in-progress CDC record, skipping purge")

				return ni, nil
			}

			if hasRecord {
				return ni, nil
			}
		}

		zerolog.Ctx(ctx).
			Error().
			Msg("narinfo was found in the database but no nar was found in storage, requesting a purge")

		if err := c.purgeNarInfo(ctx, hash, narURL); err != nil {
			return nil, fmt.Errorf("error purging the narinfo: %w", err)
		}

		return nil, errNarInfoPurged
	}

	return ni, nil
}

func (c *Cache) populateNarInfoFromDatabase(
	ctx context.Context,
	tx *ent.Tx,
	hash string,
	touch bool,
) (*narinfo.NarInfo, *nar.URL, error) {
	nir, err := tx.NarInfo.Query().
		Where(entnarinfo.HashEQ(hash)).
		WithReferences().
		WithSignatures().
		Only(ctx)
	if err != nil {
		if database.IsNotFoundError(err) {
			return nil, nil, storage.ErrNotFound
		}

		return nil, nil, fmt.Errorf("error fetching the narinfo record from database: %w", err)
	}

	// If URL is nil/empty, the record hasn't been migrated yet (an
	// older ncps version may have created it as a placeholder).
	if nir.URL == nil || *nir.URL == "" {
		return nil, nil, storage.ErrNotFound
	}

	ni := &narinfo.NarInfo{
		StorePath:   derefStringPtr(nir.StorePath),
		URL:         *nir.URL,
		Compression: derefStringPtr(nir.Compression),
		//nolint:gosec // G115: file_size is non-negative by narinfos_file_size_nonneg CHECK
		FileSize: uint64(derefInt64Ptr(nir.FileSize)),
		//nolint:gosec // G115: nar_size is non-negative by narinfos_nar_size_nonneg CHECK
		NarSize: uint64(derefInt64Ptr(nir.NarSize)),
		Deriver: derefStringPtr(nir.Deriver),
		System:  derefStringPtr(nir.System),
		CA:      derefStringPtr(nir.Ca),
	}

	if ni.FileHash, err = parseValidHashPtr(nir.FileHash, "file_hash"); err != nil {
		return nil, nil, err
	}

	if ni.NarHash, err = parseValidHashPtr(nir.NarHash, "nar_hash"); err != nil {
		return nil, nil, err
	}

	// References and signatures came back via the eager-load.
	for _, ref := range nir.Edges.References {
		ni.References = append(ni.References, ref.Reference)
	}

	for _, s := range nir.Edges.Signatures {
		sig, err := signature.ParseSignature(s.Signature)
		if err != nil {
			return nil, nil, fmt.Errorf("error parsing signature %q: %w", s.Signature, err)
		}

		ni.Signatures = append(ni.Signatures, sig)
	}

	// Parse narURL for subsequent HasNar check
	parsedURL, err := nar.ParseURL(ni.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("error parsing nar URL %q: %w", ni.URL, err)
	}

	// Touch the record if needed.
	if touch {
		if nir.LastAccessedAt == nil || time.Since(*nir.LastAccessedAt) > c.recordAgeIgnoreTouch {
			if _, err := tx.NarInfo.Update().
				Where(entnarinfo.HashEQ(hash)).
				SetLastAccessedAt(time.Now()).
				Save(ctx); err != nil {
				return nil, nil, fmt.Errorf("error touching the narinfo record: %w", err)
			}
		}
	}

	return ni, &parsedURL, nil
}

func (c *Cache) getNarInfoFromUpstream(
	ctx context.Context,
	hash string,
) (*upstream.Cache, *narinfo.NarInfo, error) {
	ctx, span := tracer.Start(
		ctx,
		"cache.getNarInfoFromUpstream",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	// Track fetch start time
	startTime := time.Now()

	defer func() {
		duration := time.Since(startTime).Seconds()
		upstreamNarInfoFetchDuration.Record(ctx, duration)
	}()

	uc, err := c.selectNarInfoUpstream(ctx, hash)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error selecting an upstream for the narinfo")

		return nil, nil, err
	}

	if uc == nil {
		return nil, nil, storage.ErrNotFound
	}

	narInfo, err := uc.GetNarInfo(ctx, hash)
	if err != nil {
		if !errors.Is(err, upstream.ErrNotFound) {
			level := errorLogLevelForContextErrors(err)

			zerolog.Ctx(ctx).
				WithLevel(level).
				Err(err).
				Str("hostname", uc.GetHostname()).
				Msg("error fetching the narInfo from upstream")
		}

		return nil, nil, storage.ErrNotFound
	}

	return uc, narInfo, nil
}

func (c *Cache) purgeNarInfo(
	ctx context.Context,
	hash string,
	narURL *nar.URL,
) error {
	ctx, span := tracer.Start(
		ctx,
		"cache.purgeNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
			attribute.String("narinfo_url", narURL.String()),
		),
	)
	defer span.End()

	err := c.withEntTransaction(ctx, "purgeNarInfo", func(tx *ent.Tx) error {
		if _, err := tx.NarInfo.Delete().
			Where(entnarinfo.HashEQ(hash)).
			Exec(ctx); err != nil {
			return fmt.Errorf("error deleting the narinfo record: %w", err)
		}

		if narURL.Hash != "" {
			if _, err := tx.NarFile.Delete().
				Where(
					entnarfile.HashEQ(narURL.Hash),
					entnarfile.CompressionEQ(narURL.Compression.String()),
					entnarfile.QueryEQ(narURL.Query.Encode()),
				).
				Exec(ctx); err != nil {
				return fmt.Errorf("error deleting the nar record: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Ignore ErrNotFound: purge is idempotent. A concurrent goroutine may
	// have already removed the file between the DB transaction above and
	// the store delete below (TOCTOU), which is fine — the goal is gone.
	if err := c.deleteNarInfoFromStore(ctx, hash); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("error removing narinfo from store: %w", err)
	}

	if narURL.Hash != "" {
		if err := c.deleteNarFromStore(ctx, narURL); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("error removing nar from store: %w", err)
		}
	}

	return nil
}

func (c *Cache) storeInDatabase(
	ctx context.Context,
	hash string,
	narInfo *narinfo.NarInfo,
) error {
	ctx, span := tracer.Start(
		ctx,
		"cache.storeInDatabase",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	zerolog.Ctx(ctx).
		Info().
		Msg("storing narinfo and nar_file record in the database")

	return c.withEntTransaction(ctx, "storeInDatabase", func(tx *ent.Tx) error {
		nir, err := upsertNarInfoFromParsed(ctx, tx, hash, narInfo)
		if err != nil {
			return err
		}

		if err := addNarInfoReferences(ctx, tx, nir.ID, narInfo.References); err != nil {
			return err
		}

		if err := addNarInfoSignatures(ctx, tx, nir.ID, narInfo.Signatures); err != nil {
			return err
		}

		narURL, err := nar.ParseURL(narInfo.URL)
		if err != nil {
			return fmt.Errorf("error parsing the nar URL: %w", err)
		}

		// Normalize the NAR URL to remove any narinfo hash prefix.
		// This ensures nar_files.hash matches what's actually stored in the storage layer.
		normalizedNarURL, err := narURL.Normalize()
		if err != nil {
			return fmt.Errorf("error normalizing the nar URL: %w", err)
		}

		narFileID, err := createOrUpdateNarFileEnt(ctx, tx, normalizedNarURL, narFileSize(narInfo))
		if err != nil {
			return err
		}

		// Link narinfo to nar_file. Ignore() compiles to ON CONFLICT
		// DO UPDATE SET column = column (a no-op update) which is the
		// effective equivalent of the legacy ON CONFLICT DO NOTHING but
		// always returns a row so RETURNING-style scans succeed on
		// Postgres. Postgres needs the conflict resolved at the SQL
		// layer or the surrounding transaction enters an aborted state.
		if err := tx.NarInfoNarFile.Create().
			SetNarinfoID(nir.ID).
			SetNarFileID(narFileID).
			OnConflictColumns(entnarinfonarfile.FieldNarinfoID, entnarinfonarfile.FieldNarFileID).
			Ignore().
			Exec(ctx); err != nil {
			return fmt.Errorf("error linking narinfo to nar_file: %w", err)
		}

		return nil
	})
}

// upsertNarInfoFromParsed mirrors the legacy CreateNarInfo's UPSERT
// semantics ("INSERT … ON CONFLICT (hash) DO UPDATE … WHERE url IS NULL"):
//
//   - hash absent  → insert a fresh narinfo with the supplied fields
//   - hash present with NULL/empty URL → update the stub with the supplied fields
//   - hash present with a non-empty URL → keep the existing row untouched
//
// Atomicity is provided by the surrounding *ent.Tx; the cache-level
// PutNarInfo write lock prevents concurrent same-hash writers from
// racing this read-then-write pattern.
func upsertNarInfoFromParsed(
	ctx context.Context,
	tx *ent.Tx,
	hash string,
	narInfo *narinfo.NarInfo,
) (*ent.NarInfo, error) {
	existing, err := narInfoByHash(ctx, tx.NarInfo, hash)

	switch {
	case database.IsNotFoundError(err):
		// Insert new, ignoring a concurrent insert racing between our
		// SELECT above and this INSERT. Using DO NOTHING instead of
		// DO UPDATE keeps the transaction alive on conflict — a
		// competing INSERT (DO UPDATE) would abort the PostgreSQL
		// transaction (SQLSTATE 25P02), making the re-fetch below fail.
		nb := tx.NarInfo.Create().SetHash(hash)
		applyNarInfoCreate(nb, narInfo)

		if err := nb.OnConflictColumns(entnarinfo.FieldHash).Ignore().Exec(ctx); err != nil {
			return nil, fmt.Errorf("error inserting the narinfo record for hash %q: %w", hash, err)
		}

		// Always SELECT after to get the row's ID, whether we inserted
		// it or a concurrent writer did.
		nir, err := narInfoByHash(ctx, tx.NarInfo, hash)
		if err != nil {
			return nil, fmt.Errorf("error fetching narinfo record after insert for hash %q: %w", hash, err)
		}

		// If the concurrent writer inserted a stub (URL=NULL), fill it
		// in now — otherwise the caller links an incomplete narinfo row.
		if nir.URL == nil || *nir.URL == "" {
			ub := tx.NarInfo.UpdateOneID(nir.ID)
			applyNarInfoUpdate(ub, narInfo)

			nir, err = ub.Save(ctx)
			if err != nil {
				return nil, fmt.Errorf("error updating raced stub narinfo record for hash %q: %w", hash, err)
			}
		}

		return nir, nil
	case err != nil:
		return nil, fmt.Errorf("error fetching the narinfo record for hash %q: %w", hash, err)
	case existing.URL == nil || *existing.URL == "":
		// Update stub
		ub := tx.NarInfo.UpdateOneID(existing.ID)
		applyNarInfoUpdate(ub, narInfo)

		nir, err := ub.Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("error updating the stub narinfo record for hash %q: %w", hash, err)
		}

		return nir, nil
	default:
		// Existing with non-empty URL → keep as-is
		return existing, nil
	}
}

// applyNarInfoCreate copies the nullable fields of a parsed narinfo
// onto an Ent create builder. Empty/zero values are dropped (the
// corresponding columns stay NULL) to match the legacy
// sql.NullString{Valid: v != ""} behaviour of CreateNarInfo.
func applyNarInfoCreate(nb *ent.NarInfoCreate, narInfo *narinfo.NarInfo) {
	if narInfo.StorePath != "" {
		nb.SetStorePath(narInfo.StorePath)
	}

	if narInfo.URL != "" {
		nb.SetURL(narInfo.URL)
	}

	if narInfo.Compression != "" {
		nb.SetCompression(narInfo.Compression)
	}

	if narInfo.FileHash != nil {
		nb.SetFileHash(narInfo.FileHash.String())
	}
	// nar_size is intentionally always set (Valid: true in legacy)
	//nolint:gosec // G115: NarSize/FileSize are non-negative by spec
	nb.SetNarSize(int64(narInfo.NarSize))

	if narInfo.FileSize != 0 {
		//nolint:gosec // G115: FileSize is non-negative by spec
		nb.SetFileSize(int64(narInfo.FileSize))
	}

	if narInfo.NarHash != nil {
		nb.SetNarHash(narInfo.NarHash.String())
	}

	if narInfo.Deriver != "" {
		nb.SetDeriver(narInfo.Deriver)
	}

	if narInfo.System != "" {
		nb.SetSystem(narInfo.System)
	}

	if narInfo.CA != "" {
		nb.SetCa(narInfo.CA)
	}
}

// applyNarInfoUpdate is the counterpart to applyNarInfoCreate for the
// stub-update path: clears columns whose source value is empty, sets
// the rest. Mirrors the legacy `WHERE url IS NULL` branch's behaviour.
func applyNarInfoUpdate(ub *ent.NarInfoUpdateOne, narInfo *narinfo.NarInfo) {
	if narInfo.StorePath != "" {
		ub.SetStorePath(narInfo.StorePath)
	} else {
		ub.ClearStorePath()
	}

	if narInfo.URL != "" {
		ub.SetURL(narInfo.URL)
	} else {
		ub.ClearURL()
	}

	if narInfo.Compression != "" {
		ub.SetCompression(narInfo.Compression)
	} else {
		ub.ClearCompression()
	}

	if narInfo.FileHash != nil {
		ub.SetFileHash(narInfo.FileHash.String())
	} else {
		ub.ClearFileHash()
	}

	//nolint:gosec // G115: NarSize is non-negative by spec
	ub.SetNarSize(int64(narInfo.NarSize))

	if narInfo.FileSize != 0 {
		//nolint:gosec // G115: FileSize is non-negative by spec
		ub.SetFileSize(int64(narInfo.FileSize))
	} else {
		ub.ClearFileSize()
	}

	if narInfo.NarHash != nil {
		ub.SetNarHash(narInfo.NarHash.String())
	} else {
		ub.ClearNarHash()
	}

	if narInfo.Deriver != "" {
		ub.SetDeriver(narInfo.Deriver)
	} else {
		ub.ClearDeriver()
	}

	if narInfo.System != "" {
		ub.SetSystem(narInfo.System)
	} else {
		ub.ClearSystem()
	}

	if narInfo.CA != "" {
		ub.SetCa(narInfo.CA)
	} else {
		ub.ClearCa()
	}
}

// addNarInfoReferences bulk-inserts narinfo_references rows.
// Conflicts on (narinfo_id, reference) become no-ops at the SQL
// level (Ent's OnConflict + DoNothing), matching the legacy
// AddNarInfoReferences's `ON CONFLICT (narinfo_id, reference) DO
// NOTHING`. DB-level dedup is required on Postgres because a Go-level
// "ignore duplicate key error" leaves the transaction in an aborted
// state.
func addNarInfoReferences(ctx context.Context, tx *ent.Tx, narinfoID int, refs []string) error {
	if len(refs) == 0 {
		return nil
	}

	bulk := make([]*ent.NarInfoReferenceCreate, len(refs))
	for i, ref := range refs {
		bulk[i] = tx.NarInfoReference.Create().SetNarinfoID(narinfoID).SetReference(ref)
	}

	if err := tx.NarInfoReference.CreateBulk(bulk...).
		OnConflictColumns(entnarinforeference.FieldNarinfoID, entnarinforeference.FieldReference).
		Ignore().
		Exec(ctx); err != nil {
		return fmt.Errorf("error inserting narinfo reference: %w", err)
	}

	return nil
}

// addNarInfoSignatures is the signature counterpart of addNarInfoReferences.
func addNarInfoSignatures(
	ctx context.Context,
	tx *ent.Tx,
	narinfoID int,
	signatures []signature.Signature,
) error {
	if len(signatures) == 0 {
		return nil
	}

	bulk := make([]*ent.NarInfoSignatureCreate, len(signatures))
	for i, sig := range signatures {
		bulk[i] = tx.NarInfoSignature.Create().SetNarinfoID(narinfoID).SetSignature(sig.String())
	}

	if err := tx.NarInfoSignature.CreateBulk(bulk...).
		OnConflictColumns(entnarinfosignature.FieldNarinfoID, entnarinfosignature.FieldSignature).
		Ignore().
		Exec(ctx); err != nil {
		return fmt.Errorf("error inserting narinfo signature: %w", err)
	}

	return nil
}

// createOrUpdateNarFileEnt is the Ent-backed counterpart to
// createOrUpdateNarFile: idempotently inserts a nar_files row for
// (hash, compression, query). If the row already exists, the
// last_accessed_at column is bumped to now via OnConflict.Update.
// Returns the int id (Ent's PK type for strong entities); callers
// that still need int64 should cast at the boundary. The §11.4 CDC
// conversions will phase out the int64 callers.
func createOrUpdateNarFileEnt(
	ctx context.Context,
	tx *ent.Tx,
	narURL nar.URL,
	fileSize uint64,
) (int, error) {
	id, err := tx.NarFile.Create().
		SetHash(narURL.Hash).
		SetCompression(narURL.Compression.String()).
		SetQuery(narURL.Query.Encode()).
		SetFileSize(fileSize).
		OnConflictColumns(
			entnarfile.FieldHash,
			entnarfile.FieldCompression,
			entnarfile.FieldQuery,
		).
		Update(func(u *ent.NarFileUpsert) {
			u.SetLastAccessedAt(time.Now())
		}).
		ID(ctx)
	if err != nil {
		return 0, fmt.Errorf("error upserting nar_file record: %w", err)
	}

	return id, nil
}

func (c *Cache) fixNarInfoFileSize(
	ctx context.Context,
	hash string,
	correctSize int64,
) error {
	ctx, span := tracer.Start(
		ctx,
		"cache.fixNarInfoFileSize",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
			attribute.Int64("correct_size", correctSize),
		),
	)
	defer span.End()

	zerolog.Ctx(ctx).
		Info().
		Int64("correct_size", correctSize).
		Msg("updating narinfo file size in the database")

	return c.withEntTransaction(ctx, "fixNarInfoFileSize", func(tx *ent.Tx) error {
		_, err := tx.NarInfo.Update().
			Where(entnarinfo.HashEQ(hash)).
			SetFileSize(correctSize).
			SetUpdatedAt(time.Now()).
			Save(ctx)

		return err
	})
}

// getNarActualSize returns the actual size of a NAR without triggering a
// streaming pipeline. It avoids calling c.GetNar() which would create an
// io.Pipe with a background goroutine, causing a spurious "pipe closed" error
// when the reader is closed before the goroutine finishes.
//
// Returns -1 if the size cannot be determined (NAR not found or not yet available).
func (c *Cache) getNarActualSize(ctx context.Context, nu nar.URL) (int64, error) {
	narFileRow, err := c.getNarFileFromDB(ctx, c.dbClient.Ent().NarFile, nu)
	if err != nil {
		if database.IsNotFoundError(err) {
			return -1, nil
		}

		return 0, fmt.Errorf("failed to get nar file from db: %w", err)
	}

	//nolint:gosec // G115: File size is non-negative
	return int64(narFileRow.FileSize), nil
}

// checkAndFixNarInfo checks if a NarInfo exists for the given hash, and if so,
// ensures its FileSize matches the actual NAR size.
func (c *Cache) CheckAndFixNarInfo(ctx context.Context, hash string) error {
	// First check if we have the NarInfo in DB using direct DB access
	// to avoid higher-level cache logic (like purging or storage checks)
	niRow, err := narInfoByHash(ctx, c.dbClient.Ent().NarInfo, hash)
	if err != nil {
		if database.IsNotFoundError(err) {
			return nil
		}

		return fmt.Errorf("failed to get narinfo from db: %w", err)
	}

	if niRow.URL == nil || *niRow.URL == "" {
		// No URL means not migrated or partial, can't check
		return nil
	}

	nu, err := nar.ParseURL(*niRow.URL)
	if err != nil {
		return fmt.Errorf("failed to parse nar url from narinfo: %w", err)
	}

	// FileSize must be null/0 for compression=none narinfos — this is correct by spec.
	// Nix ignores FileSize/FileHash for uncompressed NARs; do not overwrite with actual size.
	if nu.Compression == nar.CompressionTypeNone {
		return c.checkAndFixNarInfoNoCompression(ctx, hash, niRow)
	}

	// Determine the actual NAR size without triggering a streaming pipeline.
	// We check the store first (whole-file), then chunks (CDC). In both cases
	// we avoid calling c.GetNar() which would create an io.Pipe with a
	// background goroutine — closing the reader before the goroutine finishes
	// would log a spurious "pipe closed during NAR copy" error.
	hasNarInStore := c.HasNarInStore(ctx, nu)

	hasNarInChunks, err := c.HasNarInChunks(ctx, nu)
	if err != nil {
		return fmt.Errorf("failed to check if nar exists in chunks: %w", err)
	}

	if !hasNarInStore && !hasNarInChunks {
		return nil
	}

	size, err := c.getNarActualSize(ctx, nu)
	if err != nil {
		return err
	}

	if size < 0 {
		// Size unknown or NAR not yet available; skip check.
		return nil
	}

	currentFileSize := derefInt64Ptr(niRow.FileSize)
	if size != currentFileSize {
		zerolog.Ctx(ctx).
			Info().
			Int64("current_size", currentFileSize).
			Int64("actual_size", size).
			Msg("mismatch detected, fixing narinfo file size")

		return c.fixNarInfoFileSize(ctx, hash, size)
	}

	return nil
}

func (c *Cache) checkAndFixNarInfoNoCompression(ctx context.Context, hash string, niRow *ent.NarInfo) error {
	// For compression=none, FileSize must be 0/NULL. If it's not, fix it.
	if niRow.FileSize != nil && *niRow.FileSize != 0 {
		zerolog.Ctx(ctx).
			Info().
			Int64("current_size", *niRow.FileSize).
			Msg("mismatch detected for compression=none, fixing narinfo file size to NULL")

		if err := c.withEntTransaction(ctx, "fixNarInfoFileSizeToNull", func(tx *ent.Tx) error {
			_, err := tx.NarInfo.Update().
				Where(entnarinfo.HashEQ(hash)).
				ClearFileSize().
				Save(ctx)

			return err
		}); err != nil {
			return fmt.Errorf("failed to fix narinfo file size to NULL: %w", err)
		}
	}

	// For compression=none, FileHash must be NULL. If it's not, fix it.
	if niRow.FileHash != nil && *niRow.FileHash != "" {
		zerolog.Ctx(ctx).
			Info().
			Str("current_file_hash", *niRow.FileHash).
			Msg("mismatch detected for compression=none, fixing narinfo file hash to NULL")

		if err := c.withEntTransaction(ctx, "fixNarInfoFileHashToNull", func(tx *ent.Tx) error {
			_, err := tx.NarInfo.Update().
				Where(entnarinfo.HashEQ(hash)).
				ClearFileHash().
				Save(ctx)

			return err
		}); err != nil {
			return fmt.Errorf("failed to fix narinfo file hash to NULL: %w", err)
		}
	}

	return nil
}

// checkAndFixNarInfosForNar finds all NarInfos pointing to the given NAR URL
// and fixes their file size if needed.
func (c *Cache) checkAndFixNarInfosForNar(ctx context.Context, narURL nar.URL) error {
	// The NarInfo URL usually matches the NAR URL.
	nis, err := c.dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.URL(narURL.String())).
		All(ctx)
	if err != nil {
		return fmt.Errorf("failed to get narinfo hashes by url: %w", err)
	}

	var errs []error

	for _, ni := range nis {
		if err := c.CheckAndFixNarInfo(ctx, ni.Hash); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// narFileSize returns the size to store in nar_files.file_size.
// For Compression:none narinfos, FileSize is intentionally 0 (omitted from narinfo output),
// but nar_files.file_size must hold a meaningful size for LRU eviction (GetNarTotalSize sums it).
// NarSize equals the uncompressed NAR size and is the correct fallback for Compression:none.
func narFileSize(ni *narinfo.NarInfo) uint64 {
	if ni.FileSize != 0 {
		return ni.FileSize
	}

	return ni.NarSize
}

// MigrateNarInfoToDatabase migrates a single narinfo from storage to the database.
// It uses distributed locking to coordinate with other instances (if Redis is configured).
// This is a convenience wrapper around MigrateNarInfo for use within the Cache.
func (c *Cache) MigrateNarInfoToDatabase(
	ctx context.Context,
	hash string,
	ni *narinfo.NarInfo,
	deleteFromStorage bool,
) error {
	var narInfoStore storage.NarInfoStore
	if deleteFromStorage {
		narInfoStore = c.narInfoStore
	}

	return MigrateNarInfo(ctx, c.downloadLocker, c.dbClient, narInfoStore, hash, ni)
}

// MigrateNarInfo migrates a single narinfo from storage to the database.
// It uses distributed locking to coordinate with other instances (if a distributed locker is provided).
// This function is used both by Cache.MigrateNarInfoToDatabase and the CLI migrate-narinfo command.
//
// Parameters:
//   - ctx: Context for the operation
//   - locker: Distributed locker for coordination (can be in-memory for single-instance)
//   - db: Database querier (legacy; kept for the pre-lock double-check
//     until §11 fully retires the Querier surface)
//   - dbClient: Ent-backed client used by storeNarInfoInDatabase for
//     the actual UPSERT pipeline
//   - narInfoStore: Optional storage backend to delete from after migration (nil to skip deletion)
//   - hash: The narinfo hash to migrate
//   - ni: The parsed narinfo to migrate
//
// Returns an error if migration fails. Returns nil if the narinfo is already migrated or
// if another instance is currently migrating it.
func MigrateNarInfo(
	ctx context.Context,
	locker lock.Locker,
	dbClient *database.Client,
	narInfoStore storage.NarInfoStore,
	hash string,
	ni *narinfo.NarInfo,
) error {
	// Use a short-lived, non-blocking lock to coordinate migrations and prevent a "thundering herd".
	lockKey := migrationLockKey(hash)

	acquired, err := locker.TryLock(ctx, lockKey, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to acquire migration lock: %w", err)
	}

	if !acquired {
		// If lock is not acquired, another process is already handling it.
		// This is not an error - the migration is being handled elsewhere.
		zerolog.Ctx(ctx).Debug().Str("narinfo_hash", hash).Msg("migration already in progress by another instance")

		return nil
	}

	defer func() {
		if err := locker.Unlock(context.WithoutCancel(ctx), lockKey); err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Str("narinfo_hash", hash).Msg("failed to release migration lock")
		}
	}()

	// Double check if already migrated after acquiring the lock.
	// This prevents multiple sequential migrations for the same narinfo.
	nir, err := dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.HashEQ(hash)).
		Only(ctx)

	switch {
	case err == nil:
		if nir.URL != nil && *nir.URL != "" {
			zerolog.Ctx(ctx).Debug().
				Str("narinfo_hash", hash).
				Msg("migration completed by another instance while waiting for lock")

			return nil
		}
	case database.IsNotFoundError(err):
		// Expected: narinfo not yet in DB; fall through to migrate.
	default:
		// Unexpected DB error — log but still attempt migration; if the
		// DB is genuinely broken the upsert below will surface the error.
		zerolog.Ctx(ctx).Warn().Err(err).
			Str("narinfo_hash", hash).
			Msg("double-check NarInfo lookup failed; proceeding with migration")
	}

	log := zerolog.Ctx(ctx).With().Str("narinfo_hash", hash).Logger()

	log.Info().Msg("migrating narinfo to database")

	opStartTime := time.Now()

	migrateAttrs := []attribute.KeyValue{
		attribute.String("migration_type", migrationTypeNarInfoToDB),
		attribute.String("operation", migrationOperationMigrate),
	}

	// Store narinfo in database using the UPSERT logic from storeInDatabase
	err = storeNarInfoInDatabase(ctx, dbClient, hash, ni)
	if err != nil {
		log.Error().Err(err).Msg("failed to migrate narinfo to database")

		backgroundMigrationObjectsTotal.Add(
			ctx, 1,
			metric.WithAttributes(
				append(migrateAttrs, attribute.String("result", migrationResultFailure))...,
			),
		)
		backgroundMigrationDuration.Record(
			ctx, time.Since(opStartTime).Seconds(),
			metric.WithAttributes(migrateAttrs...),
		)

		return fmt.Errorf("failed to store narinfo in database: %w", err)
	}

	log.Debug().Dur("duration", time.Since(opStartTime)).Msg("successfully migrated narinfo to database")

	backgroundMigrationObjectsTotal.Add(
		ctx, 1,
		metric.WithAttributes(
			append(migrateAttrs, attribute.String("result", migrationResultSuccess))...,
		),
	)
	backgroundMigrationDuration.Record(
		ctx, time.Since(opStartTime).Seconds(),
		metric.WithAttributes(migrateAttrs...),
	)

	// Only delete from storage if narInfoStore is provided
	if narInfoStore != nil {
		deleteStartTime := time.Now()
		deleteAttrs := []attribute.KeyValue{
			attribute.String("migration_type", migrationTypeNarInfoToDB),
			attribute.String("operation", migrationOperationDelete),
		}

		if err := narInfoStore.DeleteNarInfo(ctx, hash); err != nil {
			log.Error().Err(err).Msg("failed to delete narinfo from store after migration")
			backgroundMigrationObjectsTotal.Add(
				ctx, 1,
				metric.WithAttributes(
					append(deleteAttrs, attribute.String("result", migrationResultFailure))...,
				),
			)
			// Don't return error - migration succeeded, only cleanup failed
		} else {
			log.Debug().Msg("deleted narinfo from storage after successful migration")
			backgroundMigrationObjectsTotal.Add(
				ctx, 1,
				metric.WithAttributes(
					append(deleteAttrs, attribute.String("result", migrationResultSuccess))...,
				),
			)
		}

		backgroundMigrationDuration.Record(
			ctx, time.Since(deleteStartTime).Seconds(),
			metric.WithAttributes(deleteAttrs...),
		)
	}

	return nil
}

// storeNarInfoInDatabase is the Cache-free counterpart to
// Cache.storeInDatabase. Used by MigrateNarInfo (which has no *Cache
// reference). Shares the same Ent helpers — upsertNarInfoFromParsed,
// addNarInfoReferences, addNarInfoSignatures, createOrUpdateNarFileEnt
// — defined alongside Cache.storeInDatabase.
func storeNarInfoInDatabase(
	ctx context.Context,
	dbClient *database.Client,
	hash string,
	narInfo *narinfo.NarInfo,
) error {
	return withEntTransactionRetry(ctx, dbClient, "storeNarInfoInDatabase", func(tx *ent.Tx) error {
		nir, err := upsertNarInfoFromParsed(ctx, tx, hash, narInfo)
		if err != nil {
			return err
		}

		if err := addNarInfoReferences(ctx, tx, nir.ID, narInfo.References); err != nil {
			return err
		}

		if err := addNarInfoSignatures(ctx, tx, nir.ID, narInfo.Signatures); err != nil {
			return err
		}

		narURL, err := nar.ParseURL(narInfo.URL)
		if err != nil {
			return fmt.Errorf("error parsing the nar URL: %w", err)
		}

		normalizedNarURL, err := narURL.Normalize()
		if err != nil {
			return fmt.Errorf("error normalizing the nar URL: %w", err)
		}

		narFileID, err := createOrUpdateNarFileEnt(ctx, tx, normalizedNarURL, narFileSize(narInfo))
		if err != nil {
			return err
		}

		if err := tx.NarInfoNarFile.Create().
			SetNarinfoID(nir.ID).
			SetNarFileID(narFileID).
			OnConflictColumns(entnarinfonarfile.FieldNarinfoID, entnarinfonarfile.FieldNarFileID).
			Ignore().
			Exec(ctx); err != nil {
			return fmt.Errorf("error linking narinfo to nar_file: %w", err)
		}

		return nil
	})
}

func (c *Cache) backgroundMigrateNarInfo(ctx context.Context, hash string, ni *narinfo.NarInfo) {
	// We use a detached context because this is a background job.
	// But we keep the trace from the request context.
	detachedCtx := context.WithoutCancel(ctx)

	c.backgroundWG.Add(1)
	analytics.SafeGo(detachedCtx, func() {
		defer c.backgroundWG.Done()

		// Call the exported migration function with deletion enabled
		if err := c.MigrateNarInfoToDatabase(detachedCtx, hash, ni, true); err != nil {
			zerolog.Ctx(detachedCtx).Error().Err(err).Str("narinfo_hash", hash).Msg("background migration failed")
		}
	})
}

func (c *Cache) deleteNarInfoFromStore(ctx context.Context, hash string) error {
	ctx, span := tracer.Start(
		ctx,
		"cache.deleteNarInfoFromStore",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	// Check if narinfo exists in storage or database
	inStorage := c.narInfoStore.HasNarInfo(ctx, hash)

	_, err := c.dbClient.Ent().NarInfo.Query().Where(entnarinfo.HashEQ(hash)).First(ctx)
	if err != nil && !database.IsNotFoundError(err) {
		return fmt.Errorf("error checking for narinfo in database: %w", err)
	}

	inDatabase := err == nil

	zerolog.Ctx(ctx).Debug().
		Bool("in_storage", inStorage).
		Bool("in_database", inDatabase).
		Msg("narinfo existence check before delete")

	if !inStorage && !inDatabase {
		return storage.ErrNotFound
	}

	// Delete from database (includes cascading deletes for references, signatures, and links)
	if inDatabase {
		if _, err := c.dbClient.Ent().NarInfo.Delete().
			Where(entnarinfo.HashEQ(hash)).
			Exec(ctx); err != nil {
			return fmt.Errorf("error deleting narinfo from the database: %w", err)
		}

		zerolog.Ctx(ctx).Debug().Msg("narinfo deleted from database")
	}

	// Delete from storage if present
	if inStorage {
		if err := c.narInfoStore.DeleteNarInfo(ctx, hash); err != nil {
			return fmt.Errorf("error deleting narinfo from storage: %w", err)
		}

		zerolog.Ctx(ctx).Debug().Msg("narinfo deleted from storage backend")
	}

	return nil
}

func (c *Cache) validateHostname(hostName string) error {
	if hostName == "" {
		return ErrHostnameRequired
	}

	u, err := url.Parse(hostName)
	if err != nil {
		return fmt.Errorf("error parsing the hostName %q: %w", hostName, err)
	}

	if u.Scheme != "" {
		return ErrHostnameMustNotContainScheme
	}

	if strings.Contains(hostName, "/") {
		return ErrHostnameMustNotContainPath
	}

	return nil
}

func (c *Cache) setupSecretKey(ctx context.Context, secretKeyPath string) error {
	// 1. If a secret key path is provided, load it from there
	if secretKeyPath != "" {
		return c.setupSecretKeyFromFile(ctx, secretKeyPath)
	}

	// 2. Try to load from the database
	if dbKeyStr, err := c.config.GetSecretKey(ctx); err == nil {
		c.secretKey, err = signature.LoadSecretKey(dbKeyStr)
		if err != nil {
			return fmt.Errorf("error loading the secret key from the database: %w", err)
		}

		zerolog.Ctx(ctx).Debug().Msg("loaded secret key from database")

		return nil
	} else if !errors.Is(err, config.ErrConfigNotFound) && !database.IsNotFoundError(err) {
		return fmt.Errorf("error fetching the secret key from the database: %w", err)
	}

	// 3. Try to load from the deprecated config store (FS/S3)
	// If found, migrate to DB and delete from store
	//nolint:staticcheck // Migration logic requires using the deprecated interface
	if oldKey, err := c.configStore.GetSecretKey(ctx); err == nil {
		c.secretKey = oldKey

		// Migrate to DB
		if err := c.config.SetSecretKey(ctx, c.secretKey.String()); err != nil {
			return fmt.Errorf("error storing the migrated secret key in the database: %w", err)
		}

		// Delete from old store
		//nolint:staticcheck // Migration logic requires using the deprecated interface
		if err := c.configStore.DeleteSecretKey(ctx); err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Msg("failed to delete the secret key from the deprecated config store")
		}

		zerolog.Ctx(ctx).Info().Msg("migrated secret key from config store to database")

		return nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("error fetching the secret key from the config store: %w", err)
	}

	// 4. Generate a new key and store it in the database
	secretKey, _, err := signature.GenerateKeypair(c.hostName, nil)
	if err != nil {
		return fmt.Errorf("error generating a secret key pair: %w", err)
	}

	c.secretKey = secretKey

	if err := c.config.SetSecretKey(ctx, secretKey.String()); err != nil {
		return fmt.Errorf("error storing the generated secret key in the database: %w", err)
	}

	zerolog.Ctx(ctx).Info().Msg("generated and stored a new secret key in the database")

	return nil
}

func (c *Cache) setupSecretKeyFromFile(ctx context.Context, secretKeyPath string) error {
	skc, err := os.ReadFile(secretKeyPath)
	if err != nil {
		return fmt.Errorf("error reading the given secret key located at %q: %w", secretKeyPath, err)
	}

	c.secretKey, err = signature.LoadSecretKey(string(skc))
	if err != nil {
		return fmt.Errorf("error loading the given secret key located at %q: %w", secretKeyPath, err)
	}

	zerolog.Ctx(ctx).Debug().Str("path", secretKeyPath).Msg("loaded secret key from file")

	// Store it in the database if it doesn't exist or is different
	// We ignore the error here because we don't want to fail if the DB is down or read-only
	// The primary source of truth is the file in this case.
	dbKeyStr, err := c.config.GetSecretKey(ctx)
	if err != nil || dbKeyStr != c.secretKey.String() {
		if err := c.config.SetSecretKey(ctx, c.secretKey.String()); err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Msg("failed to store the secret key in the database")
		}
	}

	return nil
}

func (c *Cache) hasUpstreamJob(hash string) bool {
	c.upstreamJobsMu.Lock()
	defer c.upstreamJobsMu.Unlock()

	_, narJobExists := c.upstreamJobs[narJobKey(hash)]

	return narJobExists
}

func (c *Cache) isRemoteDownloadInProgress(ctx context.Context, hash string) bool {
	// Try to acquire the lock. If it fails, someone else has it.
	// We use a very short TTL since we'll release it immediately if we get it.
	locked, err := c.downloadLocker.TryLock(ctx, narJobKey(hash), 10*time.Second)
	if err != nil {
		return false
	}

	if !locked {
		return true
	}

	// We got the lock! No one else was downloading it remotely.
	// We must release it immediately.
	if err := c.downloadLocker.Unlock(context.WithoutCancel(ctx), narJobKey(hash)); err != nil {
		zerolog.Ctx(ctx).
			Warn().
			Err(err).
			Str("hash", hash).
			Msg("failed to unlock after TryLock check in isRemoteDownloadInProgress")
	}

	return false
}

// coordinateDownload manages distributed download coordination with lock acquisition,
// storage checking, and job tracking. Returns a downloadState that can be monitored
// for download progress and errors.
// The coordCtx is used for coordination (responding to caller's cancellation),
// while ctx is used for the download itself (may be detached to allow background downloads).
func (c *Cache) coordinateDownload(
	coordCtx context.Context,
	ctx context.Context,
	lockKey string,
	hash string,
	waitForStorage bool,
	hasAsset func(context.Context) bool,
	startJob func(*downloadState),
) *downloadState {
	// First check local jobs to avoid blocking on distributed lock if already downloading locally
	c.upstreamJobsMu.Lock()

	if ds, ok := c.upstreamJobs[lockKey]; ok {
		c.upstreamJobsMu.Unlock()

		completionChan := ds.stored
		if !waitForStorage {
			completionChan = ds.start
		}

		select {
		case <-completionChan:
			// Desired state reached (start or stored)
		case <-ds.done:
			// Download completed (successfully or with error)
		case <-coordCtx.Done():
			// Caller context canceled
			return ds
		}

		return ds
	}

	c.upstreamJobsMu.Unlock()

	// Acquire lock with retry (handled internally by Redis locker)
	if err := c.downloadLocker.Lock(ctx, lockKey, c.downloadLockTTL); err != nil {
		zerolog.Ctx(ctx).Warn().
			Err(err).
			Str("hash", hash).
			Str("lock_key", lockKey).
			Msg("failed to acquire download lock, will poll storage and re-attempt acquisition")

		// Another server holds the lock. Rather than dead-ending in an HTTP 500,
		// poll storage for the asset while periodically re-attempting lock
		// acquisition so we can take over if the holder finishes or fails.
		ds, tookOver := c.pollForDownloadOrTakeOver(coordCtx, ctx, lockKey, hash, err, hasAsset)
		if !tookOver {
			return ds
		}

		// Fell through: we re-acquired the lock and now own the download.
		// Continue into the normal post-lock path below.
	}

	// Start a background goroutine to refresh the lock TTL periodically.
	stopRefresher := lock.StartRefresher(ctx, c.downloadLocker, lockKey, c.downloadLockTTL)

	// Double check local jobs and asset presence under lock
	if hasAsset(ctx) {
		stopRefresher()

		// Release the lock before returning
		if err := c.downloadLocker.Unlock(context.WithoutCancel(ctx), lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Str("hash", hash).
				Str("lock_key", lockKey).
				Msg("failed to release download lock")
		}

		zerolog.Ctx(ctx).Debug().
			Str("hash", hash).
			Msg("asset already in storage, skipping download")

		// Return a completed downloadState
		ds := newDownloadState()
		ds.closed = true
		ds.startOnce.Do(func() { close(ds.start) })
		ds.storedOnce.Do(func() { close(ds.stored) })
		ds.doneOnce.Do(func() { close(ds.done) })

		return ds
	}

	c.upstreamJobsMu.Lock()

	ds, ok := c.upstreamJobs[lockKey]
	if !ok {
		ds = newDownloadState()
		c.upstreamJobs[lockKey] = ds

		// Start download in background
		c.backgroundWG.Add(1)
		analytics.SafeGo(ctx, func() {
			defer c.backgroundWG.Done()

			startJob(ds)
		})
	}

	c.upstreamJobsMu.Unlock()

	// Wait for the requested state (started or stored)
	completionChan := ds.stored
	if !waitForStorage {
		completionChan = ds.start
	}

	select {
	case <-completionChan:
		// Desired state reached
	case <-ds.done:
		// Download completed
	case <-coordCtx.Done():
		// Caller context canceled
	}

	// Release the download lock with different strategies based on waitForStorage:
	// - waitForStorage=true (NarInfo): Release immediately after asset is stored.
	//   NarInfo operations require full completion before serving to clients.
	// - waitForStorage=false (NAR): Release in background after storage completes.
	//   This allows immediate streaming to clients while preventing other instances
	//   from starting redundant downloads. The lock is held until storage completes.
	if waitForStorage {
		stopRefresher()

		if err := c.downloadLocker.Unlock(context.WithoutCancel(ctx), lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Str("hash", hash).
				Str("lock_key", lockKey).
				Msg("failed to release download lock")
		}
	} else {
		c.backgroundWG.Add(1)
		analytics.SafeGo(ctx, func() {
			defer c.backgroundWG.Done()
			defer stopRefresher()

			select {
			case <-ds.stored:
			case <-ds.done:
			}

			if err := c.downloadLocker.Unlock(context.WithoutCancel(ctx), lockKey); err != nil {
				zerolog.Ctx(ctx).Error().
					Err(err).
					Str("hash", hash).
					Str("lock_key", lockKey).
					Msg("failed to release download lock in background")
			}
		})
	}

	return ds
}

// pollForDownloadOrTakeOver runs the distributed fallback when the download lock
// is held by another server. It polls storage for the asset while periodically
// re-attempting lock acquisition, up to a bound of max(downloadLockTTL,
// downloadPollTimeout) — the window during which a live holder could still be
// refreshing its lock and making progress on a large NAR.
//
// It returns either:
//   - (ds, false): a terminal downloadState the caller should return directly —
//     a completed state if the asset appeared, or an errored state (a cache miss
//     via storage.ErrNotFound, or the caller's context error); or
//   - (nil, true): the lock was re-acquired, so the caller now owns the download
//     and should continue into the normal post-lock path. Taking over only after
//     re-acquisition keeps downloads serialized (at most one per hash across the
//     cluster), so the concurrent-CDC path is never exercised by this fallback.
func (c *Cache) pollForDownloadOrTakeOver(
	coordCtx context.Context,
	ctx context.Context,
	lockKey string,
	hash string,
	initialErr error,
	hasAsset func(context.Context) bool,
) (*downloadState, bool) {
	const pollInterval = 200 * time.Millisecond

	giveUpBound := c.downloadLockTTL
	if c.downloadPollTimeout > giveUpBound {
		giveUpBound = c.downloadPollTimeout
	}

	deadlineCtx, cancel := context.WithTimeout(coordCtx, giveUpBound)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if hasAsset(coordCtx) {
				zerolog.Ctx(ctx).Debug().
					Str("hash", hash).
					Msg("asset appeared in storage while polling (downloaded by another server)")

				downloadCoordinationFallbackTotal.Add(ctx, 1,
					metric.WithAttributes(attribute.String("outcome", "served_by_peer")))

				// Return a completed downloadState.
				ds := newDownloadState()
				ds.closed = true
				ds.startOnce.Do(func() { close(ds.start) })
				ds.storedOnce.Do(func() { close(ds.stored) })
				ds.doneOnce.Do(func() { close(ds.done) })

				return ds, false
			}

			// Re-attempt acquisition without blocking, bounded by the give-up
			// deadline. TryLock is a single non-blocking attempt, so a tick can
			// never stall the poll loop or outlive deadlineCtx / caller
			// cancellation (a blocking Lock retries internally and could do
			// exactly that). Success means the previous holder released the lock
			// (it finished or failed) without the asset appearing, so we take
			// over as the sole downloader.
			acquired, lockErr := c.downloadLocker.TryLock(deadlineCtx, lockKey, c.downloadLockTTL)
			if lockErr == nil && acquired {
				zerolog.Ctx(ctx).Debug().
					Str("hash", hash).
					Str("lock_key", lockKey).
					Msg("re-acquired download lock, taking over the download")

				downloadCoordinationFallbackTotal.Add(ctx, 1,
					metric.WithAttributes(attribute.String("outcome", "take_over")))

				return nil, true
			}
		case <-deadlineCtx.Done():
			ds := newDownloadState()

			// Distinguish caller cancellation (the client went away) from our own
			// give-up. Caller cancellation surfaces as a context error, which the
			// server treats as "no response"; a genuine give-up surfaces as a
			// cache miss so Nix falls back to another substituter instead of
			// retrying a 500.
			if coordCtx.Err() != nil {
				downloadCoordinationFallbackTotal.Add(ctx, 1,
					metric.WithAttributes(attribute.String("outcome", "caller_canceled")))

				ds.downloadError = coordCtx.Err()
			} else {
				zerolog.Ctx(ctx).Warn().
					Err(initialErr).
					Str("hash", hash).
					Str("lock_key", lockKey).
					Dur("give_up_bound", giveUpBound).
					Msg("gave up waiting for download by another server, returning cache miss")

				downloadCoordinationFallbackTotal.Add(ctx, 1,
					metric.WithAttributes(attribute.String("outcome", "give_up")))

				ds.downloadError = fmt.Errorf(
					"gave up after %s waiting for another server to download %q: %w",
					giveUpBound, hash, storage.ErrNotFound,
				)
			}

			// Signal that the download is done (with error) to prevent deadlocks.
			ds.startOnce.Do(func() { close(ds.start) })
			ds.storedOnce.Do(func() { close(ds.stored) })
			ds.doneOnce.Do(func() { close(ds.done) })

			return ds, false
		}
	}
}

// withEntTransaction wraps c.dbClient.WithTransaction with the same
// deadlock/duplicate-key retry policy the package-level
// withEntTransactionRetry provides.
func (c *Cache) withEntTransaction(ctx context.Context, operation string, fn func(tx *ent.Tx) error) error {
	return withEntTransactionRetry(ctx, c.dbClient, operation, fn)
}

// withEntTransactionRetry wraps dbClient.WithTransaction with the
// same deadlock-retry policy the legacy *Cache.executeTransaction
// helper provided. Package-level so it can be reused by callers that
// don't hold a *Cache (storeNarInfoInDatabase running under
// MigrateNarInfo / the CLI migrate-narinfo path).
func withEntTransactionRetry(
	ctx context.Context,
	dbClient *database.Client,
	operation string,
	fn func(tx *ent.Tx) error,
) error {
	const (
		maxAttempts  = 5
		initialDelay = 50 * time.Millisecond
	)

	var (
		err   error
		delay = initialDelay
	)

	zerolog.Ctx(ctx).Debug().
		Str("operation", operation).
		Msg("withEntTransaction: starting transaction")

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err = dbClient.WithTransaction(ctx, operation, fn)
		if err == nil {
			zerolog.Ctx(ctx).Debug().
				Str("operation", operation).
				Msg("withEntTransaction: transaction committed successfully")

			return nil
		}

		// Retry on deadlock/busy AND duplicate-key — concurrent
		// transactions doing a "select-then-insert" can both see the
		// row as missing, both attempt INSERT, and the loser gets a
		// 23505/1062. Re-running the closure picks the existing row
		// up via the SELECT and takes the UPDATE branch instead.
		retryable := database.IsDeadlockError(err) || database.IsDuplicateKeyError(err)
		if !retryable {
			return err
		}

		if attempt == maxAttempts {
			break
		}

		zerolog.Ctx(ctx).
			Warn().
			Err(err).
			Str("operation", operation).
			Int("attempt", attempt).
			Dur("delay", delay).
			Msg("retryable transaction error (deadlock/busy/duplicate-key), retrying")

		// time.NewTimer + Stop instead of time.After to avoid leaking
		// the underlying timer (and its goroutine on pre-1.23 runtimes)
		// for the full delay duration when ctx is cancelled mid-wait.
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()

			return ctx.Err()
		case <-timer.C:
			delay *= 2
		}
	}

	return fmt.Errorf("transaction for %s failed after %d attempts: %w", operation, maxAttempts, err)
}

// withReadLock executes fn while holding a read lock with the specified key.
// It automatically handles lock acquisition, release, and error logging.
func (c *Cache) withReadLock(ctx context.Context, operation string, lockKey string, fn func() error) error {
	if err := c.cacheLocker.RLock(ctx, lockKey, c.cacheLockTTL); err != nil {
		zerolog.Ctx(ctx).Error().
			Err(err).
			Str("operation", operation).
			Msg("failed to acquire read lock")

		return fmt.Errorf("failed to acquire read lock for %s: %w", operation, err)
	}

	defer func() {
		if err := c.cacheLocker.RUnlock(context.WithoutCancel(ctx), lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Str("operation", operation).
				Msg("failed to release read lock")
		}
	}()

	return fn()
}

// withWriteLock executes fn while holding a write lock with the specified key.
// It automatically handles lock acquisition, release, and error logging.
func (c *Cache) withWriteLock(ctx context.Context, operation string, lockKey string, fn func() error) error {
	if err := c.cacheLocker.Lock(ctx, lockKey, c.cacheLockTTL); err != nil {
		zerolog.Ctx(ctx).Error().
			Err(err).
			Str("operation", operation).
			Str("lock_key", lockKey).
			Msg("failed to acquire write lock")

		return fmt.Errorf("failed to acquire write lock for %s: %w", operation, err)
	}

	defer func() {
		if err := c.cacheLocker.Unlock(context.WithoutCancel(ctx), lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Str("operation", operation).
				Str("lock_key", lockKey).
				Msg("failed to release write lock")
		}
	}()

	return fn()
}

// withTryLock attempts to execute fn while holding a write lock with the specified key.
// If the lock cannot be acquired, it returns (false, nil) immediately without executing fn.
// If the lock is acquired successfully, it executes fn and returns (true, error).
// It automatically handles lock release and error logging.
func (c *Cache) withTryLock(ctx context.Context, operation string, lockKey string, fn func() error) (bool, error) {
	acquired, err := c.cacheLocker.TryLock(ctx, lockKey, c.cacheLockTTL)
	if err != nil {
		zerolog.Ctx(ctx).Error().
			Err(err).
			Str("operation", operation).
			Str("lock_key", lockKey).
			Msg("error trying to acquire write lock")

		return false, fmt.Errorf("error trying to acquire write lock for %s: %w", operation, err)
	}

	if !acquired {
		return false, nil
	}

	defer func() {
		if err := c.cacheLocker.Unlock(context.WithoutCancel(ctx), lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Str("operation", operation).
				Str("lock_key", lockKey).
				Msg("failed to release write lock")
		}
	}()

	err = fn()

	return true, err
}

// calculateCleanupSize validates the total NAR size and calculates how much needs to be cleaned up.
// Returns 0 if no cleanup is needed.
func (c *Cache) calculateCleanupSize(ctx context.Context, tx *ent.Tx, log zerolog.Logger) (uint64, error) {
	narTotalSize, err := totalNarFileSize(ctx, tx.NarFile)
	if err != nil {
		log.Error().Err(err).Msg("error fetching the total nar size")

		return 0, err
	}

	if narTotalSize == 0 {
		log.Info().Msg("SUM(file_size) is zero, nothing to clean up")

		return 0, nil
	}

	log = log.With().Int64("nar_total_size", narTotalSize).Logger()

	//nolint:gosec // G115: SUM over nar_files.file_size (a uint64 column) is non-negative
	if uint64(narTotalSize) <= c.maxSize {
		log.Info().Msg("store size is less than max-size, not removing any nars")

		return 0, nil
	}

	//nolint:gosec // G115: SUM over nar_files.file_size (a uint64 column) is non-negative
	cleanupSize := uint64(narTotalSize) - c.maxSize

	log = log.With().Uint64("cleanup_size", cleanupSize).Logger()
	log.Info().Msg("going to remove nars")

	return cleanupSize, nil
}

// deleteLRURecordsFromDB identifies the least used NarInfos, deletes them,
// and then cleans up any NarFiles that became orphaned as a result.
func (c *Cache) deleteLRURecordsFromDB(
	ctx context.Context,
	tx *ent.Tx,
	log zerolog.Logger,
	cleanupSize uint64,
	pinnedHashes map[string]struct{},
) ([]string, []nar.URL, []string, error) {
	// 1. METADATA PHASE
	// Find the NarInfos that constitute the oldest `cleanupSize` worth of
	// data. We fetch in LRU order (last_accessed_at ASC, id ASC) with the
	// linked nar_file eager-loaded, then take the longest PREFIX whose
	// cumulative file_size stays within ~2× cleanupSize bytes. The 2×
	// overshoot mirrors the legacy GetLeastUsedNarInfos correlated-subquery
	// filter: it provides slack for skipping pinned narinfos without
	// pulling the whole table. The actual deletion loop then walks this
	// prefix and stops as soon as it has freed cleanupSize bytes (or runs
	// out of candidates) — never over-evicting beyond the budget.
	const maxFetchRows = 10000 // hard cap so we never load the whole table

	candidates, err := tx.NarInfo.Query().
		Order(
			ent.Asc(entnarinfo.FieldLastAccessedAt),
			ent.Asc(entnarinfo.FieldID),
		).
		WithNarInfoNarFiles(func(q *ent.NarInfoNarFileQuery) {
			q.WithNarFile()
		}).
		Limit(maxFetchRows).
		All(ctx)
	if err != nil {
		log.Error().Err(err).Msg("error getting least used narinfos")

		return nil, nil, nil, err
	}

	if len(candidates) == 0 {
		log.Warn().Msg("cleanup required but no reclaimable narinfos found")

		return nil, nil, nil, nil
	}

	if len(candidates) == maxFetchRows {
		log.Warn().Int("limit", maxFetchRows).Msg(
			"LRU candidate fetch hit the row cap; narinfos beyond this " +
				"window are not considered for eviction in this pass",
		)
	}

	// Apply the legacy cumulative-byte filter: keep the LRU prefix whose
	// cumulative file_size is <= max(2*cleanupSize, smallest-single-row).
	// For cleanupSize == 0 ("delete all") keep every candidate.
	narInfoFileSize := func(info *ent.NarInfo) uint64 {
		for _, link := range info.Edges.NarInfoNarFiles {
			if link.Edges.NarFile != nil {
				return link.Edges.NarFile.FileSize
			}
		}

		return 0
	}

	narInfosToDelete := candidates

	if cleanupSize > 0 {
		// Compute byteBudget = 2*cleanupSize, saturating on overflow.
		byteBudget := cleanupSize * 2
		if byteBudget < cleanupSize {
			byteBudget = math.MaxUint64
		}

		var cumulative uint64

		narInfosToDelete = narInfosToDelete[:0]

		for _, info := range candidates {
			next := cumulative + narInfoFileSize(info)
			// Always include the first narinfo so we make progress even
			// when its single file_size already exceeds the budget. This
			// matches the legacy SQL, which returns rows whose cumulative
			// (including themselves) is <= budget — the first row's
			// "cumulative" is its own file_size.
			if next > byteBudget && len(narInfosToDelete) > 0 {
				break
			}

			cumulative = next

			narInfosToDelete = append(narInfosToDelete, info)
		}
	}

	if len(narInfosToDelete) == 0 {
		log.Warn().Msg("cleanup required but no reclaimable narinfos found")

		return nil, nil, nil, nil
	}

	log.Info().Int("count", len(narInfosToDelete)).Msg("found narinfos to expire")

	// Track hashes to remove from the in-memory/disk store later
	narInfoHashesToRemove := make([]string, 0, len(narInfosToDelete))

	var totalSize uint64

	// Delete the NarInfos from the database.
	// This breaks the link between the Metadata and the Storage.
	// Skip any narinfos that are in the pinned closure.
	for _, info := range narInfosToDelete {
		// Skip if this narinfo is in the pinned closure
		if _, isPinned := pinnedHashes[info.Hash]; isPinned {
			log.Debug().Str("hash", info.Hash).Msg("skipping pinned narinfo during eviction")

			continue
		}

		fileSize := narInfoFileSize(info)

		narInfoHashesToRemove = append(narInfoHashesToRemove, info.Hash)
		totalSize += fileSize

		if err := tx.NarInfo.DeleteOneID(info.ID).Exec(ctx); err != nil {
			log.Error().
				Err(err).
				Str("hash", info.Hash).
				Msg("error deleting narinfo record")

			return nil, nil, nil, err
		}

		// Stop if we've collected enough to meet cleanupSize
		// Note: cleanupSize = 0 means "delete all", so we don't break early in that case
		// Also, if totalSize >= cleanupSize AND this is the last narinfo in the list,
		// we should continue to ensure all are deleted (handles edge case where
		// cleanupSize equals total unique size)
		if cleanupSize > 0 && totalSize >= cleanupSize {
			// Only break if this is not the last narinfo we're processing
			// (i.e., there are more narinfos to process after this one)
			idx := len(narInfoHashesToRemove)
			if idx < len(narInfosToDelete)-1 {
				break
			}
		}
	}

	// Only warn if we actually needed to delete something (cleanupSize > 0)
	if cleanupSize > 0 && totalSize < cleanupSize {
		log.Warn().
			Uint64("collected", totalSize).
			Uint64("requested", cleanupSize).
			Msg("could not collect enough narinfos for cleanup, all may be pinned or database exhausted")
	}

	log.Info().
		Int("count", len(narInfoHashesToRemove)).
		Uint64("total_size", totalSize).
		Msg("narinfos to be deleted")

	// 2. STORAGE PHASE
	// Now that metadata is gone, some files might have zero references.
	// We find those truly orphaned files.
	orphanedNarFiles, err := tx.NarFile.Query().
		Where(entnarfile.Not(entnarfile.HasNarInfoNarFiles())).
		All(ctx)
	if err != nil {
		log.Error().Err(err).Msg("error identifying orphaned nar files")

		return nil, nil, nil, err
	}

	narURLsToRemove := make([]nar.URL, 0, len(orphanedNarFiles))

	if len(orphanedNarFiles) > 0 {
		log.Info().Int("count", len(orphanedNarFiles)).Msg("found orphaned nar files to delete")

		for _, nf := range orphanedNarFiles {
			// Add to list for physical storage deletion
			narURLsToRemove = append(narURLsToRemove, nar.URL{
				Hash:        nf.Hash,
				Compression: nar.CompressionTypeFromString(nf.Compression),
			})
		}

		// Batch delete all orphaned nar files in one query
		if _, err := tx.NarFile.Delete().
			Where(entnarfile.Not(entnarfile.HasNarInfoNarFiles())).
			Exec(ctx); err != nil {
			log.Error().
				Err(err).
				Msg("error deleting orphaned nar file records")

			return nil, nil, nil, err
		}
	} else {
		log.Info().Msg("no orphaned nar files found (files may be shared with active narinfos)")
	}

	// 3. CHUNK PHASE
	// Now that files are gone, some chunks might have zero references.
	if !c.isCDCEnabled() {
		return narInfoHashesToRemove, narURLsToRemove, nil, nil
	}

	orphanedChunks, err := tx.Chunk.Query().
		Where(entchunk.Not(entchunk.HasNarFileLinks())).
		All(ctx)
	if err != nil {
		log.Error().Err(err).Msg("error identifying orphaned chunks")

		return nil, nil, nil, err
	}

	if len(orphanedChunks) == 0 {
		log.Debug().Msg("no orphaned chunks found")

		return narInfoHashesToRemove, narURLsToRemove, nil, nil
	}

	log.Info().Int("count", len(orphanedChunks)).Msg("found orphaned chunks to delete")

	chunkHashesToRemove := make([]string, 0, len(orphanedChunks))
	for _, chk := range orphanedChunks {
		chunkHashesToRemove = append(chunkHashesToRemove, chk.Hash)
	}

	// Batch delete all orphaned chunks in one query
	if _, err := tx.Chunk.Delete().
		Where(entchunk.Not(entchunk.HasNarFileLinks())).
		Exec(ctx); err != nil {
		log.Error().
			Err(err).
			Msg("error deleting orphaned chunk records")

		return nil, nil, nil, err
	}

	return narInfoHashesToRemove, narURLsToRemove, chunkHashesToRemove, nil
}

// parallelDeleteFromStores deletes narinfos and nars from stores in parallel.
func (c *Cache) parallelDeleteFromStores(
	ctx context.Context,
	log zerolog.Logger,
	narInfoHashesToRemove []string,
	narURLsToRemove []nar.URL,
	chunkHashesToRemove []string,
) {
	var wg sync.WaitGroup

	for _, hash := range narInfoHashesToRemove {
		wg.Add(1)

		analytics.SafeGo(ctx, func() {
			defer wg.Done()

			log := log.With().Str("narinfo_hash", hash).Logger()

			log.Info().Msg("deleting narinfo from store")

			if err := c.narInfoStore.DeleteNarInfo(ctx, hash); err != nil {
				log.Error().
					Err(err).
					Msg("error removing the narinfo from the store")
			}
		})
	}

	for _, narURL := range narURLsToRemove {
		wg.Add(1)

		analytics.SafeGo(ctx, func() {
			defer wg.Done()

			log := log.With().Str("nar_url", narURL.String()).Logger()

			log.Info().Msg("deleting nar from store")

			if err := c.narStore.DeleteNar(ctx, narURL); err != nil {
				log.Error().
					Err(err).
					Msg("error removing the nar from the store")
			}
		})
	}

	for _, hash := range chunkHashesToRemove {
		wg.Add(1)

		analytics.SafeGo(ctx, func() {
			defer wg.Done()

			chunkStore := c.getChunkStore()
			if chunkStore == nil {
				return
			}

			log := log.With().Str("chunk_hash", hash).Logger()

			log.Info().Msg("deleting chunk from store")

			if err := chunkStore.DeleteChunk(ctx, hash); err != nil {
				log.Error().
					Err(err).
					Msg("error removing the chunk from the store")
			}
		})
	}

	wg.Wait()
}

func (c *Cache) runLRU(ctx context.Context) func() {
	return func() {
		// Track cleanup start time
		startTime := time.Now()

		lockKey := cacheLockKey

		// Try to acquire LRU lock (non-blocking)
		acquired, err := c.withTryLock(ctx, "runLRU", lockKey, func() error {
			// Increment run counter
			lruCleanupRunsTotal.Add(ctx, 1)

			log := zerolog.Ctx(ctx).With().
				Str("op", "lru").
				Uint64("max_size", c.maxSize).
				Logger()

			log.Info().Msg("running LRU")

			// Get pinned closure hashes BEFORE starting the transaction to avoid
			// deadlock issues with SQLite (concurrent reads while transaction is active)
			pinnedHashes, err := c.GetPinnedClosureHashes(ctx)
			if err != nil {
				log.Error().Err(err).Msg("error getting pinned closure hashes")

				return err
			}

			var (
				narInfoHashesToRemove []string
				narURLsToRemove       []nar.URL
				chunkHashesToRemove   []string
				cleanupSize           uint64
			)

			err = c.withEntTransaction(ctx, "runLRU", func(tx *ent.Tx) error {
				var txErr error

				cleanupSize, txErr = c.calculateCleanupSize(ctx, tx, log)
				if txErr != nil || cleanupSize == 0 {
					return txErr
				}

				narInfoHashesToRemove, narURLsToRemove, chunkHashesToRemove, txErr = c.deleteLRURecordsFromDB(
					ctx,
					tx,
					log,
					cleanupSize,
					pinnedHashes,
				)

				return txErr
			})
			if err != nil {
				return err
			}

			if len(narInfoHashesToRemove) == 0 &&
				len(narURLsToRemove) == 0 &&
				len(chunkHashesToRemove) == 0 {
				return nil
			}

			// Track eviction counts
			lruNarInfosEvictedTotal.Add(ctx, int64(len(narInfoHashesToRemove)))
			lruNarFilesEvictedTotal.Add(ctx, int64(len(narURLsToRemove)))
			lruChunksEvictedTotal.Add(ctx, int64(len(chunkHashesToRemove)))

			// Track bytes freed (approximate as cleanupSize)
			lruBytesFreedTotal.Add(ctx, int64(cleanupSize))

			// Remove all the files from the store as fast as possible
			c.parallelDeleteFromStores(ctx, log, narInfoHashesToRemove, narURLsToRemove, chunkHashesToRemove)

			return nil
		})

		// Record cleanup duration
		duration := time.Since(startTime).Seconds()
		lruCleanupDuration.Record(ctx, duration)

		if err != nil {
			return
		}

		if !acquired {
			// Another instance is running LRU, skip this run
			zerolog.Ctx(ctx).Info().
				Msg("another instance is running LRU, skipping")
		}
	}
}

// runCDCDeletedCleanup runs the CDC delayed cleanup job to delete old compressed NAR files
// after they have been replaced by chunked versions and the delay has passed.
func (c *Cache) runCDCDeletedCleanup(ctx context.Context) func() {
	return func() {
		startTime := time.Now()

		lockKey := "cdc-deleted-cleanup"

		// Try to acquire cleanup lock (non-blocking)
		acquired, err := c.withTryLock(ctx, "runCDCDeletedCleanup", lockKey, func() error {
			log := zerolog.Ctx(ctx).With().
				Str("op", "cdc-deleted-cleanup").
				Dur("delete_delay", c.GetCDCDeleteDelay()).
				Logger()

			log.Info().Msg("running CDC delayed cleanup")

			// Get old compressed NAR files that are ready for deletion
			cutoffTime := time.Now().Add(-c.GetCDCDeleteDelay())

			// Two-step Ent equivalent of the legacy self-join: first
			// gather the hashes that already have a chunked
			// (compression='none', total_chunks>0) representation, then
			// find the old compressed (total_chunks=0, compression!='none')
			// records for those hashes whose created_at predates cutoff.
			//
			// HashIn is batched (cdcCleanupHashBatchSize) to stay below
			// driver parameter limits (Postgres ≤ 65535, SQLite ≤ 32766
			// on recent builds, ≤ 999 on older). 500 is well below all
			// limits, well above typical chunked-NAR-hash counts.
			entClient := c.dbClient.Ent()

			var chunkedHashRows []struct {
				Hash string `sql:"hash"`
			}
			if err := entClient.NarFile.Query().
				Where(
					entnarfile.CompressionEQ("none"),
					entnarfile.TotalChunksGT(0),
				).
				Select(entnarfile.FieldHash).
				Scan(ctx, &chunkedHashRows); err != nil {
				log.Error().Err(err).Msg("error fetching chunked NAR hashes for cleanup")

				return err
			}

			if len(chunkedHashRows) == 0 {
				log.Debug().Msg("no chunked NAR hashes found; nothing to clean")

				return nil
			}

			chunkedHashes := make([]string, len(chunkedHashRows))
			for i, r := range chunkedHashRows {
				chunkedHashes[i] = r.Hash
			}

			var oldFiles []*ent.NarFile

			for start := 0; start < len(chunkedHashes); start += cdcCleanupHashBatchSize {
				end := start + cdcCleanupHashBatchSize
				if end > len(chunkedHashes) {
					end = len(chunkedHashes)
				}

				batch, err := entClient.NarFile.Query().
					Where(
						entnarfile.TotalChunksEQ(0),
						entnarfile.CompressionNEQ("none"),
						entnarfile.CreatedAtLT(cutoffTime),
						entnarfile.HashIn(chunkedHashes[start:end]...),
					).
					All(ctx)
				if err != nil {
					log.Error().Err(err).Msg("error getting old compressed NAR files for cleanup")

					return err
				}

				oldFiles = append(oldFiles, batch...)
			}

			if len(oldFiles) == 0 {
				log.Debug().Msg("no old compressed NAR files found for cleanup")

				return nil
			}

			log.Info().Int("count", len(oldFiles)).Msg("found old compressed NAR files for cleanup")

			// Delete each old compressed file
			for _, oldFile := range oldFiles {
				narURL := nar.URL{
					Hash:        oldFile.Hash,
					Compression: nar.CompressionType(oldFile.Compression),
				}

				// Delete from database first. If this fails, we'll retry on the next run.
				if _, err := entClient.NarFile.Delete().
					Where(entnarfile.IDEQ(oldFile.ID)).
					Exec(ctx); err != nil {
					log.Error().Err(err).
						Int("id", oldFile.ID).
						Msg("failed to delete old compressed NAR file record from database")

					continue
				}

				// Now, delete from storage. If this fails, we've orphaned a file,
				// but the DB record is gone, so we won't retry.
				if err := c.narStore.DeleteNar(ctx, narURL); err != nil {
					if !errors.Is(err, storage.ErrNotFound) {
						log.Error().Err(err).
							Str("hash", oldFile.Hash).
							Str("compression", oldFile.Compression).
							Msg("failed to delete old compressed NAR from storage")
					}
					// Continue even if file not found in storage
				}

				log.Debug().
					Str("hash", oldFile.Hash).
					Str("compression", oldFile.Compression).
					Msg("deleted old compressed NAR file")
			}

			log.Info().
				Int("count", len(oldFiles)).
				Dur("elapsed", time.Since(startTime)).
				Msg("CDC delayed cleanup completed")

			return nil
		})
		if err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Msg("error running CDC delayed cleanup")
		} else if !acquired {
			zerolog.Ctx(ctx).Debug().Msg("another instance is running CDC delayed cleanup, skipping")
		}
	}
}

// cdcRecoveryCursorKey is the config-table key under which the CDC lazy-recovery
// keyset cursor is persisted, so the scan resumes across restarts and lock handoffs
// instead of restarting at 0 (which would let a low-id backlog starve higher-id rows).
const cdcRecoveryCursorKey = "cdc_lazy_recovery_cursor"

// runCDCLazyRecovery runs the CDC lazy recovery job to recover stuck NAR files.
func (c *Cache) runCDCLazyRecovery(ctx context.Context, schedule cron.Schedule, batchSize int) func() {
	return func() {
		startTime := time.Now()

		lockKey := "cdc-lazy-recovery"

		// Try to acquire recovery lock (non-blocking)
		acquired, err := c.withTryLock(ctx, "runCDCLazyRecovery", lockKey, func() error {
			log := zerolog.Ctx(ctx).With().
				Str("op", "cdc-lazy-recovery").
				Int("batch_size", batchSize).
				Logger()

			log.Info().Msg("running CDC lazy recovery")

			// Calculate interval dynamically to ensure it's always correct.
			// This uses the actual interval between scheduled runs.
			nextRun := schedule.Next(startTime)
			nextNextRun := schedule.Next(nextRun)
			interval := nextNextRun.Sub(nextRun)

			// Get stuck NAR files - those that have total_chunks = 0,
			// chunking_started_at = NULL, and are older than the recovery interval
			cutoffTime := startTime.Add(-interval)

			// Ensure batch size is within int32 bounds to avoid overflow
			if batchSize > math.MaxInt32 {
				batchSize = math.MaxInt32
			}

			// Resume the keyset scan from the persisted cursor so progress survives
			// restarts and lock handoffs to other instances; a per-process cursor would
			// restart at 0 and let a low-id backlog (e.g. failed-download placeholders)
			// starve genuinely re-drivable higher-id rows. Backing-less rows are skipped
			// without mutation, so this cursor is the only thing advancing past them.
			cursorID := c.loadRecoveryCursor(ctx)

			stuckFiles, err := c.dbClient.Ent().NarFile.Query().
				Where(
					entnarfile.TotalChunksEQ(0),
					entnarfile.ChunkingStartedAtIsNil(),
					entnarfile.CreatedAtLT(cutoffTime),
					entnarfile.IDGT(cursorID),
				).
				Order(ent.Asc(entnarfile.FieldID)).
				Limit(batchSize).
				All(ctx)
			if err != nil {
				log.Error().Err(err).Msg("error getting stuck NAR files for recovery")

				return err
			}

			if len(stuckFiles) == 0 {
				// Reached the end of the id space (or there were never any rows past
				// the cursor); wrap back to the start so the next run rescans from the
				// beginning and picks up newly-stuck rows.
				cursorID = 0
				c.saveRecoveryCursor(ctx, cursorID)

				log.Debug().Msg("no stuck NAR files found for recovery")

				return nil
			}

			// Advance the keyset cursor past the batch we are about to examine so the
			// next run resumes after it instead of re-fetching the same low-id rows. A
			// short batch means we hit the end, so wrap back to the start next time.
			cursorID = stuckFiles[len(stuckFiles)-1].ID
			if len(stuckFiles) < batchSize {
				cursorID = 0
			}

			c.saveRecoveryCursor(ctx, cursorID)

			log.Info().Int("count", len(stuckFiles)).Msg("found stuck NAR files for recovery")

			// Trigger background chunking for each stuck NAR
			for _, stuckFile := range stuckFiles {
				// Parse the query string from the database
				parsedQuery, err := url.ParseQuery(stuckFile.Query)
				if err != nil {
					log.Error().Err(err).Str("query", stuckFile.Query).Msg("failed to parse query string for stuck NAR file")

					continue
				}

				narURL := nar.URL{
					Hash:        stuckFile.Hash,
					Compression: nar.CompressionType(stuckFile.Compression),
					Query:       parsedQuery,
				}

				// Only re-drive rows that have a whole-file in the store:
				// BackgroundMigrateNarToChunks chunks an existing whole-file NAR and
				// cannot help a backing-less row (placeholder created at narinfo-fetch
				// time, or a NAR upstream genuinely does not have). Re-driving those
				// every interval just spams "error fetching nar from store: not found"
				// and indefinitely retries a hash that can never be migrated. Such rows
				// are recovered on demand by GetNar, which re-downloads from upstream.
				if !c.HasNarInStore(ctx, narURL) {
					c.gcOrSkipBackingLessNarFile(ctx, stuckFile.ID, narURL, &log)

					continue
				}

				// Trigger background migration - this uses distributed locking internally
				// to prevent duplicate processing across instances
				c.BackgroundMigrateNarToChunks(ctx, narURL)

				log.Debug().
					Str("hash", stuckFile.Hash).
					Str("compression", stuckFile.Compression).
					Msg("triggered background chunking for stuck NAR file")
			}

			log.Info().
				Int("count", len(stuckFiles)).
				Dur("elapsed", time.Since(startTime)).
				Msg("CDC lazy recovery completed")

			return nil
		})
		if err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Msg("error running CDC lazy recovery")
		} else if !acquired {
			zerolog.Ctx(ctx).Debug().Msg("another instance is running CDC lazy recovery, skipping")
		}
	}
}

// loadRecoveryCursor reads the persisted CDC lazy-recovery keyset cursor, returning 0
// (start of scan) when it is unset or unreadable.
func (c *Cache) loadRecoveryCursor(ctx context.Context) int {
	e, err := c.dbClient.Ent().ConfigEntry.Query().
		Where(entconfigentry.KeyEQ(cdcRecoveryCursorKey)).
		Only(ctx)
	if err != nil {
		// A missing entry is expected (first run / after a wrap to 0). Any other
		// error (transient DB issue, lock) resets the scan to 0, which can revive the
		// low-id starvation this cursor prevents, so surface it for diagnosis.
		if !database.IsNotFoundError(err) {
			zerolog.Ctx(ctx).Warn().Err(err).Msg("failed to load CDC lazy-recovery cursor; resuming from 0")
		}

		return 0
	}

	id, err := strconv.Atoi(e.Value)
	if err != nil || id < 0 {
		return 0
	}

	return id
}

// saveRecoveryCursor persists the CDC lazy-recovery keyset cursor. The
// cdc-lazy-recovery distributed lock serializes sweeps, so a plain upsert suffices. A
// failure is non-fatal: the next sweep simply resumes from the last persisted value.
func (c *Cache) saveRecoveryCursor(ctx context.Context, cursorID int) {
	value := strconv.Itoa(cursorID)

	if err := c.dbClient.Ent().ConfigEntry.Create().
		SetKey(cdcRecoveryCursorKey).
		SetValue(value).
		OnConflictColumns(entconfigentry.FieldKey).
		Update(func(u *ent.ConfigEntryUpsert) {
			u.SetValue(value)
			u.SetUpdatedAt(time.Now())
		}).
		Exec(ctx); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("failed to persist CDC lazy-recovery cursor")
	}
}

// gcOrSkipBackingLessNarFile handles a stuck nar_file row that has no whole-file in
// the store (a placeholder that migration cannot help). If the NAR is provably gone
// from every healthy upstream, the row is garbage-collected so it stops being
// re-scanned every sweep; on-delete cascades clean up its narinfo_nar_files and
// nar_file_chunks links. Otherwise the row is left for on-demand GetNar recovery.
func (c *Cache) gcOrSkipBackingLessNarFile(ctx context.Context, narFileID int, narURL nar.URL, log *zerolog.Logger) {
	// Resolve every narinfo linked to this nar_file via the narinfo_nar_files relation
	// (the source of truth for linkage; URL equality is narrower and can miss links).
	// Several store paths can reference the same NAR, so every linked narinfo must be
	// genuinely gone upstream before we delete the row — otherwise an active store path
	// would lose its NAR.
	nis, err := c.dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.HasNarInfoNarFilesWith(entnarinfonarfile.NarFileID(narFileID))).
		All(ctx)
	if err != nil {
		log.Debug().
			Err(err).
			Str("hash", narURL.Hash).
			Msg("skipping backing-less stuck NAR file (failed to query linked narinfos)")

		return
	}

	// An orphan with no linked narinfo can never be resolved or served, so it is dead
	// weight that would be re-scanned forever; garbage-collect it outright. A later
	// request re-creates it on demand.
	if len(nis) == 0 {
		if err := c.dbClient.Ent().NarFile.DeleteOneID(narFileID).Exec(ctx); err != nil {
			log.Warn().
				Err(err).
				Str("hash", narURL.Hash).
				Msg("failed to garbage-collect orphaned placeholder nar_file")

			return
		}

		log.Info().
			Str("hash", narURL.Hash).
			Msg("garbage-collected orphaned placeholder nar_file (no linked narinfo)")

		return
	}

	for _, ni := range nis {
		if !c.narInfoGenuinelyAbsentUpstream(ctx, ni.Hash) {
			log.Debug().
				Str("hash", narURL.Hash).
				Msg("skipping backing-less stuck NAR file (still present or unverifiable upstream)")

			return
		}
	}

	if err := c.dbClient.Ent().NarFile.DeleteOneID(narFileID).Exec(ctx); err != nil {
		log.Warn().
			Err(err).
			Str("hash", narURL.Hash).
			Msg("failed to garbage-collect genuinely-absent placeholder nar_file")

		return
	}

	log.Info().
		Str("hash", narURL.Hash).
		Int("narinfo_count", len(nis)).
		Msg("garbage-collected genuinely-absent placeholder nar_file")
}

// narInfoGenuinelyAbsentUpstream reports whether EVERY healthy upstream definitively
// lacks the narinfo for hash. It is deliberately conservative: a single Present or
// inconclusive (Unknown) probe, or having no healthy upstreams, yields false, so a
// transient outage never causes a placeholder row to be deleted for a NAR an upstream
// can still provide.
func (c *Cache) narInfoGenuinelyAbsentUpstream(ctx context.Context, hash string) bool {
	ups := c.getHealthyUpstreams()
	if len(ups) == 0 {
		return false
	}

	sawAbsent := false

	for _, uc := range ups {
		switch uc.NarInfoExistence(ctx, hash) {
		case upstream.ExistencePresent, upstream.ExistenceUnknown:
			return false
		case upstream.ExistenceAbsent:
			sawAbsent = true
		}
	}

	return sawAbsent
}

type upstreamSelectionFn func(
	ctx context.Context,
	uc *upstream.Cache,
	wg *sync.WaitGroup,
	ch chan *upstream.Cache,
	errC chan error,
)

func (c *Cache) getHealthyUpstreams() []*upstream.Cache {
	c.upstreamCachesMu.RLock()
	defer c.upstreamCachesMu.RUnlock()

	healthyUpstreams := make([]*upstream.Cache, 0, len(c.upstreamCaches))

	for _, u := range c.upstreamCaches {
		if u.IsHealthy() {
			healthyUpstreams = append(healthyUpstreams, u)
		}
	}

	slices.SortFunc(healthyUpstreams, func(a, b *upstream.Cache) int {
		//nolint:gosec
		return int(a.GetPriority() - b.GetPriority())
	})

	return healthyUpstreams
}

func (c *Cache) selectNarInfoUpstream(
	ctx context.Context,
	hash string,
) (*upstream.Cache, error) {
	return c.selectUpstream(ctx, c.getHealthyUpstreams(), func(
		ctx context.Context,
		uc *upstream.Cache,
		wg *sync.WaitGroup,
		ch chan *upstream.Cache,
		errC chan error,
	) {
		defer wg.Done()

		exists, err := uc.HasNarInfo(ctx, hash)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				errC <- err
			}

			return
		}

		if exists {
			ch <- uc
		}
	})
}

func (c *Cache) selectNarUpstream(
	ctx context.Context,
	narURL *nar.URL,
	ucs []*upstream.Cache,
) (*upstream.Cache, error) {
	return c.selectUpstream(ctx, ucs, func(
		ctx context.Context,
		uc *upstream.Cache,
		wg *sync.WaitGroup,
		ch chan *upstream.Cache,
		errC chan error,
	) {
		defer wg.Done()

		exists, err := uc.HasNar(ctx, *narURL)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				errC <- err
			}

			return
		}

		if exists {
			ch <- uc
		}
	})
}

func (c *Cache) selectUpstream(
	ctx context.Context,
	ucs []*upstream.Cache,
	selectFn upstreamSelectionFn,
) (*upstream.Cache, error) {
	if len(ucs) == 0 {
		//nolint:nilnil
		return nil, nil
	}

	if len(ucs) == 1 {
		return ucs[0], nil
	}

	ch := make(chan *upstream.Cache, len(ucs))
	errC := make(chan error, len(ucs))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	for _, uc := range ucs {
		wg.Add(1)

		analytics.SafeGo(ctx, func() {
			selectFn(ctx, uc, &wg, ch, errC)
		})
	}

	analytics.SafeGo(ctx, func() {
		wg.Wait()

		close(ch)
	})

	var errs error

	for {
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), errs)
		case uc, ok := <-ch:
			if !ok {
				return nil, errs
			}

			return uc, errs
		case err := <-errC:
			if !errors.Is(err, context.Canceled) {
				errs = errors.Join(errs, err)
			}
		}
	}
}

// processHealthChanges handles health status changes for upstreams.
func (c *Cache) processHealthChanges(ctx context.Context, healthChangeCh <-chan healthcheck.HealthStatusChange) {
	for {
		select {
		case <-ctx.Done():
			return
		case change := <-healthChangeCh:
			if change.IsHealthy {
				zerolog.Ctx(ctx).
					Info().
					Str("upstream", change.Upstream.GetHostname()).
					Msg("upstream became healthy and is now available for requests")
			} else {
				zerolog.Ctx(ctx).
					Warn().
					Str("upstream", change.Upstream.GetHostname()).
					Msg("upstream became unhealthy and is no longer available for requests")
			}
		}
	}
}

// parseValidHashPtr parses an Ent-shape nullable hash field.
// Nullable string fields come back as *string from Ent; nil or
// empty string means "no hash".
func parseValidHashPtr(hash *string, fieldName string) (*nixhash.HashWithEncoding, error) {
	if hash == nil || *hash == "" {
		//nolint:nilnil
		return nil, nil
	}

	h, err := nixhash.ParseAny(*hash, nil)
	if err != nil {
		return nil, fmt.Errorf("error parsing %s: %w", fieldName, err)
	}

	return h, nil
}

// derefStringPtr returns "" if p is nil, *p otherwise. Ent's
// nullable string fields come back as *string while the legacy
// narinfo struct uses plain strings; this bridges the two.
func derefStringPtr(p *string) string {
	if p == nil {
		return ""
	}

	return *p
}

// derefInt64Ptr returns 0 if p is nil, *p otherwise. Mirrors
// derefStringPtr for int64-valued nullable Ent fields.
func derefInt64Ptr(p *int64) int64 {
	if p == nil {
		return 0
	}

	return *p
}

// maybeCDCNormalizeNarInfoURL normalizes the narinfo URL and Compression in-memory
// when CDC is enabled and the NAR has already been migrated to chunks. Without this,
// there is a race window where GetNarInfo returns "Compression: xz" but GetNar serves
// uncompressed data from chunks — causing Nix to fail with "input compression not
// recognized". The DB update happens asynchronously via migrateNarToChunksCleanup;
// this call makes the in-flight response correct immediately.
func (c *Cache) maybeCDCNormalizeNarInfoURL(ctx context.Context, narURL nar.URL, narInfo *narinfo.NarInfo) {
	if !c.isCDCEnabled() {
		return
	}

	// Normalize the URL before querying so nix-serve-style prefixed hashes
	// (e.g. "abc-hash") match the normalized hashes stored in nar_file rows.
	normalizedURL, err := narURL.Normalize()
	if err != nil {
		return
	}

	hasChunks, err := c.HasNarInChunks(ctx, normalizedURL)
	if err != nil || !hasChunks {
		return
	}

	noneURL := nar.URL{Hash: normalizedURL.Hash, Compression: nar.CompressionTypeNone, Query: normalizedURL.Query}
	narInfo.URL = noneURL.String()
	narInfo.Compression = nar.CompressionTypeNone.String()
	narInfo.FileHash = nil
	narInfo.FileSize = 0
}

// HasNarInChunks returns true if the NAR is already in chunks and chunking is complete.
func (c *Cache) HasNarInChunks(ctx context.Context, narURL nar.URL) (bool, error) {
	if !c.isCDCEnabled() {
		return false, nil
	}

	nr, err := c.getNarFileFromDB(ctx, c.dbClient.Ent().NarFile, narURL)
	if err != nil {
		if database.IsNotFoundError(err) {
			return false, nil
		}

		return false, err
	}

	return nr.TotalChunks > 0, nil
}

// HasNarFileRecord checks if a NAR file record exists in the database,
// regardless of chunking completion status. This is used for coordination
// to allow progressive streaming while chunking is in progress.
func (c *Cache) HasNarFileRecord(ctx context.Context, narURL nar.URL) (bool, error) {
	_, err := c.getNarFileFromDB(ctx, c.dbClient.Ent().NarFile, narURL)
	if err != nil {
		if database.IsNotFoundError(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func (c *Cache) getNarFromChunks(ctx context.Context, narURL *nar.URL) (int64, io.ReadCloser, error) {
	// Guard: if CDC is not configured, we cannot stream from chunks even if DB records exist.
	// This can happen if CDC was previously enabled and then disabled (config change or rollback).
	if !c.isCDCEnabled() {
		return 0, nil, fmt.Errorf("CDC is not enabled, cannot serve NAR from chunks: %w", storage.ErrNotFound)
	}

	ctx, span := tracer.Start(
		ctx,
		"cache.getNarFromChunks",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
		),
	)
	defer span.End()

	// Query initial state
	var (
		narFileID   int64
		totalSize   int64
		totalChunks int64
	)

	err := c.withEntTransaction(ctx, "getNarFromChunks.init", func(tx *ent.Tx) error {
		nr, err := c.getNarFileFromDB(ctx, tx.NarFile, *narURL)
		if err != nil {
			return err
		}

		narFileID = int64(nr.ID)
		//nolint:gosec // G115: File size is non-negative
		totalSize = int64(nr.FileSize)
		totalChunks = nr.TotalChunks

		// Touch the NAR file by the fetched row's ID. getNarFileFromDB may
		// return a compression=none fallback row whose key differs from
		// narURL, so filtering on narURL fields can silently miss it.
		if nr.LastAccessedAt == nil || time.Since(*nr.LastAccessedAt) > c.recordAgeIgnoreTouch {
			now := time.Now()
			if _, err := tx.NarFile.UpdateOneID(nr.ID).
				SetLastAccessedAt(now).
				SetUpdatedAt(now).
				Save(ctx); err != nil {
				return fmt.Errorf("error touching the nar record: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return 0, nil, err
	}

	// Always clear TransparentZstd here so the goroutine uses GetChunk (decompressed
	// bytes) and the HTTP layer re-encodes everything into a single zstd stream when
	// the client accepts it.
	narURL.TransparentZstd = false

	// Update narURL.Compression to match what we are actually serving (None).
	narURL.Compression = nar.CompressionTypeNone

	pr, pw := io.Pipe()

	analytics.SafeGo(ctx, func() {
		defer pw.Close()

		var streamErr error

		if totalChunks > 0 {
			// Fast path: All chunks complete
			streamErr = c.streamCompleteChunks(ctx, pw, narFileID, totalChunks, false)
		} else {
			// Progressive path: Stream as chunks appear
			streamErr = c.streamProgressiveChunks(ctx, pw, narFileID, false)
		}

		if streamErr != nil {
			pw.CloseWithError(streamErr)
		}
	})

	return totalSize, pr, nil
}

// streamCompleteChunks streams all chunks for a NAR that has completed chunking.
// This is the fast path where all chunks are available immediately.
// It uses a prefetch pipeline to overlap chunk fetching with data copying for better performance.
func (c *Cache) streamCompleteChunks(
	ctx context.Context,
	w io.Writer,
	narFileID int64,
	totalChunks int64,
	raw bool,
) error {
	// Get all chunks at once, ordered by their position in the NAR.
	// Query the junction entity directly (rather than chunk + edge
	// HasNarFileLinks with edge-ordering, which Ent compiles to a
	// Postgres-incompatible `ORDER BY <join_table>.chunk_index` after
	// the implicit GROUP BY chunk.id). Eager-load Chunk on each link.
	chunkHashes := make([]string, 0, totalChunks)

	links, err := c.dbClient.Ent().NarFileChunk.Query().
		Where(entnarfilechunk.NarFileID(int(narFileID))).
		Order(entnarfilechunk.ByChunkIndex()).
		WithChunk().
		All(ctx)
	if err != nil {
		return fmt.Errorf("error getting chunks: %w", err)
	}

	for _, link := range links {
		if link.Edges.Chunk == nil {
			return fmt.Errorf("nar_file_chunk %d: %w", link.ID, errMissingChunkEdge)
		}

		chunkHashes = append(chunkHashes, link.Edges.Chunk.Hash)
	}

	if len(chunkHashes) != int(totalChunks) {
		return fmt.Errorf("expected %d chunks but got %d: %w", totalChunks, len(chunkHashes), storage.ErrNotFound)
	}

	// Use prefetch pipeline to overlap I/O operations
	return c.streamChunksWithPrefetch(ctx, w, chunkHashes, raw)
}

// prefetchedChunk holds a chunk reader and any error from fetching it.
type prefetchedChunk struct {
	reader io.ReadCloser
	hash   string
	err    error
}

// streamChunksWithPrefetch implements a prefetch pipeline that fetches the next chunk
// while the current chunk is being copied to the writer. This overlaps network/disk I/O
// with data copying, significantly improving throughput for remote storage.
func (c *Cache) streamChunksWithPrefetch(ctx context.Context, w io.Writer, chunkHashes []string, raw bool) error {
	if len(chunkHashes) == 0 {
		return nil
	}

	chunkChan := make(chan *prefetchedChunk, prefetchBufferSize)

	// Start prefetch goroutine
	analytics.SafeGo(ctx, func() {
		defer close(chunkChan)

		for _, hash := range chunkHashes {
			// Check if context is cancelled before fetching
			select {
			case <-ctx.Done():
				// Send context error and stop prefetching
				select {
				case chunkChan <- &prefetchedChunk{err: ctx.Err(), hash: hash}:
				case <-ctx.Done():
				}

				return
			default:
			}

			// Fetch chunk
			var (
				rc  io.ReadCloser
				err error
			)

			if raw {
				rc, err = c.getChunkStore().GetRawChunk(ctx, hash)
			} else {
				rc, err = c.getChunkStore().GetChunk(ctx, hash)
			}

			// Send chunk or error to consumer
			select {
			case chunkChan <- &prefetchedChunk{reader: rc, hash: hash, err: err}:
			case <-ctx.Done():
				// Context cancelled while sending, close the reader if we got one
				if rc != nil {
					rc.Close()
				}

				return
			}
		}
	})

	// Stream chunks as they arrive from the prefetch pipeline
	for chunk := range chunkChan {
		if chunk.err != nil {
			return fmt.Errorf("error fetching chunk %s: %w", chunk.hash, chunk.err)
		}

		// Copy chunk data to writer
		if _, err := io.Copy(w, chunk.reader); err != nil {
			chunk.reader.Close()

			return fmt.Errorf("error copying chunk %s: %w", chunk.hash, err)
		}

		chunk.reader.Close()
	}

	return nil
}

// defaultChunkWaitTimeout is the default bound on how long progressive CDC
// streaming waits for the next chunk before treating the transfer as failed.
// It is intentionally below common reverse-proxy gateway timeouts so a stalled
// chunk surfaces as a retryable error rather than a gateway 504.
const defaultChunkWaitTimeout = 30 * time.Second

// SetChunkWaitTimeout overrides the per-chunk wait bound used by progressive CDC
// streaming. A non-positive value resets it to defaultChunkWaitTimeout. Operators
// on high-latency storage can raise it; those behind a short gateway timeout can
// lower it to fail fast and let the client retry.
func (c *Cache) SetChunkWaitTimeout(d time.Duration) {
	if d <= 0 {
		d = defaultChunkWaitTimeout
	}

	// Guard with cdcMu (the same mutex protecting the other CDC config) so a
	// concurrent reader in streamProgressiveChunks never sees a torn write.
	c.cdcMu.Lock()
	c.chunkWaitTimeout = d
	c.cdcMu.Unlock()
}

// streamProgressiveChunks streams chunks as they become available during an in-progress chunking operation.
// This allows concurrent downloads while another instance is still chunking the NAR.
// It uses a batch query to fetch multiple available chunks at once, eliminating per-chunk
// poll latency when chunks are already created. Only polls (200ms) when no new chunks are available yet.
func (c *Cache) streamProgressiveChunks(ctx context.Context, w io.Writer, narFileID int64, raw bool) error {
	pollInterval := 200 * time.Millisecond

	c.cdcMu.RLock()
	maxWaitPerChunk := c.chunkWaitTimeout
	c.cdcMu.RUnlock()

	if maxWaitPerChunk <= 0 {
		maxWaitPerChunk = defaultChunkWaitTimeout
	}

	chunkChan := make(chan *prefetchedChunk, prefetchBufferSize)

	// Start prefetch goroutine that batch-queries available chunks and fetches them.
	analytics.SafeGo(ctx, func() {
		defer close(chunkChan)

		chunkIndex := int64(0)

		var totalChunks int64

		for {
			// Check if context is cancelled before polling
			select {
			case <-ctx.Done():
				chunkChan <- &prefetchedChunk{err: ctx.Err()}

				return
			default:
			}

			// Batch-query chunks from current index to avoid per-chunk poll overhead.
			// When multiple chunks are already written by the CDC goroutine, this returns
			// all of them at once, eliminating the 200ms wait between each chunk.
			// Query the junction entity (with eager-loaded Chunk) so Postgres
			// can use the column ORDER BY directly — chunk-side edge-ordering
			// trips Postgres's GROUP BY rule, see getNarChunkedFromStore.
			batch, err := c.dbClient.Ent().NarFileChunk.Query().
				Where(

					entnarfilechunk.NarFileID(int(narFileID)),

					entnarfilechunk.ChunkIndexGTE(int(chunkIndex)),
				).
				Order(entnarfilechunk.ByChunkIndex()).
				WithChunk().
				Limit(progressivePollBatchSize).
				All(ctx)
			if err != nil && !database.IsNotFoundError(err) {
				chunkChan <- &prefetchedChunk{err: fmt.Errorf("error querying chunks from index %d: %w", chunkIndex, err)}

				return
			}

			if len(batch) > 0 { //nolint:nestif // pipeline loop, pre-existing depth
				// Feed all available chunks from the batch into the pipeline.
				for _, link := range batch {
					if link.Edges.Chunk == nil {
						select {
						case chunkChan <- &prefetchedChunk{
							err: fmt.Errorf("nar_file_chunk %d: %w", link.ID, errMissingChunkEdge),
						}:
						case <-ctx.Done():
						}

						return
					}

					ch := link.Edges.Chunk

					var (
						rc       io.ReadCloser
						fetchErr error
					)

					if raw {
						rc, fetchErr = c.getChunkStore().GetRawChunk(ctx, ch.Hash)
					} else {
						rc, fetchErr = c.getChunkStore().GetChunk(ctx, ch.Hash)
					}

					select {
					case chunkChan <- &prefetchedChunk{reader: rc, hash: ch.Hash, err: fetchErr}:
					case <-ctx.Done():
						if rc != nil {
							rc.Close()
						}

						return
					}

					chunkIndex++
				}

				// Check if we've streamed all chunks (only meaningful once total is known).
				if totalChunks > 0 && chunkIndex >= totalChunks {
					return
				}

				// More chunks may be available; loop immediately without sleeping.
				continue
			}

			// No chunks at current index yet — the CDC goroutine hasn't written them.
			// Check whether chunking is complete or still in progress before waiting.
			if totalChunks == 0 {
				nr, queryErr := c.dbClient.Ent().NarFile.Get(ctx, int(narFileID))
				if queryErr != nil {
					chunkChan <- &prefetchedChunk{err: fmt.Errorf("error querying nar file: %w", queryErr)}

					return
				}

				totalChunks = nr.TotalChunks

				// If total_chunks is still 0 but chunking_started_at is NULL, chunking was aborted.
				if totalChunks == 0 && nr.ChunkingStartedAt == nil {
					chunkChan <- &prefetchedChunk{err: fmt.Errorf("chunking was aborted: %w", storage.ErrNotFound)}

					return
				}

				// A non-NULL but stale chunking_started_at means the producing job died
				// without clearing the lock. Fail fast rather than waiting the full
				// per-chunk timeout for chunks that will never arrive.
				if totalChunks == 0 && nr.ChunkingStartedAt != nil &&
					time.Since(*nr.ChunkingStartedAt) > cdcChunkingLockTTL {
					chunkChan <- &prefetchedChunk{err: fmt.Errorf("chunking stalled (stale lock): %w", storage.ErrNotFound)}

					return
				}
			}

			// Check if we're already done (batch returned empty after total is known).
			if totalChunks > 0 && chunkIndex >= totalChunks {
				return
			}

			// Check wait timeout before sleeping.
			// chunkWaitStart is reset each time we enter the wait phase for a new index.
			chunkWaitStart := time.Now()

			for {
				select {
				case <-time.After(pollInterval):
				case <-ctx.Done():
					chunkChan <- &prefetchedChunk{err: ctx.Err()}

					return
				}

				// Re-check: try a batch query again after the sleep. Same
				// junction-side query as the initial poll to keep Postgres happy.
				batch2, batchErr := c.dbClient.Ent().NarFileChunk.Query().
					Where(

						entnarfilechunk.NarFileID(int(narFileID)),

						entnarfilechunk.ChunkIndexGTE(int(chunkIndex)),
					).
					Order(entnarfilechunk.ByChunkIndex()).
					WithChunk().
					Limit(progressivePollBatchSize).
					All(ctx)
				if batchErr != nil && !database.IsNotFoundError(batchErr) {
					chunkChan <- &prefetchedChunk{err: fmt.Errorf("error querying chunks from index %d: %w", chunkIndex, batchErr)}

					return
				}

				if len(batch2) > 0 { //nolint:nestif // pipeline loop, pre-existing depth
					// Feed the newly-available chunks and break out of the wait loop.
					for _, link := range batch2 {
						if link.Edges.Chunk == nil {
							select {
							case chunkChan <- &prefetchedChunk{
								err: fmt.Errorf("nar_file_chunk %d: %w", link.ID, errMissingChunkEdge),
							}:
							case <-ctx.Done():
							}

							return
						}

						ch := link.Edges.Chunk

						var (
							rc       io.ReadCloser
							fetchErr error
						)

						if raw {
							rc, fetchErr = c.getChunkStore().GetRawChunk(ctx, ch.Hash)
						} else {
							rc, fetchErr = c.getChunkStore().GetChunk(ctx, ch.Hash)
						}

						select {
						case chunkChan <- &prefetchedChunk{reader: rc, hash: ch.Hash, err: fetchErr}:
						case <-ctx.Done():
							if rc != nil {
								rc.Close()
							}

							return
						}

						chunkIndex++
					}

					if totalChunks > 0 && chunkIndex >= totalChunks {
						return
					}

					break // Break inner wait loop; outer loop will batch-query again.
				}

				// Re-check NarFile record for abort or completion since the last
				// empty batch; the CDC goroutine may have updated these fields.

				nr2, narFileErr := c.dbClient.Ent().NarFile.Get(ctx, int(narFileID))
				if narFileErr != nil {
					chunkChan <- &prefetchedChunk{err: fmt.Errorf("error querying nar file: %w", narFileErr)}

					return
				}

				if nr2.TotalChunks > 0 {
					totalChunks = nr2.TotalChunks

					if chunkIndex >= totalChunks {
						return
					}

					break // More chunks now recorded; loop to batch-query them.
				}

				if nr2.ChunkingStartedAt == nil {
					chunkChan <- &prefetchedChunk{err: fmt.Errorf("chunking was aborted: %w", storage.ErrNotFound)}

					return
				}

				// Lock went stale while we were waiting: the producer died without
				// clearing it. Fail fast instead of waiting out maxWaitPerChunk.
				if time.Since(*nr2.ChunkingStartedAt) > cdcChunkingLockTTL {
					chunkChan <- &prefetchedChunk{err: fmt.Errorf("chunking stalled (stale lock): %w", storage.ErrNotFound)}

					return
				}

				if time.Since(chunkWaitStart) > maxWaitPerChunk {
					chunkChan <- &prefetchedChunk{
						err: fmt.Errorf("timeout waiting for chunk %d after %v: %w",
							chunkIndex, maxWaitPerChunk, context.DeadlineExceeded),
					}

					return
				}
			}
		}
	})

	// Stream chunks as they arrive from the prefetch pipeline.
	for chunk := range chunkChan {
		if chunk.err != nil {
			return chunk.err
		}

		if _, err := io.Copy(w, chunk.reader); err != nil {
			chunk.reader.Close()

			return fmt.Errorf("error copying chunk %s: %w", chunk.hash, err)
		}

		chunk.reader.Close()
	}

	return nil
}

// MigrateChunksToNar is the reverse of MigrateNarToChunks: it reconstructs a
// CDC-chunked NAR into a whole file so a deployment can exit CDC. It reconstructs
// the NAR from its chunks, verifies the assembled bytes against the linked
// narinfo's recorded NarHash (and size), writes the whole file to the NAR store
// via narStore.PutNar, then flips the nar_file record to the whole-file
// representation (total_chunks=0, chunk links removed). The write-then-flip
// ordering keeps an interrupted run recoverable.
//
// Orphaned chunks (no longer referenced by any nar_file) are NOT deleted by
// default: an in-flight chunk-serve that started before the flip may still be
// reading chunk files, and deleting them mid-stream would truncate that transfer.
// They are left for the regular GC to reclaim. When forceReclaim is true the
// caller asserts traffic is drained (e.g. a maintenance-window run), and orphaned
// chunks are reclaimed immediately and dedup-safely (chunks still referenced by
// another nar_file are retained).
//
// It returns ErrNarAlreadyWholeFile when there is nothing chunked to migrate,
// ErrNoNarHashToVerify when the linked narinfo lacks a NarHash (the NAR is left
// chunked rather than de-chunked unverified), and ErrNarHashMismatch when the
// reconstructed bytes do not match the recorded hash/size.
func (c *Cache) MigrateChunksToNar(ctx context.Context, narURL *nar.URL, forceReclaim bool) error {
	if !c.isCDCEnabled() {
		return ErrCDCDisabled
	}

	// Coordinate with running instances and the forward migration using a
	// non-blocking lock keyed on the hash.
	lockKey := "migration-to-nar:" + narURL.Hash

	acquired, err := c.downloadLocker.TryLock(ctx, lockKey, c.downloadLockTTL)
	if err != nil {
		return fmt.Errorf("failed to acquire migration lock: %w", err)
	}

	if !acquired {
		zerolog.Ctx(ctx).Debug().
			Str("nar_hash", narURL.Hash).
			Msg("migration to nar already in progress by another instance")

		return ErrMigrationInProgress
	}

	defer func() {
		if err := c.downloadLocker.Unlock(context.WithoutCancel(ctx), lockKey); err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Str("nar_hash", narURL.Hash).Msg("failed to release migration lock")
		}
	}()

	// Chunks are always stored against the Compression:none URL.
	noneURL := nar.URL{Hash: narURL.Hash, Compression: nar.CompressionTypeNone, Query: narURL.Query}

	nr, err := c.getNarFileFromDB(ctx, c.dbClient.Ent().NarFile, noneURL)
	if err != nil {
		return fmt.Errorf("error looking up nar_file record: %w", err)
	}

	if nr.TotalChunks == 0 {
		// Nothing chunked to migrate back.
		return ErrNarAlreadyWholeFile
	}

	// Resolve the expected NAR hash from the linked narinfo. Per policy, refuse to
	// de-chunk (and later delete chunks for) a NAR we cannot content-verify.
	expected, err := c.linkedNarinfoNarHash(ctx, nr.ID)
	if err != nil {
		return err
	}

	if expected == nil {
		zerolog.Ctx(ctx).Warn().
			Str("nar_hash", narURL.Hash).
			Msg("no narinfo NarHash to verify against; skipping de-chunk migration")

		return ErrNoNarHashToVerify
	}

	// Reconstruct the whole NAR from chunks into a temp file while hashing, so we
	// can verify before committing anything to the store (verified-or-nothing).
	_, rc, err := c.getNarFromChunks(ctx, &noneURL)
	if err != nil {
		return fmt.Errorf("error reconstructing nar from chunks: %w", err)
	}
	defer rc.Close()

	f, err := os.CreateTemp(c.tempDir, "ncps-dechunk-*.nar")
	if err != nil {
		return fmt.Errorf("error creating temp file: %w", err)
	}

	tempPath := f.Name()
	defer os.Remove(tempPath)

	hasher := sha256.New()

	size, err := io.Copy(f, io.TeeReader(rc, hasher))
	if err != nil {
		_ = f.Close()

		return fmt.Errorf("error reconstructing nar to temp file: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("error closing temp file: %w", err)
	}

	if !bytes.Equal(hasher.Sum(nil), expected.Digest()) {
		return fmt.Errorf("hash mismatch for %s: %w", narURL.Hash, ErrNarHashMismatch)
	}

	//nolint:gosec // file_size is a non-negative byte count
	if size != int64(nr.FileSize) {
		return fmt.Errorf("size mismatch for %s (got %d, want %d): %w",
			narURL.Hash, size, nr.FileSize, ErrNarHashMismatch)
	}

	// Write the whole file straight to the NAR store. We deliberately bypass
	// c.PutNar here because, with CDC enabled, it would re-chunk the input.
	rf, err := os.Open(tempPath)
	if err != nil {
		return fmt.Errorf("error reopening reconstructed nar: %w", err)
	}
	defer rf.Close()

	if _, err := c.narStore.PutNar(ctx, noneURL, rf, size); err != nil {
		return fmt.Errorf("error storing reconstructed whole nar: %w", err)
	}

	// When reclaiming immediately, capture the chunks this nar_file references
	// before dropping the links so we can delete the ones that become orphaned.
	var prevChunks []*ent.Chunk

	if forceReclaim {
		prevChunks, err = c.dbClient.Ent().Chunk.Query().
			Where(entchunk.HasNarFileLinksWith(entnarfilechunk.NarFileID(nr.ID))).
			All(ctx)
		if err != nil {
			return fmt.Errorf("error listing chunks for nar_file %d: %w", nr.ID, err)
		}
	}

	// Flip the nar_file to the whole-file representation: drop its chunk links and
	// zero total_chunks in one transaction. After this the NAR is served from the
	// whole file written above. This happens after the durable PutNar so an
	// interrupted run is recoverable by re-running.
	if err := c.withEntTransaction(ctx, "MigrateChunksToNar.flip", func(tx *ent.Tx) error {
		if _, err := tx.NarFileChunk.Delete().
			Where(entnarfilechunk.NarFileID(nr.ID)).
			Exec(ctx); err != nil {
			return fmt.Errorf("error deleting chunk links: %w", err)
		}

		if _, err := tx.NarFile.UpdateOneID(nr.ID).
			SetTotalChunks(0).
			ClearChunkingStartedAt().
			SetUpdatedAt(time.Now()).
			Save(ctx); err != nil {
			return fmt.Errorf("error flipping nar_file to whole-file: %w", err)
		}

		return nil
	}); err != nil {
		return err
	}

	// By default the now-orphaned chunks are left for the regular GC to reclaim:
	// an in-flight chunk-serve that began before the flip may still be reading
	// those chunk files, and deleting them mid-stream would truncate that
	// transfer. With forceReclaim the caller asserts traffic is drained, so we
	// reclaim immediately. cleanupStaleLockChunks is dedup-safe: a chunk still
	// referenced by another nar_file is retained.
	if forceReclaim {
		c.cleanupStaleLockChunks(ctx, c.chunkStore, prevChunks)
	}

	return nil
}

// linkedNarinfoNarHash returns the parsed NarHash of the narinfo linked to the
// given nar_file, or (nil, nil) when no link exists or the narinfo has no
// recorded NarHash.
func (c *Cache) linkedNarinfoNarHash(ctx context.Context, narFileID int) (*nixhash.HashWithEncoding, error) {
	ni, err := c.dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.HasNarInfoNarFilesWith(entnarinfonarfile.NarFileIDEQ(narFileID))).
		First(ctx)
	if err != nil {
		if database.IsNotFoundError(err) {
			return nil, nil //nolint:nilnil // no linked narinfo == nothing to verify against
		}

		return nil, fmt.Errorf("error resolving linked narinfo for nar_file %d: %w", narFileID, err)
	}

	if ni.NarHash == nil || *ni.NarHash == "" {
		return nil, nil //nolint:nilnil // narinfo without a NarHash == nothing to verify against
	}

	expected, err := nixhash.ParseAny(*ni.NarHash, nil)
	if err != nil {
		return nil, fmt.Errorf("error parsing narinfo NarHash %q: %w", *ni.NarHash, err)
	}

	return expected, nil
}

// MigrateNarToChunks migrates a traditional NAR blob to content-defined chunks.
// narURL is taken by pointer because storeNarWithCDC normalizes compression to "none".
func (c *Cache) MigrateNarToChunks(ctx context.Context, narURL *nar.URL) error {
	if !c.isCDCEnabled() {
		return ErrCDCDisabled
	}

	// Use a non-blocking lock to coordinate migrations and prevent a "thundering herd".
	// A long TTL is used because migrating a large NAR can be a long-running operation.
	lockKey := "migration-to-chunks:" + narURL.Hash

	acquired, err := c.downloadLocker.TryLock(ctx, lockKey, c.downloadLockTTL)
	if err != nil {
		return fmt.Errorf("failed to acquire migration lock: %w", err)
	}

	if !acquired {
		// If lock is not acquired, another process is already handling it.
		// This is not an error - the migration is being handled elsewhere.
		zerolog.Ctx(ctx).Debug().
			Str("nar_hash", narURL.Hash).
			Msg("migration to chunks already in progress by another instance")

		return ErrMigrationInProgress
	}

	defer func() {
		if err := c.downloadLocker.Unlock(context.WithoutCancel(ctx), lockKey); err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Str("nar_hash", narURL.Hash).Msg("failed to release migration lock")
		}
	}()

	// 1. Check if already chunked (Double-check after lock)
	hasChunks, err := c.HasNarInChunks(ctx, *narURL)
	if err != nil {
		return err
	}

	if hasChunks {
		// Chunks already exist. Run the post-chunking cleanup steps idempotently.
		//
		// If a previous run crashed between storeNarWithCDC (which commits the chunks
		// atomically) and the subsequent UpdateNarInfoCompressionAndURL / DeleteNar
		// steps, the narinfo URL may still reference the old compressed URL and the
		// original whole-file NAR may still be in narStore.  Running these steps again
		// is safe: UpdateNarInfoCompressionAndURL is a no-op when the URL is already
		// correct (it updates 0 rows), and DeleteNar gracefully handles ErrNotFound.
		if narURL.Compression != nar.CompressionTypeNone {
			c.migrateNarToChunksCleanup(ctx, *narURL)
		}

		return ErrNarAlreadyChunked
	}

	// 2. Fetch the NAR from the store
	_, rc, err := c.narStore.GetNar(ctx, *narURL)
	if err != nil {
		return fmt.Errorf("error fetching nar from store: %w", err)
	}
	defer rc.Close()

	// 3. Create a temporary file to store the NAR (optional, but safer for large files)
	// Actually, we can stream directly to CDC.
	// But storeNarWithCDC expects a file path. Let's use a temp file.
	f, err := os.CreateTemp(c.tempDir, "ncps-migrate-*.nar")
	if err != nil {
		return fmt.Errorf("error creating temp file: %w", err)
	}

	tempPath := f.Name()
	defer os.Remove(tempPath)

	if _, err := io.Copy(f, rc); err != nil {
		_ = f.Close() // Best effort close on error path

		return fmt.Errorf("error copying nar to temp file: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("error closing temp file: %w", err)
	}

	// 4. Store using CDC logic
	// storeNarWithCDC handles chunking, storing chunks, and DB updates.
	// Save original URL and compression before storeNarWithCDC normalizes narURL.Compression to "none".
	originalNarURL := *narURL // value copy — storeNarWithCDC mutates narURL.Compression in-place

	if err = c.storeNarWithCDC(ctx, tempPath, narURL, nil); err != nil {
		return fmt.Errorf("error storing nar with CDC: %w", err)
	}

	// 5. Update narinfo records in the database and delete the original whole-file NAR.
	// These cleanup steps are intentionally run after storeNarWithCDC commits so that
	// a crash here leaves the system in a recoverable state: the next call to
	// MigrateNarToChunks will detect hasChunks==true and re-run these steps via
	// migrateNarToChunksCleanup.
	c.migrateNarToChunksCleanup(ctx, originalNarURL)

	return nil
}

// migrateNarToChunksCleanup performs the post-chunking cleanup for a NAR that has
// been migrated from whole-file storage to CDC chunks.  It is deliberately separated
// from storeNarWithCDC so it can be called both after a fresh migration and on
// subsequent MigrateNarToChunks calls when chunks already exist (idempotency /
// crash recovery).
//
// Both operations are idempotent:
//   - UpdateNarInfoCompressionFileSizeHashAndURLParams updates 0 rows if the
//     URL/FileSize/FileHash is already correct.
//   - DeleteNar returns ErrNotFound (ignored) if the file is already gone.
//
// When lazy chunking is enabled, the compressed NAR file is NOT deleted immediately.
// Instead, it is scheduled for delayed deletion via the background cleanup job to allow
// clients to update their cache.
func (c *Cache) migrateNarToChunksCleanup(ctx context.Context, originalNarURL nar.URL) {
	newNarURL := nar.URL{
		Hash:        originalNarURL.Hash,
		Compression: nar.CompressionTypeNone,
		Query:       originalNarURL.Query,
	}

	originalURL := originalNarURL.String()
	newURL := newNarURL.String()

	if _, err := c.dbClient.Ent().NarInfo.Update().
		Where(entnarinfo.URL(originalURL)).
		SetCompression(nar.CompressionTypeNone.String()).
		SetURL(newURL).
		ClearFileSize().
		ClearFileHash().
		SetUpdatedAt(time.Now()).
		Save(ctx); err != nil {
		zerolog.Ctx(ctx).Warn().
			Err(err).
			Str("old_url", originalURL).
			Str("new_url", newURL).
			Msg("failed to update narinfo compression/URL after CDC migration")
	}

	// When lazy chunking is enabled, don't delete the compressed NAR immediately.
	// The background cleanup job will handle deletion after the configured delay.
	// This allows clients to continue fetching from the compressed file while
	// new requests are served from chunks.
	if c.GetCDCLazyChunkingEnabled() {
		zerolog.Ctx(ctx).Debug().
			Str("nar_url", originalURL).
			Msg("skipping immediate deletion of compressed NAR in lazy chunking mode")

		return
	}

	// Delete the original whole-file NAR from narStore.  We attempt both the
	// original compression and the zstd variant because Compression:none NARs are
	// stored on disk as .nar.zst.
	deletedFromStore := false

	zstdNarURL := nar.URL{Hash: originalNarURL.Hash, Compression: nar.CompressionTypeZstd, Query: originalNarURL.Query}

	deletedURLs := []nar.URL{originalNarURL}
	if originalNarURL.Compression != nar.CompressionTypeZstd {
		deletedURLs = append(deletedURLs, zstdNarURL)
	}

	for _, deleteURL := range deletedURLs {
		if err := c.narStore.DeleteNar(ctx, deleteURL); err == nil {
			deletedFromStore = true

			break
		} else if !errors.Is(err, storage.ErrNotFound) {
			zerolog.Ctx(ctx).Warn().
				Err(err).
				Str("nar_url", deleteURL.String()).
				Msg("failed to delete original whole-file NAR from narStore after CDC migration")
		}
	}

	if !deletedFromStore {
		zerolog.Ctx(ctx).Debug().
			Str("nar_url", originalURL).
			Msg("original whole-file NAR not found in narStore after CDC migration (already absent)")
	}
}

// maybeBackgroundMigrateNarToChunks checks if CDC is enabled and triggers background migration.
func (c *Cache) maybeBackgroundMigrateNarToChunks(ctx context.Context, narURL nar.URL) {
	if c.isCDCEnabled() {
		c.BackgroundMigrateNarToChunks(ctx, narURL)
	}
}

// BackgroundMigrateNarToChunks migrates a traditional NAR blob to content-defined chunks in the background.
func (c *Cache) BackgroundMigrateNarToChunks(ctx context.Context, narURL nar.URL) {
	// Use a detached context to prevent the background migration from being aborted by the request context's cancellation.
	ctx = context.WithoutCancel(ctx)

	// Track the migration in backgroundWG so Close() drains in-flight migrations on
	// shutdown. Without this, the detached goroutine can keep writing chunk files
	// after the owning cache (and, in tests, its temp chunk store) is torn down.
	c.backgroundWG.Add(1)

	analytics.SafeGo(ctx, func() {
		defer c.backgroundWG.Done()

		log := zerolog.Ctx(ctx).With().
			Str("op", "BackgroundMigrateNarToChunks").
			Str("nar_hash", narURL.Hash).
			Logger()

		log.Debug().Msg("starting background migration to chunks")

		opStartTime := time.Now()

		err := c.MigrateNarToChunks(ctx, &narURL)
		if errors.Is(err, ErrMigrationInProgress) {
			// no need to do anything, another instance is already migrating this nar.
			return
		}

		backgroundMigrationDuration.Record(
			ctx, time.Since(opStartTime).Seconds(),
			metric.WithAttributes(
				attribute.String("migration_type", migrationTypeNarToChunks),
				attribute.String("operation", migrationOperationMigrate),
			),
		)

		if err != nil {
			// if the nar is already chunked, we don't need to do anything else.
			if errors.Is(err, ErrNarAlreadyChunked) {
				log.Debug().Msg("skipping background migration to chunks, nar already chunked")

				backgroundMigrationObjectsTotal.Add(
					ctx, 1,
					metric.WithAttributes(
						attribute.String("migration_type", migrationTypeNarToChunks),
						attribute.String("operation", migrationOperationMigrate),
						attribute.String("result", migrationResultSkipped),
					),
				)

				return
			}

			log.Error().Err(err).Msg("error migrating nar to chunks")
			backgroundMigrationObjectsTotal.Add(
				ctx, 1,
				metric.WithAttributes(
					attribute.String("migration_type", migrationTypeNarToChunks),
					attribute.String("operation", migrationOperationMigrate),
					attribute.String("result", migrationResultFailure),
				),
			)

			return
		}

		backgroundMigrationObjectsTotal.Add(
			ctx, 1,
			metric.WithAttributes(
				attribute.String("migration_type", migrationTypeNarToChunks),
				attribute.String("operation", migrationOperationMigrate),
				attribute.String("result", migrationResultSuccess),
			),
		)
		log.Info().Msg("successfully migrated nar to chunks")
	})
}

// PinClosure pins a closure by its narinfo hash.
// The narinfo and all its transitive references will be protected from LRU eviction.
func (c *Cache) PinClosure(ctx context.Context, hash string) error {
	// Verify the narinfo exists before pinning, as per the spec.
	// Return 404 if the narinfo doesn't exist in the database.
	if _, err := c.dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.HashEQ(hash)).
		First(ctx); err != nil {
		if database.IsNotFoundError(err) {
			return storage.ErrNotFound
		}

		return fmt.Errorf("failed to verify narinfo existence: %w", err)
	}

	// Try to create the pinned closure. Idempotent at the SQL layer
	// via OnConflictColumns(hash).Ignore() — matches the legacy
	// `INSERT … ON CONFLICT(hash) DO NOTHING`.
	if err := c.dbClient.Ent().PinnedClosure.Create().
		SetHash(hash).
		OnConflictColumns(entpinnedclosure.FieldHash).
		Ignore().
		Exec(ctx); err != nil {
		return fmt.Errorf("failed to pin closure: %w", err)
	}

	return nil
}

// UnpinClosure unpins a closure by its narinfo hash.
func (c *Cache) UnpinClosure(ctx context.Context, hash string) error {
	if _, err := c.dbClient.Ent().PinnedClosure.Delete().
		Where(entpinnedclosure.HashEQ(hash)).
		Exec(ctx); err != nil {
		return fmt.Errorf("failed to unpin closure: %w", err)
	}

	return nil
}

// ListPinnedClosures returns all pinned closures.
func (c *Cache) ListPinnedClosures(ctx context.Context) ([]*ent.PinnedClosure, error) {
	closures, err := c.dbClient.Ent().PinnedClosure.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list pinned closures: %w", err)
	}

	return closures, nil
}

// IsNarInfoPinned checks if a narinfo hash is pinned.
func (c *Cache) IsNarInfoPinned(ctx context.Context, hash string) (bool, error) {
	exists, err := c.dbClient.Ent().PinnedClosure.Query().
		Where(entpinnedclosure.HashEQ(hash)).
		Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to check if narinfo is pinned: %w", err)
	}

	return exists, nil
}

// narInfoHashLength is the length of a Nix base32 hash (52 characters).
const narInfoHashLength = 52

// GetPinnedClosureHashes computes all hashes that are protected by pinned closures.
// This includes the pinned root hashes and all their transitive references.
func (c *Cache) GetPinnedClosureHashes(ctx context.Context) (map[string]struct{}, error) {
	// TODO: This BFS implementation makes multiple database calls per level.
	// Consider optimizing with batched queries or a recursive CTE for better performance
	// with deep or wide closure graphs.

	// Get all pinned closure roots
	pinnedClosures, err := c.dbClient.Ent().PinnedClosure.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list pinned closures: %w", err)
	}

	if len(pinnedClosures) == 0 {
		return make(map[string]struct{}), nil
	}

	// Build initial set with pinned roots
	excluded := make(map[string]struct{})
	queue := make([]string, 0, len(pinnedClosures))

	for _, pc := range pinnedClosures {
		excluded[pc.Hash] = struct{}{}
		queue = append(queue, pc.Hash)
	}

	// BFS traversal to find all references
	visited := make(map[string]struct{})
	for _, hash := range queue {
		visited[hash] = struct{}{}
	}

	maxDepth := 1000
	depth := 0

	for len(queue) > 0 && depth < maxDepth {
		levelSize := len(queue)
		depth++

		for i := 0; i < levelSize; i++ {
			currentHash := queue[0]
			queue = queue[1:]

			// Get the narinfo + its references in one round-trip
			ni, err := c.dbClient.Ent().NarInfo.Query().
				Where(entnarinfo.HashEQ(currentHash)).
				WithReferences().
				Only(ctx)
			if err != nil {
				if database.IsNotFoundError(err) {
					continue // Narinfo not in database, skip
				}

				return nil, fmt.Errorf("failed to get narinfo %s: %w", currentHash, err)
			}

			for _, refEdge := range ni.Edges.References {
				ref := refEdge.Reference
				// Extract hash from reference (format: <hash>-<name>-<version>)
				// Hash is the first 52 characters
				if len(ref) < narInfoHashLength {
					continue
				}

				refHash := ref[:narInfoHashLength]

				// Skip if already visited
				if _, exists := visited[refHash]; exists {
					continue
				}

				visited[refHash] = struct{}{}
				excluded[refHash] = struct{}{}
				queue = append(queue, refHash)
			}
		}
	}

	if depth >= maxDepth {
		zerolog.Ctx(ctx).Warn().Msg("max closure traversal depth reached, some pinned references may not be protected")
	}

	return excluded, nil
}
