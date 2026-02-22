package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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

	"github.com/kalbasit/ncps/pkg/analytics"
	"github.com/kalbasit/ncps/pkg/cache/healthcheck"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/chunker"
	"github.com/kalbasit/ncps/pkg/config"
	"github.com/kalbasit/ncps/pkg/database"
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

	// Buffer size of 2 allows one chunk to be copied while the next is being fetched.
	prefetchBufferSize = 2

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

var (
	// ErrHostnameRequired is returned if the given hostName to New is not given.
	ErrHostnameRequired = errors.New("hostName is required")

	// ErrHostnameMustNotContainScheme is returned if the given hostName to New contained a scheme.
	ErrHostnameMustNotContainScheme = errors.New("hostName must not contain scheme")

	// ErrHostnameNotValid is returned if the given hostName to New is not valid.
	ErrHostnameNotValid = errors.New("hostName is not valid")

	// ErrHostnameMustNotContainPath is returned if the given hostName to New contained a path.
	ErrHostnameMustNotContainPath = errors.New("hostName must not contain a path")

	// errNarInfoPurged is returned if the narinfo was purged.
	errNarInfoPurged = errors.New("the narinfo was purged")

	// ErrCDCDisabled is returned when CDC is required but not enabled.
	ErrCDCDisabled = errors.New("CDC must be enabled and chunk store configured for migration")

	// ErrNarAlreadyChunked is returned when the nar is already chunked.
	ErrNarAlreadyChunked = errors.New("nar is already chunked")

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
}

// Cache represents the main cache service.
type Cache struct {
	hostName      string
	secretKey     signature.SecretKey
	healthChecker *healthcheck.HealthChecker
	maxSize       uint64
	db            database.Querier

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
}

func (r *fileAvailableReader) Read(p []byte) (int, error) {
	r.ds.mu.Lock()

	for r.offset >= r.ds.bytesWritten && r.ds.finalSize == 0 && r.ds.downloadError == nil {
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
	db database.Querier,
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
		db:                   db,
		config:               config.New(db, cacheLocker),
		configStore:          configStore,
		narInfoStore:         narInfoStore,
		narStore:             narStore,
		shouldSignNarinfo:    true,
		downloadLocker:       downloadLocker,
		cacheLocker:          cacheLocker,
		downloadLockTTL:      downloadLockTTL,
		downloadPollTimeout:  downloadPollTimeout,
		cacheLockTTL:         cacheLockTTL,
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

func (c *Cache) setupMetricCallbacks() error {
	_, err := meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		// Observe total size
		size, err := c.db.GetNarTotalSize(ctx)
		if err != nil {
			// Log error but don't fail the scrape entirely
			zerolog.Ctx(ctx).
				Warn().
				Err(err).
				Msg("failed to get total nar size for metrics")
		} else {
			o.ObserveInt64(totalSizeMetric, size)
		}

		// Observe narinfo count
		narInfoCount, err := c.db.GetNarInfoCount(ctx)
		if err != nil {
			zerolog.Ctx(ctx).
				Warn().
				Err(err).
				Msg("failed to get narinfo count for metrics")
		} else {
			o.ObserveInt64(narInfoCountMetric, narInfoCount)
		}

		// Observe nar file count
		narFileCount, err := c.db.GetNarFileCount(ctx)
		if err != nil {
			zerolog.Ctx(ctx).
				Warn().
				Err(err).
				Msg("failed to get nar file count for metrics")
		} else {
			o.ObserveInt64(narFileCountMetric, narFileCount)
		}

		// Observe cache max size (static value)
		//nolint:gosec // G115: Cache max size is configured and unlikely to exceed int64 max (9.2 exabytes)
		o.ObserveInt64(cacheMaxSizeBytes, int64(c.maxSize))

		// Observe cache utilization ratio
		if c.maxSize > 0 && size > 0 {
			utilizationRatio := float64(size) / float64(c.maxSize)
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
func (c *Cache) SetTempDir(d string) { c.tempDir = d }

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

// StartCron starts the cron scheduler in its own go-routine, or no-op if already started.
func (c *Cache) StartCron(ctx context.Context) {
	zerolog.Ctx(ctx).
		Info().
		Msg("starting the cron scheduler")

	c.cron.Start()
}

// Close waits for all background operations to complete.
func (c *Cache) Close() {
	c.backgroundWG.Wait()

	if c.cron != nil {
		c.cron.Stop()
	}
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
// stored and finally returned.
// NOTE: It's the caller responsibility to close the body.
func (c *Cache) GetNar(ctx context.Context, narURL nar.URL) (int64, io.ReadCloser, error) {
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

		hasNarInStore := c.hasNarInStore(ctx, narURL)

		var err error

		hasNar := hasNarInStore
		if !hasNar {
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
		preferredUpstreamURL := c.lookupPreferredUpstreamURL(ctx, narURL)

		// create a detachedCtx that has the same span and logger as the main
		// context but with the baseContext as parent; This context will not cancel
		// when ctx is canceled allowing us to continue pulling the nar in the
		// background.
		detachedCtx := context.WithoutCancel(ctx)
		ds := c.prePullNar(ctx, detachedCtx, &narURL, preferredUpstreamURL, nil)

		// Check if download is complete (closed=true) before adding to WaitGroup
		// This prevents race with cleanup goroutine calling ds.wg.Wait()
		ds.mu.Lock()

		canStream := !ds.closed
		if canStream {
			ds.wg.Add(1)
		}

		ds.mu.Unlock()

		hasNarInStore = c.hasNarInStore(ctx, narURL)

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

			metricAttrs = append(metricAttrs,
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

		metricAttrs = append(metricAttrs,
			attribute.String("result", "miss"),
			attribute.String("status", "success"),
		)

		select {
		case <-ds.start:
			// Download has started
		case <-ctx.Done():
			// Context canceled before download started
			metricAttrs = append(metricAttrs, attribute.String("status", "error"))

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

				fileReader := &fileAvailableReader{f: f, ds: ds}

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
				}

				return
			}

			var f *os.File

			var bytesSent int64

			for {
				ds.mu.Lock()

				for bytesSent >= ds.bytesWritten && ds.finalSize == 0 {
					ds.cond.Wait() // Put this goroutine to sleep until a broadcast is received from the downloader

					// check for error just in case otherwise we'd end up sleeping forever
					// in case an error happened and the downloader bailed after its last
					// broadcast in the defered function.
					if ds.downloadError != nil {
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
		return 0, nil, err
	}

	return size, reader, nil
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

	nr, err := c.getNarFileFromDB(ctx, c.db, nu)
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
	urlStr, err := c.db.GetNarInfoURLByNarFileHash(ctx, database.GetNarInfoURLByNarFileHashParams{
		Hash:        normalizedNarURL.Hash,
		Compression: normalizedNarURL.Compression.String(),
		Query:       normalizedNarURL.Query.Encode(),
	})
	if err != nil {
		// Not found is an expected case. We should log any other database errors.
		if !database.IsNotFoundError(err) && !errors.Is(err, sql.ErrNoRows) {
			zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to lookup original nar URL")
		}

		return normalizedNarURL
	}

	if urlStr.Valid && urlStr.String != "" {
		originalURL, parseErr := nar.ParseURL(urlStr.String)
		if parseErr == nil {
			return originalURL
		}
		// Log if we have a URL in the DB but can't parse it.
		zerolog.Ctx(ctx).Warn().Err(parseErr).Str("url", urlStr.String).Msg("Failed to parse original nar URL from DB")
	}

	// If parsing fails or URL is invalid/empty, return the normalized URL unchanged
	return normalizedNarURL
}

// lookupPreferredUpstreamURL returns the original compressed URL for a CDC NAR
// (e.g. the xz URL) by looking up the narinfo hash in the DB and fetching the
// narinfo from upstream. This allows CDC first pulls to download the compressed
// version instead of the uncompressed version, reducing TTFB significantly.
// Returns nil if CDC is not enabled, there is an active local download, or the
// original URL cannot be found.
func (c *Cache) lookupPreferredUpstreamURL(ctx context.Context, narURL nar.URL) *nar.URL {
	if !c.isCDCEnabled() || narURL.Compression != nar.CompressionTypeNone || c.hasUpstreamJob(narURL.Hash) {
		return nil
	}

	narInfoHash, err := c.db.GetNarInfoHashByNarURL(ctx, sql.NullString{String: narURL.String(), Valid: true})
	if err != nil {
		if !database.IsNotFoundError(err) && !errors.Is(err, sql.ErrNoRows) {
			zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to lookup narinfo hash by nar URL")
		}

		return nil
	}

	if narInfoHash == "" {
		return nil
	}

	_, upstreamNarInfo, err := c.getNarInfoFromUpstream(ctx, narInfoHash)
	if err != nil || upstreamNarInfo == nil {
		return nil
	}

	originalURL, err := nar.ParseURL(upstreamNarInfo.URL)
	if err != nil {
		zerolog.Ctx(ctx).
			Warn().
			Err(err).
			Str("url", upstreamNarInfo.URL).
			Msg("Failed to parse preferred upstream nar URL")

		return nil
	}

	if originalURL.Compression == nar.CompressionTypeNone {
		return nil
	}

	return &originalURL
}

// ensureNarFileRecord ensures a NarFile record exists with the correct size.
// It creates the record if it doesn't exist, or updates the size if it's incorrect.
func (c *Cache) ensureNarFileRecord(ctx context.Context, narURL nar.URL, written int64, txName string) error {
	return c.withTransaction(ctx, txName, func(qtx database.Querier) error {
		nf, err := qtx.CreateNarFile(ctx, database.CreateNarFileParams{
			Hash:        narURL.Hash,
			Compression: narURL.Compression.String(),
			Query:       narURL.Query.Encode(),
			//nolint:gosec // G115: conversion is safe because size is non-negative
			FileSize:    uint64(written),
			TotalChunks: 0, // initially 0, background job will chunk if needed
		})
		if err != nil {
			return err
		}

		// If the record existed but had a different size, update it to reflect the truth.
		//nolint:gosec // G115: conversion is safe because size is non-negative
		if nf.FileSize != uint64(written) {
			return qtx.UpdateNarFileFileSize(ctx, database.UpdateNarFileFileSizeParams{
				ID: nf.ID,
				//nolint:gosec // G115: conversion is safe because size is non-negative
				FileSize: uint64(written),
			})
		}

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

		written, err := c.narStore.PutNar(ctx, narURL, r)
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
	pattern := filepath.Base(narURL.Hash) + "-*.nar"
	if cext := narURL.Compression.String(); cext != "" {
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

	ds.assetPath = f.Name()

	return f, nil
}

// streamResponseToFile streams the HTTP response body to a file in chunks,
// updating download state and broadcasting progress to waiting clients.
func (c *Cache) streamResponseToFile(ctx context.Context, resp *http.Response, f *os.File, ds *downloadState) error {
	buf := make([]byte, 32*1024)

	for {
		n, err := resp.Body.Read(buf)
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

	// Writing the NAR to a temporary file is now done, final notification to watchers
	ds.mu.Lock()
	ds.finalSize = ds.bytesWritten
	ds.mu.Unlock()
	ds.cond.Broadcast()

	return nil
}

// storeNarFromTempFile reopens the temporary file and stores it in the NAR store.
func (c *Cache) storeNarFromTempFile(ctx context.Context, tempPath string, narURL *nar.URL) error {
	if c.isCDCEnabled() {
		return c.storeNarWithCDC(ctx, tempPath, narURL, nil)
	}

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
	if narURL.Compression == nar.CompressionTypeNone {
		zerolog.Ctx(ctx).Debug().Msg("re-compressing uncompressed NAR as zstd before storing")

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
	}

	written, err := c.narStore.PutNar(ctx, storeURL, reader)
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

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("error stating temp file: %w", err)
	}

	//nolint:gosec // G115: File size from Stat is non-negative
	fileSize := uint64(fi.Size())

	// For CDC, always store raw uncompressed data in chunks.
	// Save original compression before normalizing narURL.
	originalCompression := narURL.Compression
	narURL.Compression = nar.CompressionTypeNone

	// 1. Create or get NarFile record
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
	var reader io.Reader = f

	if originalCompression != nar.CompressionTypeNone {
		decompressed, decompErr := nar.DecompressReader(ctx, f, originalCompression)
		if decompErr != nil {
			// If decompression fails, log a warning and proceed with raw data.
			// This can happen if the stored metadata doesn't match the actual data compression
			// (e.g., during migration of NARs that have xz metadata but aren't actually compressed).
			zerolog.Ctx(ctx).Warn().
				Err(decompErr).
				Str("compression_type", originalCompression.String()).
				Msg("failed to create decompression reader for CDC, proceeding with raw data")

			// Seek back to the beginning since the failed reader creation may have consumed bytes.
			if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
				return fmt.Errorf("error seeking file after failed decompression: %w", seekErr)
			}
		} else {
			defer decompressed.Close()

			reader = decompressed
		}
	}

	chunksChan, errChan := cdcChunker.Chunk(ctx, reader)

	var (
		totalSize  int64
		chunkCount int64
	)

	var batch []chunker.Chunk

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

				// All chunks processed - mark as complete and update file_size
				// to the actual uncompressed size (may differ from original compressed file size).
				err := c.withTransaction(ctx, "storeNarWithCDC.MarkComplete", func(qtx database.Querier) error {
					return qtx.UpdateNarFileTotalChunks(ctx, database.UpdateNarFileTotalChunksParams{
						ID:          narFileID,
						TotalChunks: chunkCount,
						//nolint:gosec // G115: totalSize is non-negative
						FileSize: uint64(totalSize),
					})
				})
				if err != nil {
					return fmt.Errorf("error marking chunking complete: %w", err)
				}

				// If compression was normalized (e.g., xz → none), atomically clean up the old
				// NarFile record and re-link narinfos to the new one in a single transaction.
				//
				// ATOMICITY REQUIREMENT: DeleteNarFileByHash CASCADE-deletes narinfo_nar_files.
				// relinkNarInfosToNarFile must run in the same transaction so that narinfos are
				// never left without a nar_file link if the process is killed between the two ops.
				if originalCompression != nar.CompressionTypeNone {
					oldNarURL := nar.URL{
						Hash:        narURL.Hash,
						Compression: originalCompression,
						Query:       narURL.Query,
					}

					if err := c.withTransaction(ctx, "storeNarWithCDC.RelinkAndCleanup", func(qtx database.Querier) error {
						if _, err := qtx.DeleteNarFileByHash(ctx, database.DeleteNarFileByHashParams{
							Hash:        narURL.Hash,
							Compression: originalCompression.String(),
							Query:       narURL.Query.Encode(),
						}); err != nil {
							return fmt.Errorf("failed to delete old nar_file record: %w", err)
						}

						return c.relinkNarInfosToNarFileWithQuerier(ctx, qtx, oldNarURL, narFileID)
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
) (narFileID int64, staleLockChunks []database.Chunk, err error) {
	err = c.withTransaction(ctx, "storeNarWithCDC.CreateNarFile", func(qtx database.Querier) error {
		nr, err := qtx.CreateNarFile(ctx, database.CreateNarFileParams{
			Hash:        narURL.Hash,
			Compression: narURL.Compression.String(),
			Query:       narURL.Query.Encode(),
			FileSize:    fileSize,
			TotalChunks: 0, // Mark as "in progress"
		})
		if err != nil {
			return err
		}

		// If the record existed but had a different size, update it to reflect the truth.
		// However, in CDC mode, once chunked, FileSize holds the uncompressed size.
		// We should only update it if it's not yet fully chunked.
		if nr.FileSize != fileSize && nr.TotalChunks == 0 {
			if err := qtx.UpdateNarFileFileSize(ctx, database.UpdateNarFileFileSizeParams{
				ID:       nr.ID,
				FileSize: fileSize,
			}); err != nil {
				return err
			}
		}

		narFileID = nr.ID

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

		if nr.ChunkingStartedAt.Valid {
			age := time.Since(nr.ChunkingStartedAt.Time)
			if age < cdcChunkingLockTTL {
				// Another in-progress attempt is still within the TTL — skip.
				return storage.ErrAlreadyExists
			}

			// Lock is stale: a previous attempt was interrupted mid-chunking.
			// Collect the partial chunk records so we can clean them up after the
			// transaction commits (chunk files live outside the DB transaction).
			partialChunks, err := qtx.GetChunksByNarFileID(ctx, narFileID)
			if err != nil {
				return fmt.Errorf("failed to get chunks for stale nar_file %d: %w", narFileID, err)
			}

			staleLockChunks = partialChunks

			zerolog.Ctx(ctx).Warn().
				Dur("age", age).
				Int64("narFileID", narFileID).
				Int("stale_chunk_count", len(staleLockChunks)).
				Msg("stale CDC chunking lock detected; cleaning up partial chunks and restarting")

			if err := qtx.DeleteNarFileChunksByNarFileID(ctx, narFileID); err != nil {
				return fmt.Errorf("failed to delete partial chunks for nar_file %d: %w", narFileID, err)
			}
		}

		// Mark this nar_file as having chunking in progress.
		if err := qtx.SetNarFileChunkingStarted(ctx, narFileID); err != nil {
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
func (c *Cache) cleanupStaleLockChunks(ctx context.Context, cs chunk.Store, staleLockChunks []database.Chunk) {
	if len(staleLockChunks) == 0 {
		return
	}

	log := zerolog.Ctx(ctx).With().
		Int("stale_chunk_count", len(staleLockChunks)).
		Logger()

	// Build a map from chunk ID → hash for O(1) lookup.
	staleByID := make(map[int64]string, len(staleLockChunks))
	for _, ch := range staleLockChunks {
		staleByID[ch.ID] = ch.Hash
	}

	// Find all currently orphaned chunks (no nar_file_chunks references).
	// Run this outside the previous transaction so we see the committed deletion.
	orphanedChunks, err := c.db.GetOrphanedChunks(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("failed to get orphaned chunks during stale lock cleanup; will rely on GC")

		return
	}

	for _, oc := range orphanedChunks {
		hash, ok := staleByID[oc.ID]
		if !ok {
			continue // Not one of our stale lock chunks.
		}

		chunkLog := log.With().Str("chunk_hash", hash).Int64("chunk_id", oc.ID).Logger()
		chunkLog.Debug().Msg("immediately cleaning up chunk from stale CDC lock")

		// Delete the DB record first; if this fails, leave the physical file for GC.
		if err := c.db.DeleteChunkByID(ctx, oc.ID); err != nil {
			chunkLog.Warn().Err(err).Msg("failed to delete orphaned chunk record during stale lock cleanup")

			continue
		}

		// Delete the physical chunk file.
		if err := cs.DeleteChunk(ctx, hash); err != nil && !errors.Is(err, chunk.ErrNotFound) {
			chunkLog.Warn().Err(err).Msg("failed to delete orphaned chunk file during stale lock cleanup")
		}
	}
}

// relinkNarInfosToNarFileWithQuerier links all narinfos pointing to narURL to narFileID
// in a single bulk INSERT ... SELECT. Called after CDC migration to repair
// narinfo_nar_files entries that were CASCADE-deleted when the old nar_file
// record was removed. It accepts any database.Querier (including a transaction
// querier) so the bulk re-link can be executed within the same transaction as
// the preceding DeleteNarFileByHash call.
func (c *Cache) relinkNarInfosToNarFileWithQuerier(
	ctx context.Context,
	q database.Querier,
	narURL nar.URL,
	narFileID int64,
) error {
	if err := q.LinkNarInfosByURLToNarFile(ctx, database.LinkNarInfosByURLToNarFileParams{
		NarFileID: narFileID,
		URL:       sql.NullString{String: narURL.String(), Valid: true},
	}); err != nil {
		return fmt.Errorf("failed to link narinfos by URL %q to nar_file %d: %w", narURL.String(), narFileID, err)
	}

	return nil
}

func (c *Cache) recordChunkBatch(ctx context.Context, narFileID int64, startIndex int64, batch []chunker.Chunk) error {
	if len(batch) == 0 {
		return nil
	}

	return c.withTransaction(ctx, "recordChunkBatch", func(qtx database.Querier) error {
		chunkIDs := make([]int64, len(batch))
		chunkIndices := make([]int64, len(batch))

		for i, chunkMetadata := range batch {
			// Create or increment ref count.
			ch, err := qtx.CreateChunk(ctx, database.CreateChunkParams{
				Hash:           chunkMetadata.Hash,
				Size:           chunkMetadata.Size,
				CompressedSize: chunkMetadata.CompressedSize,
			})
			if err != nil {
				return fmt.Errorf("error creating chunk record: %w", err)
			}

			chunkIDs[i] = ch.ID
			chunkIndices[i] = startIndex + int64(i)
		}

		// Link to NAR file in bulk
		err := qtx.LinkNarFileToChunks(ctx, database.LinkNarFileToChunksParams{
			NarFileID:  narFileID,
			ChunkID:    chunkIDs,
			ChunkIndex: chunkIndices,
		})
		if err != nil {
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

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)

		resp.Body.Close()
	}()

	f, err := c.createTempNarFile(ctx, narURL, ds)
	if err != nil {
		ds.setError(err)

		return
	}

	defer f.Close()

	ds.assetPath = f.Name()

	// Cleanup: wait for download to complete, then wait for all readers to finish
	c.backgroundWG.Add(1)
	analytics.SafeGo(ctx, func() {
		defer c.backgroundWG.Done()

		ds.cleanupWg.Wait() // Wait for download to complete

		// For CDC: wait for the background chunking goroutine to finish before
		// preventing new readers. This allows concurrent GetNar calls to stream
		// from the temp file while CDC chunking is in progress (cdcWg is zero
		// for non-CDC downloads, so Wait() returns immediately in that case).
		ds.cdcWg.Wait()

		// Mark as closed to prevent new readers from adding to WaitGroup
		ds.mu.Lock()
		ds.closed = true
		ds.mu.Unlock()

		ds.wg.Wait() // Then wait for all readers to finish
		os.Remove(ds.assetPath)
	})

	// Record the actual compression type of the bytes written to the temp file.
	// This allows the streaming goroutine to detect when it needs to decompress
	// (e.g., when a CDC-enabled GetNar client requests compression:none but we
	// downloaded xz from upstream via preferredUpstreamURL for better TTFB).
	ds.tempFileCompression = downloadURL.Compression

	// Signal that temp file is ready for streaming
	ds.startOnce.Do(func() { close(ds.start) })

	err = c.streamResponseToFile(ctx, resp, f, ds)
	if err != nil {
		ds.setError(err)

		return
	}

	if c.isCDCEnabled() {
		// For CDC: create the nar_file DB record synchronously (fast), signal ds.stored so
		// the HTTP connection can close immediately, then run the actual chunking in a
		// background goroutine. This prevents the HTTP response from waiting ~18s for CDC
		// chunking to complete on first pull of a large NAR.
		//
		// We keep the job in upstreamJobs (keepJobAlive=true) so concurrent GetNar calls on
		// THIS server can find ds and stream from the temp file while chunking is in progress.
		// The CDC goroutine is responsible for removing the job and closing ds.done when done.
		//
		// cdcWg prevents the temp-file cleanup goroutine from setting ds.closed=true until
		// after the CDC goroutine is done, allowing concurrent readers to join via ds.wg.
		keepJobAlive = true

		ds.cdcWg.Add(1)
		ds.wg.Add(1)

		c.backgroundWG.Add(1)

		analytics.SafeGo(ctx, func() {
			defer c.backgroundWG.Done()

			// Defers execute LIFO: wg.Done fires 1st (remove CDC from reader count),
			// then cdcWg.Done fires 2nd (unblocks cleanup goroutine to set closed=true),
			// then the inline func fires 3rd (remove job, close ds.done, broadcast).
			defer func() {
				// Remove job from upstreamJobs so new GetNar calls serve from chunks.
				c.upstreamJobsMu.Lock()
				delete(c.upstreamJobs, narJobKey(narURL.Hash))
				c.upstreamJobsMu.Unlock()

				// Inform all waiters (e.g. distributed lock releaser) that CDC is done.
				ds.doneOnce.Do(func() { close(ds.done) })
				ds.cond.Broadcast()
			}()
			defer ds.cdcWg.Done()
			defer ds.wg.Done()

			// onNarFileReady is called inside storeNarWithCDC right after findOrCreateNarFileForCDC
			// succeeds (i.e., the nar_file DB record exists). At that point, other servers can see
			// the record and enter progressive chunk streaming instead of starting a duplicate
			// download. We signal ds.stored here so the distributed lock can also be released.
			onNarFileReady := func() {
				ds.storedOnce.Do(func() { close(ds.stored) })
			}

			if err := c.storeNarWithCDC(ctx, ds.assetPath, narURL, onNarFileReady); err != nil {
				zerolog.Ctx(ctx).
					Error().
					Err(err).
					Msg("CDC chunking failed in background after pullNarIntoStore")
				ds.setError(err)

				return // Defers will still run to clean up.
			}

			if err := c.checkAndFixNarInfosForNar(context.WithoutCancel(ctx), *narURL); err != nil {
				zerolog.Ctx(ctx).
					Warn().
					Err(err).
					Msg("failed to fix narinfo file size after pullNarIntoStore (CDC)")
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

	// Signal that the asset is now in final storage and the distributed lock can be released
	// This prevents the race condition where other instances check hasAsset() before storage completes
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

	if hasInStore {
		storageSize, storageReader, err = c.getNarFromStore(ctx, narURL)
	} else {
		storageSize, storageReader, err = c.getNarFromChunks(ctx, narURL)
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

	if decompress {
		decompressed, decompErr := nar.DecompressReader(ctx, r, nar.CompressionTypeZstd)
		if decompErr != nil {
			_ = r.Close()

			return 0, nil, fmt.Errorf("error decompressing nar from store: %w", decompErr)
		}

		r = decompressed
		size = -1 // decompressed size is unknown
	}

	var needsDBRecord bool

	err = c.withTransaction(ctx, "getNarFromStore", func(qtx database.Querier) error {
		nr, err := c.getNarFileFromDB(ctx, qtx, *narURL)
		if err != nil {
			if database.IsNotFoundError(err) {
				// NAR is in storage but has no DB record — this is an orphan left by a
				// crash between narStore.PutNar and ensureNarFileRecord. Schedule healing.
				needsDBRecord = true

				return nil
			}

			return fmt.Errorf("error fetching the nar record: %w", err)
		}

		if lat, err := nr.LastAccessedAt.Value(); err == nil && time.Since(lat.(time.Time)) > c.recordAgeIgnoreTouch {
			if _, err := qtx.TouchNarFile(ctx, database.TouchNarFileParams{
				Hash:        narURL.Hash,
				Compression: narURL.Compression.String(),
				Query:       narURL.Query.Encode(),
			}); err != nil {
				return fmt.Errorf("error touching the nar record: %w", err)
			}
		}

		return nil
	})
	if err != nil {
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
			zerolog.Ctx(ctx).
				Error().
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

	if !c.hasNarInStore(ctx, *narURL) {
		return storage.ErrNotFound
	}

	if _, err := c.db.DeleteNarFileByHash(ctx, database.DeleteNarFileByHashParams{
		Hash:        narURL.Hash,
		Compression: narURL.Compression.String(),
		Query:       narURL.Query.Encode(),
	}); err != nil {
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

	var narInfo *narinfo.NarInfo

	err := c.withReadLock(ctx, "GetNarInfo", narInfoJobKey(hash), func() error {
		ctx = zerolog.Ctx(ctx).
			With().
			Str("narinfo_hash", hash).
			Logger().
			WithContext(ctx)

		var err error

		narInfo, err = c.getNarInfoFromDatabase(ctx, hash)
		if err == nil {
			metricAttrs = append(metricAttrs,
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
				}

				zerolog.Ctx(ctx).
					Debug().
					Str("narinfo", narInfo.String()).
					Msg("fetched this narinfo from the database")
			}

			return nil
		} else if !errors.Is(err, storage.ErrNotFound) && !errors.Is(err, errNarInfoPurged) {
			zerolog.Ctx(ctx).
				Error().
				Err(err).
				Msg("error fetching the narinfo from the database")

			return fmt.Errorf("error fetching narinfo from database: %w", err)
		}

		if c.narInfoStore.HasNarInfo(ctx, hash) {
			metricAttrs = append(metricAttrs,
				attribute.String("result", "hit"),
				attribute.String("status", "success"),
				attribute.String("source", "storage"),
			)

			narInfo, err = c.getNarInfoFromStore(ctx, hash)
			if err == nil {
				if zerolog.Ctx(ctx).GetLevel() <= zerolog.DebugLevel {
					zerolog.Ctx(ctx).
						Debug().
						Str("narinfo", narInfo.String()).
						Msg("fetched this narinfo from the store")
				}

				return nil
			}

			// If narinfo was purged, continue to fetch from upstream
			if !errors.Is(err, errNarInfoPurged) {
				return c.handleStorageFetchError(ctx, hash, err, &narInfo, &metricAttrs)
			}
		}

		metricAttrs = append(metricAttrs, attribute.String("result", "miss"))

		// If the artifact is not in the DB or Store, check if we are in "Upload Only" mode.
		// If so, we return ErrNotFound immediately to let the client know we don't have it locally,
		// triggering the PUT (push) operation.
		if IsUploadOnly(ctx) {
			return storage.ErrNotFound
		}

		ds := c.prePullNarInfo(ctx, hash)

		zerolog.Ctx(ctx).
			Debug().
			Msg("pulling nar in a go-routing and will wait for it")
		<-ds.done

		err = ds.getError()
		if err != nil {
			metricAttrs = append(metricAttrs, attribute.String("status", "error"))

			// Add upstream hostname to metrics even on error
			if upstreamHostname := ds.getUpstreamHostname(); upstreamHostname != "" {
				metricAttrs = append(metricAttrs,
					attribute.String("upstream_hostname", upstreamHostname))
			}

			return err
		}

		// Add upstream hostname to metrics on success
		if upstreamHostname := ds.getUpstreamHostname(); upstreamHostname != "" {
			metricAttrs = append(metricAttrs,
				attribute.String("upstream_hostname", upstreamHostname))
		}

		// After pulling from upstream, get the narinfo from the database (where it's now stored)
		narInfo, err = c.getNarInfoFromDatabase(ctx, hash)
		if err != nil {
			zerolog.Ctx(ctx).
				Error().
				Err(err).
				Msg("failed to fetch this narinfo from the database")

			metricAttrs = append(metricAttrs, attribute.String("status", "error"))

			return err
		}

		if zerolog.Ctx(ctx).GetLevel() <= zerolog.DebugLevel {
			zerolog.Ctx(ctx).
				Debug().
				Str("narinfo", narInfo.String()).
				Msg("fetched narinfo from database after upstream pull")
		}

		return nil
	})
	if err != nil {
		return nil, err
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

	c.prePullNar(ctx, detachedCtx, &narURLForBG, nil, uc)

	// For CDC mode, NARs are stored as raw uncompressed chunks.
	// For Compression:none upstreams, NARs are stored as zstd files and served
	// as Compression:none with transparent HTTP encoding.
	// Normalize narInfo to reflect this regardless of upstream compression.
	// Note: we must NOT modify narURL here since prePullNar may still be using
	// the pointer in a background goroutine. Instead, build the normalized URL string directly.
	if c.isCDCEnabled() || narInfo.Compression == nar.CompressionTypeNone.String() {
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

	// Signal that the asset is now in final storage and the distributed lock can be released
	// This prevents the race condition where other instances check hasAsset() before storage completes
	ds.storedOnce.Do(func() { close(ds.stored) })

	if err := c.storeInDatabase(ctx, hash, narInfo); err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error storing the narinfo in the database")

		ds.setError(err)

		return
	}

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

	if err := c.checkAndFixNarInfo(context.WithoutCancel(ctx), hash); err != nil {
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

	return c.withReadLock(ctx, "DeleteNarInfo", narInfoJobKey(hash), func() error {
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
	})
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
		ctx,
		narInfoJobKey(hash),
		hash,
		true,
		func(ctx context.Context) bool {
			return c.narInfoStore.HasNarInfo(ctx, hash)
		},
		func(ds *downloadState) {
			c.pullNarInfo(ctx, hash, ds)
		},
	)
}

func (c *Cache) prePullNar(
	coordCtx context.Context,
	ctx context.Context,
	narURL *nar.URL,
	preferredUpstreamURL *nar.URL,
	uc *upstream.Cache,
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
			hasInStore := c.hasNarInStore(ctx, *narURL)
			if hasInStore {
				return true
			}

			// Check if NAR file record exists (even if chunking in progress)
			// This allows progressive streaming to work only if CDC is enabled
			if c.isCDCEnabled() {
				hasInChunks, _ := c.HasNarFileRecord(ctx, *narURL)

				return hasInChunks
			}

			return false
		},
		func(ds *downloadState) {
			c.pullNarIntoStore(ctx, narURL, preferredUpstreamURL, uc, ds)
		},
	)
}

// getNarFileFromDB looks up a nar_file record by URL using the given querier.
// It tries the most likely compression first based on whether CDC is enabled:
//   - CDC enabled:  try "none" first (all CDC files use none), fall back to original
//   - CDC disabled: try original compression first, fall back to "none"
func (c *Cache) getNarFileFromDB(ctx context.Context, q database.Querier, narURL nar.URL) (database.NarFile, error) {
	first, second := narURL.Compression, nar.CompressionTypeNone
	if c.isCDCEnabled() {
		first, second = nar.CompressionTypeNone, narURL.Compression
	}

	nr, err := q.GetNarFileByHashAndCompressionAndQuery(ctx, database.GetNarFileByHashAndCompressionAndQueryParams{
		Hash:        narURL.Hash,
		Compression: first.String(),
		Query:       narURL.Query.Encode(),
	})
	if err == nil {
		return nr, nil
	}

	if database.IsNotFoundError(err) && first != second {
		return q.GetNarFileByHashAndCompressionAndQuery(ctx, database.GetNarFileByHashAndCompressionAndQueryParams{
			Hash:        narURL.Hash,
			Compression: second.String(),
			Query:       narURL.Query.Encode(),
		})
	}

	return database.NarFile{}, err
}

// hasNarInStore checks if the NAR exists in the storage, handling the .nar.zst fallback for CompressionTypeNone.
func (c *Cache) hasNarInStore(ctx context.Context, narURL nar.URL) bool {
	// For Compression:none NARs, the physical file is stored as .nar.zst; check that first.
	if narURL.Compression == nar.CompressionTypeNone {
		zstdURL := narURL

		zstdURL.Compression = nar.CompressionTypeZstd
		if c.narStore.HasNar(ctx, zstdURL) {
			return true
		}
	}

	return c.narStore.HasNar(ctx, narURL)
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

	// For Compression:none NARs, the physical file is stored as .nar.zst; check that first.
	hasNarInStore := false

	if narURL.Compression == nar.CompressionTypeNone {
		zstdURL := narURL
		zstdURL.Compression = nar.CompressionTypeZstd
		hasNarInStore = c.narStore.HasNar(ctx, zstdURL)
	}

	if !hasNarInStore {
		hasNarInStore = c.narStore.HasNar(ctx, narURL)
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

	err = c.withTransaction(ctx, "getNarInfoFromStore", func(qtx database.Querier) error {
		nir, err := qtx.GetNarInfoByHash(ctx, hash)
		if err != nil {
			if database.IsNotFoundError(err) {
				c.backgroundMigrateNarInfo(ctx, hash, ni)

				return nil
			}

			return fmt.Errorf("error fetching the narinfo record: %w", err)
		}

		// Migrate narinfos from storage to the database.
		if !nir.URL.Valid {
			c.backgroundMigrateNarInfo(ctx, hash, ni)
		}

		if c.isCDCEnabled() {
			c.BackgroundMigrateNarToChunks(ctx, narURL)
		}

		if lat, err := nir.LastAccessedAt.Value(); err == nil && time.Since(lat.(time.Time)) > c.recordAgeIgnoreTouch {
			if _, err := qtx.TouchNarInfo(ctx, hash); err != nil {
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

		if err := c.withReadLock(ctx,
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

	err := c.withTransaction(ctx, "getNarInfoFromDatabase", func(qtx database.Querier) error {
		var populateErr error

		ni, narURL, populateErr = c.populateNarInfoFromDatabase(ctx, qtx, hash, true)

		return populateErr
	})
	if err != nil {
		return nil, err
	}

	// Verify Nar file exists in storage.
	// For Compression:none NARs, the physical file is stored as .nar.zst; check that first.
	hasNar := c.hasNarInStore(ctx, *narURL)

	if !hasNar {
		var err error

		hasNar, err = c.HasNarInChunks(ctx, *narURL)
		if err != nil {
			return nil, fmt.Errorf("failed to check if nar exists in chunks: %w", err)
		}
	}

	if !hasNar && !c.hasUpstreamJob(narURL.Hash) { //nolint:nestif // deferred for a later refactoring.
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
	qtx database.Querier,
	hash string,
	touch bool,
) (*narinfo.NarInfo, *nar.URL, error) {
	nir, err := qtx.GetNarInfoByHash(ctx, hash)
	if err != nil {
		if database.IsNotFoundError(err) {
			return nil, nil, storage.ErrNotFound
		}

		return nil, nil, fmt.Errorf("error fetching the narinfo record from database: %w", err)
	}

	// If URL is not valid, it means this record hasn't been migrated yet
	// (it might have been created by an older version of ncps as a placeholder).
	if !nir.URL.Valid {
		return nil, nil, storage.ErrNotFound
	}

	ni := &narinfo.NarInfo{
		StorePath:   nir.StorePath.String,
		URL:         nir.URL.String,
		Compression: nir.Compression.String,
		FileSize:    uint64(nir.FileSize.Int64), //nolint:gosec
		NarSize:     uint64(nir.NarSize.Int64),  //nolint:gosec
		Deriver:     nir.Deriver.String,
		System:      nir.System.String,
		CA:          nir.Ca.String,
	}

	if ni.FileHash, err = parseValidHash(nir.FileHash, "file_hash"); err != nil {
		return nil, nil, err
	}

	if ni.NarHash, err = parseValidHash(nir.NarHash, "nar_hash"); err != nil {
		return nil, nil, err
	}

	// Fetch references
	ni.References, err = qtx.GetNarInfoReferences(ctx, nir.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("error fetching narinfo references: %w", err)
	}

	// Fetch signatures
	sigs, err := qtx.GetNarInfoSignatures(ctx, nir.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("error fetching narinfo signatures: %w", err)
	}

	for _, sigStr := range sigs {
		sig, err := signature.ParseSignature(sigStr)
		if err != nil {
			return nil, nil, fmt.Errorf("error parsing signature %q: %w", sigStr, err)
		}

		ni.Signatures = append(ni.Signatures, sig)
	}

	// Parse narURL for subsequent HasNar check
	parsedURL, err := nar.ParseURL(ni.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("error parsing nar URL %q: %w", ni.URL, err)
	}

	// Touch the record if needed
	if touch {
		if lat, err := nir.LastAccessedAt.Value(); err == nil && time.Since(lat.(time.Time)) > c.recordAgeIgnoreTouch {
			if _, err := qtx.TouchNarInfo(ctx, hash); err != nil {
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
			zerolog.Ctx(ctx).
				Error().
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

	err := c.withTransaction(ctx, "purgeNarInfo", func(qtx database.Querier) error {
		if _, err := qtx.DeleteNarInfoByHash(ctx, hash); err != nil {
			return fmt.Errorf("error deleting the narinfo record: %w", err)
		}

		if narURL.Hash != "" {
			if _, err := qtx.DeleteNarFileByHash(ctx, database.DeleteNarFileByHashParams{
				Hash:        narURL.Hash,
				Compression: narURL.Compression.String(),
				Query:       narURL.Query.Encode(),
			}); err != nil {
				return fmt.Errorf("error deleting the nar record: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if c.narInfoStore.HasNarInfo(ctx, hash) {
		if err := c.deleteNarInfoFromStore(ctx, hash); err != nil {
			return fmt.Errorf("error removing narinfo from store: %w", err)
		}
	}

	if narURL.Hash != "" {
		if c.hasNarInStore(ctx, *narURL) {
			if err := c.deleteNarFromStore(ctx, narURL); err != nil {
				return fmt.Errorf("error removing nar from store: %w", err)
			}
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

	return c.withTransaction(ctx, "storeInDatabase", func(qtx database.Querier) error {
		createNarInfoParams := database.CreateNarInfoParams{
			Hash:        hash,
			StorePath:   sql.NullString{String: narInfo.StorePath, Valid: narInfo.StorePath != ""},
			URL:         sql.NullString{String: narInfo.URL, Valid: narInfo.URL != ""},
			Compression: sql.NullString{String: narInfo.Compression, Valid: narInfo.Compression != ""},
			FileSize:    sql.NullInt64{Int64: int64(narInfo.FileSize), Valid: narInfo.FileSize != 0}, //nolint:gosec
			NarSize:     sql.NullInt64{Int64: int64(narInfo.NarSize), Valid: true},                   //nolint:gosec
			Deriver:     sql.NullString{String: narInfo.Deriver, Valid: narInfo.Deriver != ""},
			System:      sql.NullString{String: narInfo.System, Valid: narInfo.System != ""},
			Ca:          sql.NullString{String: narInfo.CA, Valid: narInfo.CA != ""},
		}

		if narInfo.FileHash != nil {
			createNarInfoParams.FileHash = sql.NullString{String: narInfo.FileHash.String(), Valid: true}
		}

		if narInfo.NarHash != nil {
			createNarInfoParams.NarHash = sql.NullString{String: narInfo.NarHash.String(), Valid: true}
		}

		nir, err := qtx.CreateNarInfo(ctx, createNarInfoParams)
		if err != nil {
			// Database-specific UPSERT behavior:
			//
			// PostgreSQL/SQLite: Use "ON CONFLICT ... DO UPDATE ... WHERE url IS NULL"
			//   - If hash exists with NULL URL → updates and returns the row
			//   - If hash exists with valid URL → condition fails, returns database.ErrNotFound
			//   - If hash doesn't exist → inserts and returns the row
			//
			// MySQL: Use "ON DUPLICATE KEY UPDATE ... IF(url IS NULL, VALUES(...), ...)"
			//   - Always executes UPDATE clause (even if condition is false)
			//   - Returns via LastInsertId() mechanism in wrapper
			//   - Never hits this database.ErrNotFound path
			//
			// In both cases, if a record exists with valid URL, we fetch it instead.
			if database.IsNotFoundError(err) {
				nir, err = qtx.GetNarInfoByHash(ctx, hash)
				if err != nil {
					return fmt.Errorf("upsert returned no rows (record exists with valid URL), failed to fetch: %w", err)
				}
			} else {
				return fmt.Errorf("error inserting the narinfo record for hash %q in the database: %w", hash, err)
			}
		}

		if len(narInfo.References) > 0 {
			if err := qtx.AddNarInfoReferences(ctx, database.AddNarInfoReferencesParams{
				NarInfoID: nir.ID,
				Reference: narInfo.References,
			}); err != nil {
				return fmt.Errorf("error inserting narinfo reference: %w", err)
			}
		}

		// Signatures
		sigStrings := make([]string, len(narInfo.Signatures))
		for i, sig := range narInfo.Signatures {
			sigStrings[i] = sig.String()
		}

		if len(sigStrings) > 0 {
			if err := qtx.AddNarInfoSignatures(ctx, database.AddNarInfoSignaturesParams{
				NarInfoID: nir.ID,
				Signature: sigStrings,
			}); err != nil {
				return fmt.Errorf("error inserting narinfo signature: %w", err)
			}
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

		// Check if nar_file already exists (multiple narinfos can share the same nar_file)

		narFileID, err := createOrUpdateNarFile(ctx, qtx, normalizedNarURL, narFileSize(narInfo))
		if err != nil {
			return err
		}

		// Link narinfo to nar_file
		if err := qtx.LinkNarInfoToNarFile(ctx, database.LinkNarInfoToNarFileParams{
			NarInfoID: nir.ID,
			NarFileID: narFileID,
		}); err != nil {
			return fmt.Errorf("error linking narinfo to nar_file: %w", err)
		}

		return nil
	})
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

	return c.withTransaction(ctx, "fixNarInfoFileSize", func(qtx database.Querier) error {
		return qtx.UpdateNarInfoFileSize(ctx, database.UpdateNarInfoFileSizeParams{
			Hash:     hash,
			FileSize: sql.NullInt64{Int64: correctSize, Valid: true},
		})
	})
}

// getNarActualSize returns the actual size of a NAR without triggering a
// streaming pipeline. It avoids calling c.GetNar() which would create an
// io.Pipe with a background goroutine, causing a spurious "pipe closed" error
// when the reader is closed before the goroutine finishes.
//
// Returns -1 if the size cannot be determined (NAR not found or not yet available).
func (c *Cache) getNarActualSize(ctx context.Context, nu nar.URL) (int64, error) {
	narFileRow, err := c.getNarFileFromDB(ctx, c.db, nu)
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
func (c *Cache) checkAndFixNarInfo(ctx context.Context, hash string) error {
	// First check if we have the NarInfo in DB using direct DB access
	// to avoid higher-level cache logic (like purging or storage checks)
	niRow, err := c.db.GetNarInfoByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		return fmt.Errorf("failed to get narinfo from db: %w", err)
	}

	if !niRow.URL.Valid {
		// No URL means not migrated or partial, can't check
		return nil
	}

	nu, err := nar.ParseURL(niRow.URL.String)
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
	hasNarInStore := c.hasNarInStore(ctx, nu)

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

	if size != niRow.FileSize.Int64 {
		zerolog.Ctx(ctx).
			Info().
			Int64("current_size", niRow.FileSize.Int64).
			Int64("actual_size", size).
			Msg("mismatch detected, fixing narinfo file size")

		return c.fixNarInfoFileSize(ctx, hash, size)
	}

	return nil
}

func (c *Cache) checkAndFixNarInfoNoCompression(ctx context.Context, hash string, niRow database.NarInfo) error {
	// For compression=none, FileSize must be 0. If it's not, fix it.
	if niRow.FileSize.Valid && niRow.FileSize.Int64 != 0 {
		zerolog.Ctx(ctx).
			Info().
			Int64("current_size", niRow.FileSize.Int64).
			Msg("mismatch detected for compression=none, fixing narinfo file size to NULL")

		if err := c.withTransaction(ctx, "fixNarInfoFileSizeToNull", func(qtx database.Querier) error {
			return qtx.UpdateNarInfoFileSize(ctx, database.UpdateNarInfoFileSizeParams{
				Hash:     hash,
				FileSize: sql.NullInt64{Int64: 0, Valid: false},
			})
		}); err != nil {
			return fmt.Errorf("failed to fix narinfo file size to NULL: %w", err)
		}
	}

	// For compression=none, FileHash must be NULL. If it's not, fix it.
	if niRow.FileHash.Valid || niRow.FileHash.String != "" {
		zerolog.Ctx(ctx).
			Info().
			Str("current_file_hash", niRow.FileHash.String).
			Msg("mismatch detected for compression=none, fixing narinfo file hash to NULL")

		if err := c.withTransaction(ctx, "fixNarInfoFileHashToNull", func(qtx database.Querier) error {
			return qtx.UpdateNarInfoFileHash(ctx, database.UpdateNarInfoFileHashParams{
				Hash:     hash,
				FileHash: sql.NullString{String: "", Valid: false},
			})
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
	hashes, err := c.db.GetNarInfoHashesByURL(ctx, sql.NullString{String: narURL.String(), Valid: true})
	if err != nil {
		return fmt.Errorf("failed to get narinfo hashes by url: %w", err)
	}

	var errs []error

	for _, hash := range hashes {
		if err := c.checkAndFixNarInfo(ctx, hash); err != nil {
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

func createOrUpdateNarFile(
	ctx context.Context,
	qtx database.Querier,
	narURL nar.URL,
	fileSize uint64,
) (int64, error) {
	// Create (or update existing) nar_file record.
	// The query uses ON CONFLICT DO UPDATE (or ON DUPLICATE KEY UPDATE), so duplicates are handled
	// by updating the timestamp and returning the record.
	newNarFile, err := qtx.CreateNarFile(ctx, database.CreateNarFileParams{
		Hash:        narURL.Hash,
		Compression: narURL.Compression.String(),
		Query:       narURL.Query.Encode(),
		FileSize:    fileSize,
	})
	if err != nil {
		return 0, fmt.Errorf("error creating or updating nar_file record in the database: %w", err)
	}

	return newNarFile.ID, nil
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

	return MigrateNarInfo(ctx, c.downloadLocker, c.db, narInfoStore, hash, ni)
}

// MigrateNarInfo migrates a single narinfo from storage to the database.
// It uses distributed locking to coordinate with other instances (if a distributed locker is provided).
// This function is used both by Cache.MigrateNarInfoToDatabase and the CLI migrate-narinfo command.
//
// Parameters:
//   - ctx: Context for the operation
//   - locker: Distributed locker for coordination (can be in-memory for single-instance)
//   - db: Database querier for storing the narinfo
//   - narInfoStore: Optional storage backend to delete from after migration (nil to skip deletion)
//   - hash: The narinfo hash to migrate
//   - ni: The parsed narinfo to migrate
//
// Returns an error if migration fails. Returns nil if the narinfo is already migrated or
// if another instance is currently migrating it.
func MigrateNarInfo(
	ctx context.Context,
	locker lock.Locker,
	db database.Querier,
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
	nir, err := db.GetNarInfoByHash(ctx, hash)
	if err == nil && nir.URL.Valid {
		zerolog.Ctx(ctx).Debug().
			Str("narinfo_hash", hash).
			Msg("migration completed by another instance while waiting for lock")

		return nil
	}

	log := zerolog.Ctx(ctx).With().Str("narinfo_hash", hash).Logger()

	log.Info().Msg("migrating narinfo to database")

	opStartTime := time.Now()

	migrateAttrs := []attribute.KeyValue{
		attribute.String("migration_type", migrationTypeNarInfoToDB),
		attribute.String("operation", migrationOperationMigrate),
	}

	// Store narinfo in database using the UPSERT logic from storeInDatabase
	err = storeNarInfoInDatabase(ctx, db, hash, ni)
	if err != nil {
		log.Error().Err(err).Msg("failed to migrate narinfo to database")

		backgroundMigrationObjectsTotal.Add(ctx, 1,
			metric.WithAttributes(
				append(migrateAttrs, attribute.String("result", migrationResultFailure))...,
			),
		)
		backgroundMigrationDuration.Record(ctx, time.Since(opStartTime).Seconds(),
			metric.WithAttributes(migrateAttrs...),
		)

		return fmt.Errorf("failed to store narinfo in database: %w", err)
	}

	log.Debug().Dur("duration", time.Since(opStartTime)).Msg("successfully migrated narinfo to database")

	backgroundMigrationObjectsTotal.Add(ctx, 1,
		metric.WithAttributes(
			append(migrateAttrs, attribute.String("result", migrationResultSuccess))...,
		),
	)
	backgroundMigrationDuration.Record(ctx, time.Since(opStartTime).Seconds(),
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
			backgroundMigrationObjectsTotal.Add(ctx, 1,
				metric.WithAttributes(
					append(deleteAttrs, attribute.String("result", migrationResultFailure))...,
				),
			)
			// Don't return error - migration succeeded, only cleanup failed
		} else {
			log.Debug().Msg("deleted narinfo from storage after successful migration")
			backgroundMigrationObjectsTotal.Add(ctx, 1,
				metric.WithAttributes(
					append(deleteAttrs, attribute.String("result", migrationResultSuccess))...,
				),
			)
		}

		backgroundMigrationDuration.Record(ctx, time.Since(deleteStartTime).Seconds(),
			metric.WithAttributes(deleteAttrs...),
		)
	}

	return nil
}

// storeNarInfoInDatabase is extracted from Cache.storeInDatabase for use by migration.
// It contains the core UPSERT logic without Cache dependencies.
func storeNarInfoInDatabase(ctx context.Context, db database.Querier, hash string, narInfo *narinfo.NarInfo) error {
	tx, err := db.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil {
			if !errors.Is(err, sql.ErrTxDone) {
				zerolog.Ctx(ctx).Error().Err(err).Msg("error rolling back transaction")
			}
		}
	}()

	qtx := db.WithTx(tx)

	createNarInfoParams := database.CreateNarInfoParams{
		Hash:        hash,
		StorePath:   sql.NullString{String: narInfo.StorePath, Valid: narInfo.StorePath != ""},
		URL:         sql.NullString{String: narInfo.URL, Valid: narInfo.URL != ""},
		Compression: sql.NullString{String: narInfo.Compression, Valid: narInfo.Compression != ""},
		FileSize:    sql.NullInt64{Int64: int64(narInfo.FileSize), Valid: true}, //nolint:gosec
		NarSize:     sql.NullInt64{Int64: int64(narInfo.NarSize), Valid: true},  //nolint:gosec
		Deriver:     sql.NullString{String: narInfo.Deriver, Valid: narInfo.Deriver != ""},
		System:      sql.NullString{String: narInfo.System, Valid: narInfo.System != ""},
		Ca:          sql.NullString{String: narInfo.CA, Valid: narInfo.CA != ""},
	}

	if narInfo.FileHash != nil {
		createNarInfoParams.FileHash = sql.NullString{String: narInfo.FileHash.String(), Valid: true}
	}

	if narInfo.NarHash != nil {
		createNarInfoParams.NarHash = sql.NullString{String: narInfo.NarHash.String(), Valid: true}
	}

	nir, err := qtx.CreateNarInfo(ctx, createNarInfoParams)
	if err != nil {
		// Handle UPSERT behavior (see Cache.storeInDatabase for full comments)
		if database.IsNotFoundError(err) {
			nir, err = qtx.GetNarInfoByHash(ctx, hash)
			if err != nil {
				return fmt.Errorf("upsert returned no rows (record exists with valid URL), failed to fetch: %w", err)
			}
		} else {
			return fmt.Errorf("error inserting the narinfo record for hash %q in the database: %w", hash, err)
		}
	}

	if len(narInfo.References) > 0 {
		if err := qtx.AddNarInfoReferences(ctx, database.AddNarInfoReferencesParams{
			NarInfoID: nir.ID,
			Reference: narInfo.References,
		}); err != nil {
			// Duplicate key errors are expected with UPSERT
			if !database.IsDuplicateKeyError(err) {
				return fmt.Errorf("error inserting narinfo reference: %w", err)
			}
		}
	}

	// Signatures
	sigStrings := make([]string, len(narInfo.Signatures))
	for i, sig := range narInfo.Signatures {
		sigStrings[i] = sig.String()
	}

	if len(sigStrings) > 0 {
		if err := qtx.AddNarInfoSignatures(ctx, database.AddNarInfoSignaturesParams{
			NarInfoID: nir.ID,
			Signature: sigStrings,
		}); err != nil {
			// Duplicate key errors are expected with UPSERT
			if !database.IsDuplicateKeyError(err) {
				return fmt.Errorf("error inserting narinfo signature: %w", err)
			}
		}
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

	// Create or get nar_file record
	narFileID, err := createOrUpdateNarFile(ctx, qtx, normalizedNarURL, narFileSize(narInfo))
	if err != nil {
		return err
	}

	// Link narinfo to nar_file
	if err := qtx.LinkNarInfoToNarFile(ctx, database.LinkNarInfoToNarFileParams{
		NarInfoID: nir.ID,
		NarFileID: narFileID,
	}); err != nil {
		// Duplicate key errors are expected with UPSERT
		if !database.IsDuplicateKeyError(err) {
			return fmt.Errorf("error linking narinfo to nar_file: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %w", err)
	}

	return nil
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

	_, err := c.db.GetNarInfoByHash(ctx, hash)
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
		if _, err := c.db.DeleteNarInfoByHash(ctx, hash); err != nil {
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
			Msg("failed to acquire download lock, will poll storage for completion by another server")

		// Lock acquisition failed, likely because another server is downloading.
		// Poll storage periodically to check if the download completes.
		// This handles distributed coordination where servers don't share the upstreamJobs map.
		const pollInterval = 200 * time.Millisecond

		pollTimeout := c.downloadPollTimeout

		pollCtx, cancel := context.WithTimeout(coordCtx, pollTimeout)
		defer cancel()

		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if hasAsset(pollCtx) {
					zerolog.Ctx(ctx).Debug().
						Str("hash", hash).
						Msg("asset appeared in storage while polling (downloaded by another server)")

					// Return a completed downloadState
					ds := newDownloadState()
					ds.closed = true
					ds.startOnce.Do(func() { close(ds.start) })
					ds.storedOnce.Do(func() { close(ds.stored) })
					ds.doneOnce.Do(func() { close(ds.done) })

					return ds
				}
			case <-pollCtx.Done():
				// Polling timeout or context canceled
				zerolog.Ctx(ctx).Error().
					Err(pollCtx.Err()).
					Str("hash", hash).
					Str("lock_key", lockKey).
					Dur("poll_timeout", pollTimeout).
					Msg("timeout waiting for download by another server")

				ds := newDownloadState()
				ds.downloadError = fmt.Errorf("failed to acquire download lock and timeout polling for completion: %w", err)
				// Signal that the download is done (with error) to prevent deadlocks
				ds.startOnce.Do(func() { close(ds.start) })
				ds.storedOnce.Do(func() { close(ds.stored) })
				ds.doneOnce.Do(func() { close(ds.done) })

				return ds
			}
		}
	}

	// Double check local jobs and asset presence under lock
	if hasAsset(ctx) {
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

// withTransaction executes fn within a database transaction.
// It automatically handles transaction lifecycle: BeginTx, Rollback (on error or panic), and Commit.
func (c *Cache) withTransaction(ctx context.Context, operation string, fn func(qtx database.Querier) error) error {
	const (
		maxAttempts  = 5
		initialDelay = 50 * time.Millisecond
	)

	var (
		err   error
		delay = initialDelay
	)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err = c.executeTransaction(ctx, operation, fn)
		if err == nil {
			return nil
		}

		// Only retry on deadlock/busy errors
		if !database.IsDeadlockError(err) {
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
			Msg("database deadlock/busy, retrying transaction")

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			delay *= 2
		}
	}

	return fmt.Errorf("transaction for %s failed after %d attempts: %w", operation, maxAttempts, err)
}

func (c *Cache) executeTransaction(ctx context.Context, operation string, fn func(qtx database.Querier) error) error {
	tx, err := c.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning a transaction for %s: %w", operation, err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil {
			if !errors.Is(err, sql.ErrTxDone) {
				zerolog.Ctx(ctx).
					Error().
					Err(err).
					Str("operation", operation).
					Msg("error rolling back the transaction")
			}
		}
	}()

	if err := fn(c.db.WithTx(tx)); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing the transaction for %s: %w", operation, err)
	}

	return nil
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
func (c *Cache) calculateCleanupSize(ctx context.Context, qtx database.Querier, log zerolog.Logger) (uint64, error) {
	narTotalSize, err := qtx.GetNarTotalSize(ctx)
	if err != nil {
		log.Error().Err(err).Msg("error fetching the total nar size")

		return 0, err
	}

	if narTotalSize == 0 {
		log.Info().Msg("SUM(file_size) is zero, nothing to clean up")

		return 0, nil
	}

	log = log.With().Int64("nar_total_size", narTotalSize).Logger()

	//nolint:gosec
	if uint64(narTotalSize) <= c.maxSize {
		log.Info().Msg("store size is less than max-size, not removing any nars")

		return 0, nil
	}

	//nolint:gosec
	cleanupSize := uint64(narTotalSize) - c.maxSize

	log = log.With().Uint64("cleanup_size", cleanupSize).Logger()
	log.Info().Msg("going to remove nars")

	return cleanupSize, nil
}

// deleteLRURecordsFromDB identifies the least used NarInfos, deletes them,
// and then cleans up any NarFiles that became orphaned as a result.
func (c *Cache) deleteLRURecordsFromDB(
	ctx context.Context,
	qtx database.Querier,
	log zerolog.Logger,
	cleanupSize uint64,
) ([]string, []nar.URL, []string, error) {
	// 1. METADATA PHASE
	// Find the NarInfos that constitute the oldest `cleanupSize` worth of data.
	// We use the query you provided in the first prompt.
	narInfosToDelete, err := qtx.GetLeastUsedNarInfos(ctx, cleanupSize)
	if err != nil {
		log.Error().Err(err).Msg("error getting least used narinfos")

		return nil, nil, nil, err
	}

	if len(narInfosToDelete) == 0 {
		log.Warn().Msg("cleanup required but no reclaimable narinfos found")

		return nil, nil, nil, nil
	}

	log.Info().Int("count", len(narInfosToDelete)).Msg("found narinfos to expire")

	// Track hashes to remove from the in-memory/disk store later
	narInfoHashesToRemove := make([]string, 0, len(narInfosToDelete))

	// Delete the NarInfos from the database.
	// This breaks the link between the Metadata and the Storage.
	for _, info := range narInfosToDelete {
		narInfoHashesToRemove = append(narInfoHashesToRemove, info.Hash)

		// We delete by ID since we have the full object from the previous query
		if _, err := qtx.DeleteNarInfoByID(ctx, info.ID); err != nil {
			log.Error().
				Err(err).
				Str("hash", info.Hash).
				Msg("error deleting narinfo record")

			return nil, nil, nil, err
		}
	}

	// 2. STORAGE PHASE
	// Now that metadata is gone, some files might have zero references.
	// We find those truly orphaned files.
	orphanedNarFiles, err := qtx.GetOrphanedNarFiles(ctx)
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
		if _, err := qtx.DeleteOrphanedNarFiles(ctx); err != nil {
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

	orphanedChunks, err := qtx.GetOrphanedChunks(ctx)
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
	if _, err := qtx.DeleteOrphanedChunks(ctx); err != nil {
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

			tx, err := c.db.DB().BeginTx(ctx, nil)
			if err != nil {
				log.Error().Err(err).Msg("error beginning a transaction")

				return err
			}

			defer func() {
				if err := tx.Rollback(); err != nil {
					if !errors.Is(err, sql.ErrTxDone) {
						log.Error().Err(err).Msg("error rolling back the transaction")
					}
				}
			}()

			qtx := c.db.WithTx(tx)

			cleanupSize, err := c.calculateCleanupSize(ctx, qtx, log)
			if err != nil || cleanupSize == 0 {
				return err
			}

			narInfoHashesToRemove, narURLsToRemove, chunkHashesToRemove, err := c.deleteLRURecordsFromDB(
				ctx,
				qtx,
				log,
				cleanupSize,
			)
			if err != nil || (len(narInfoHashesToRemove) == 0 && len(chunkHashesToRemove) == 0) {
				return err
			}

			// Track eviction counts
			lruNarInfosEvictedTotal.Add(ctx, int64(len(narInfoHashesToRemove)))
			lruNarFilesEvictedTotal.Add(ctx, int64(len(narURLsToRemove)))
			lruChunksEvictedTotal.Add(ctx, int64(len(chunkHashesToRemove)))

			// Track bytes freed (approximate as cleanupSize)
			//nolint:gosec // G115: Cleanup size is bounded by cache max size, unlikely to exceed int64 max
			lruBytesFreedTotal.Add(ctx, int64(cleanupSize))

			// Commit the database transaction before deleting from stores
			if err := tx.Commit(); err != nil {
				log.Error().Err(err).Msg("error committing the transaction")

				return err
			}

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
		case uc := <-ch:
			cancel()

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

func parseValidHash(hash sql.NullString, fieldName string) (*nixhash.HashWithEncoding, error) {
	if !hash.Valid {
		//nolint:nilnil
		return nil, nil
	}

	h, err := nixhash.ParseAny(hash.String, nil)
	if err != nil {
		return nil, fmt.Errorf("error parsing %s: %w", fieldName, err)
	}

	return h, nil
}

// HasNarInChunks returns true if the NAR is already in chunks and chunking is complete.
func (c *Cache) HasNarInChunks(ctx context.Context, narURL nar.URL) (bool, error) {
	if !c.isCDCEnabled() {
		return false, nil
	}

	nr, err := c.getNarFileFromDB(ctx, c.db, narURL)
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
	_, err := c.getNarFileFromDB(ctx, c.db, narURL)
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

	err := c.withTransaction(ctx, "getNarFromChunks.init", func(qtx database.Querier) error {
		nr, err := c.getNarFileFromDB(ctx, qtx, *narURL)
		if err != nil {
			return err
		}

		narFileID = nr.ID
		//nolint:gosec // G115: File size is non-negative
		totalSize = int64(nr.FileSize)
		totalChunks = nr.TotalChunks

		// Touch the NAR file
		latValue, err := nr.LastAccessedAt.Value()
		if err == nil {
			if lat, ok := latValue.(time.Time); ok && time.Since(lat) > c.recordAgeIgnoreTouch {
				if _, err := qtx.TouchNarFile(ctx, database.TouchNarFileParams{
					Hash:        narURL.Hash,
					Compression: narURL.Compression.String(),
					Query:       narURL.Query.Encode(),
				}); err != nil {
					return fmt.Errorf("error touching the nar record: %w", err)
				}
			}
		}

		return nil
	})
	if err != nil {
		return 0, nil, err
	}

	pr, pw := io.Pipe()

	analytics.SafeGo(ctx, func() {
		defer pw.Close()

		var streamErr error

		if totalChunks > 0 {
			// Fast path: All chunks complete
			streamErr = c.streamCompleteChunks(ctx, pw, narFileID, totalChunks)
		} else {
			// Progressive path: Stream as chunks appear
			streamErr = c.streamProgressiveChunks(ctx, pw, narFileID)
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
func (c *Cache) streamCompleteChunks(ctx context.Context, w io.Writer, narFileID int64, totalChunks int64) error {
	// Get all chunks at once
	chunkHashes := make([]string, 0, totalChunks)

	chunks, err := c.db.GetChunksByNarFileID(ctx, narFileID)
	if err != nil {
		return fmt.Errorf("error getting chunks: %w", err)
	}

	for _, ch := range chunks {
		chunkHashes = append(chunkHashes, ch.Hash)
	}

	if len(chunkHashes) != int(totalChunks) {
		return fmt.Errorf("expected %d chunks but got %d: %w", totalChunks, len(chunkHashes), storage.ErrNotFound)
	}

	// Use prefetch pipeline to overlap I/O operations
	return c.streamChunksWithPrefetch(ctx, w, chunkHashes)
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
func (c *Cache) streamChunksWithPrefetch(ctx context.Context, w io.Writer, chunkHashes []string) error {
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
			rc, err := c.getChunkStore().GetChunk(ctx, hash)

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

// streamProgressiveChunks streams chunks as they become available during an in-progress chunking operation.
// This allows concurrent downloads while another instance is still chunking the NAR.
// It uses a prefetch pipeline to overlap chunk fetching with data copying for better performance.
func (c *Cache) streamProgressiveChunks(ctx context.Context, w io.Writer, narFileID int64) error {
	pollInterval := 200 * time.Millisecond
	maxWaitPerChunk := 30 * time.Second

	// Buffer size of 2 allows one chunk to be copied while the next is being fetched
	chunkChan := make(chan *prefetchedChunk, 2)

	// Start prefetch goroutine that polls for chunks and fetches them
	analytics.SafeGo(ctx, func() {
		defer close(chunkChan)

		chunkIndex := int64(0)

		var totalChunks int64

		for {
			// Check if context is cancelled before polling
			select {
			case <-ctx.Done():
				// Send context error and stop prefetching
				chunkChan <- &prefetchedChunk{err: ctx.Err()}

				return
			default:
			}

			// Try to get chunk at current index
			var chunkHash string

			chunkWaitStart := time.Now()

			// Poll for chunk availability
			for {
				chunk, err := c.db.GetChunkByNarFileIDAndIndex(ctx, database.GetChunkByNarFileIDAndIndexParams{
					NarFileID:  narFileID,
					ChunkIndex: chunkIndex,
				})
				if err == nil {
					chunkHash = chunk.Hash

					break // Got the chunk, proceed to fetch it
				} else if !database.IsNotFoundError(err) {
					// Database error
					chunkChan <- &prefetchedChunk{err: fmt.Errorf("error querying chunk %d: %w", chunkIndex, err)}

					return
				}

				// Only query NarFile if we don't know total chunks yet
				if totalChunks == 0 {
					nr, err := c.db.GetNarFileByID(ctx, narFileID)
					if err != nil {
						chunkChan <- &prefetchedChunk{err: fmt.Errorf("error querying nar file: %w", err)}

						return
					}

					totalChunks = nr.TotalChunks
				}

				// Check if we're done
				if totalChunks > 0 && chunkIndex >= totalChunks {
					return // All chunks processed
				}

				// Check timeout
				if time.Since(chunkWaitStart) > maxWaitPerChunk {
					chunkChan <- &prefetchedChunk{
						err: fmt.Errorf("timeout waiting for chunk %d after %v: %w",
							chunkIndex, maxWaitPerChunk, context.DeadlineExceeded),
					}

					return
				}

				// Wait and retry
				select {
				case <-time.After(pollInterval):
					// Continue polling
				case <-ctx.Done():
					chunkChan <- &prefetchedChunk{err: ctx.Err()}

					return
				}
			}

			// Fetch the chunk
			rc, err := c.getChunkStore().GetChunk(ctx, chunkHash)

			// Send chunk or error to consumer
			select {
			case chunkChan <- &prefetchedChunk{reader: rc, hash: chunkHash, err: err}:
			case <-ctx.Done():
				// Context cancelled while sending, close the reader if we got one
				if rc != nil {
					rc.Close()
				}

				return
			}

			chunkIndex++

			// After successfully fetching a chunk, check if we're done
			if totalChunks > 0 && chunkIndex >= totalChunks {
				return // All chunks fetched
			}
		}
	})

	// Stream chunks as they arrive from the prefetch pipeline
	for chunk := range chunkChan {
		if chunk.err != nil {
			return chunk.err
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

		return nil
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
func (c *Cache) migrateNarToChunksCleanup(ctx context.Context, originalNarURL nar.URL) {
	newNarURL := nar.URL{
		Hash:        originalNarURL.Hash,
		Compression: nar.CompressionTypeNone,
		Query:       originalNarURL.Query,
	}

	originalURL := originalNarURL.String()
	newURL := newNarURL.String()

	if _, err := c.db.UpdateNarInfoCompressionFileSizeHashAndURL(
		ctx,
		database.UpdateNarInfoCompressionFileSizeHashAndURLParams{
			Compression: sql.NullString{String: nar.CompressionTypeNone.String(), Valid: true},
			NewUrl:      sql.NullString{String: newURL, Valid: true},
			OldUrl:      sql.NullString{String: originalURL, Valid: true},
			FileSize:    sql.NullInt64{Valid: false},
			FileHash:    sql.NullString{Valid: false},
		},
	); err != nil {
		zerolog.Ctx(ctx).Warn().
			Err(err).
			Str("old_url", originalURL).
			Str("new_url", newURL).
			Msg("failed to update narinfo compression/URL after CDC migration")
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

	analytics.SafeGo(ctx, func() {
		log := zerolog.Ctx(ctx).With().
			Str("op", "BackgroundMigrateNarToChunks").
			Str("nar_hash", narURL.Hash).
			Logger()

		log.Debug().Msg("starting background migration to chunks")

		opStartTime := time.Now()

		err := c.MigrateNarToChunks(ctx, &narURL)

		backgroundMigrationDuration.Record(ctx, time.Since(opStartTime).Seconds(),
			metric.WithAttributes(
				attribute.String("migration_type", migrationTypeNarToChunks),
				attribute.String("operation", migrationOperationMigrate),
			),
		)

		if err != nil {
			// if the nar is already chunked, we don't need to do anything else.
			if errors.Is(err, ErrNarAlreadyChunked) {
				log.Debug().Msg("skipping background migration to chunks, nar already chunked")

				backgroundMigrationObjectsTotal.Add(ctx, 1,
					metric.WithAttributes(
						attribute.String("migration_type", migrationTypeNarToChunks),
						attribute.String("operation", migrationOperationMigrate),
						attribute.String("result", migrationResultSkipped),
					),
				)

				return
			}

			log.Error().Err(err).Msg("error migrating nar to chunks")
			backgroundMigrationObjectsTotal.Add(ctx, 1,
				metric.WithAttributes(
					attribute.String("migration_type", migrationTypeNarToChunks),
					attribute.String("operation", migrationOperationMigrate),
					attribute.String("result", migrationResultFailure),
				),
			)

			return
		}

		backgroundMigrationObjectsTotal.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("migration_type", migrationTypeNarToChunks),
				attribute.String("operation", migrationOperationMigrate),
				attribute.String("result", migrationResultSuccess),
			),
		)
		log.Info().Msg("successfully migrated nar to chunks")
	})
}
