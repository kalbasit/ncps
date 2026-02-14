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
	"github.com/kalbasit/ncps/pkg/config"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
)

const (
	recordAgeIgnoreTouch = 5 * time.Minute
	otelPackageName      = "github.com/kalbasit/ncps/pkg/cache"
	cacheLockKey         = "cache"

	// Migration operation constants for metrics.
	migrationOperationMigrate = "migrate"
	migrationOperationDelete  = "delete"

	// Migration result constants for metrics.
	migrationResultSuccess = "success"
	migrationResultFailure = "failure"

	// Migration type constants for metrics.
	migrationTypeNarInfoToDB = "narinfo-to-db"
)

// narInfoJobKey returns the key used for tracking narinfo download jobs.
func narInfoJobKey(hash string) string { return "download:narinfo:" + hash }

// narJobKey returns the key used for tracking NAR download jobs.
func narJobKey(hash string) string { return "download:nar:" + hash }

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

	// ErrInconsistentState is returned when the database is in an inconsistent state (e.g., nar exists without narinfo).
	ErrInconsistentState = errors.New("inconsistent database state")

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
	baseContext context.Context

	hostName       string
	secretKey      signature.SecretKey
	upstreamCaches []*upstream.Cache
	healthChecker  *healthcheck.HealthChecker
	maxSize        uint64
	db             database.Querier

	// tempDir is used to store nar files temporarily.
	tempDir string
	// stores
	config *config.Config
	//nolint:staticcheck // deprecated: migration support
	configStore  storage.ConfigStore
	narInfoStore storage.NarInfoStore
	narStore     storage.NarStore

	// Should the cache sign the narinfos?
	shouldSignNarinfo bool

	// recordAgeIgnoreTouch represents the duration at which a record is
	// considered up to date and a touch is not invoked. This helps avoid
	// repetitive touching of records in the database which are causing `database
	// is locked` errors
	recordAgeIgnoreTouch time.Duration

	// Lock abstraction (can be local or distributed)
	downloadLocker  lock.Locker
	cacheLocker     lock.RWLocker
	downloadLockTTL time.Duration
	cacheLockTTL    time.Duration

	// upstreamJobs is used to store in-progress jobs for pulling nars from
	// upstream cache so incoming requests for the same nar can find and wait
	// for jobs. Protected by upstreamJobsMu for local synchronization.
	upstreamJobsMu sync.Mutex
	upstreamJobs   map[string]*downloadState

	cron *cron.Cron

	// Wait group to track background operations
	backgroundWG sync.WaitGroup
}

type downloadState struct {
	// Mutex and Condition is used to gate access to this downloadState as well as broadcast chunks
	mu   sync.Mutex
	cond *sync.Cond

	// Information about the asset being downloaded
	wg           sync.WaitGroup // Tracks active readers streaming from the temp file
	cleanupWg    sync.WaitGroup // Tracks download completion to trigger cleanup
	closed       bool           // Indicates whether new readers are allowed (protected by mu)
	assetPath    string
	bytesWritten int64
	finalSize    int64

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
	cacheLockTTL time.Duration,
) (*Cache, error) {
	c := &Cache{
		baseContext:          ctx,
		db:                   db,
		config:               config.New(db, cacheLocker),
		configStore:          configStore,
		narInfoStore:         narInfoStore,
		narStore:             narStore,
		shouldSignNarinfo:    true,
		downloadLocker:       downloadLocker,
		cacheLocker:          cacheLocker,
		downloadLockTTL:      downloadLockTTL,
		cacheLockTTL:         cacheLockTTL,
		upstreamJobs:         make(map[string]*downloadState),
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
	c.healthChecker.Start(c.baseContext)

	// Start the health change processor
	analytics.SafeGo(ctx, func() {
		c.processHealthChanges(ctx, healthChangeCh)
	})

	return c, nil
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

	c.upstreamCaches = append(c.upstreamCaches, ucs...)
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
		// Observe Total
		o.ObserveInt64(totalGauge, int64(len(c.upstreamCaches)))

		// Observe Healthy
		o.ObserveInt64(healthyGauge, int64(c.GetHealthyUpstreamCount()))

		return nil
	}, totalGauge, healthyGauge)

	return err
}

// GetHealthyUpstreamCount returns the number of healthy upstream caches.
func (c *Cache) GetHealthyUpstreamCount() int {
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

	err := c.withReadLock(ctx, "GetNar", func() error {
		ctx = narURL.
			NewLogger(*zerolog.Ctx(ctx)).
			WithContext(ctx)

		if c.narStore.HasNar(ctx, narURL) {
			metricAttrs = append(metricAttrs,
				attribute.String("result", "hit"),
			)

			var err error

			size, reader, err = c.getNarFromStore(ctx, &narURL)
			if err != nil {
				metricAttrs = append(metricAttrs, attribute.String("status", "error"))
			} else {
				metricAttrs = append(metricAttrs, attribute.String("status", "success"))
			}

			return err
		}

		zerolog.Ctx(ctx).
			Debug().
			Msg("pulling nar in a go-routine and will stream the file back to the client")

		// Look up the original NAR URL from the narinfo in the database.
		// The client requests the normalized hash, but the upstream may require
		// the original (prefixed) hash (e.g., nix-serve style upstreams).
		narURL = c.lookupOriginalNarURL(ctx, narURL)

		// create a detachedCtx that has the same span and logger as the main
		// context but with the baseContext as parent; This context will not cancel
		// when ctx is canceled allowing us to continue pulling the nar in the
		// background.
		detachedCtx := trace.ContextWithSpan(
			zerolog.Ctx(ctx).WithContext(c.baseContext),
			trace.SpanFromContext(ctx),
		)
		ds := c.prePullNar(ctx, detachedCtx, &narURL, nil, nil, false)

		// Check if download is complete (closed=true) before adding to WaitGroup
		// This prevents race with cleanup goroutine calling ds.wg.Wait()
		ds.mu.Lock()

		canStream := !ds.closed
		if canStream {
			ds.wg.Add(1)
		}

		ds.mu.Unlock()

		// If download is complete or NAR is in store, get from storage
		if !canStream || c.narStore.HasNar(ctx, narURL) {
			if canStream {
				ds.wg.Done()
			}

			metricAttrs = append(metricAttrs,
				attribute.String("result", "hit"),
				attribute.String("status", "success"),
			)

			var err error

			size, reader, err = c.getNarFromStore(ctx, &narURL)
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

		err := ds.getError()
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

	return c.withReadLock(ctx, "PutNar", func() error {
		ctx = narURL.
			NewLogger(*zerolog.Ctx(ctx)).
			WithContext(ctx)

		defer func() {
			//nolint:errcheck
			io.Copy(io.Discard, r)

			r.Close()
		}()

		_, err := c.narStore.PutNar(ctx, narURL, r)
		if errors.Is(err, storage.ErrAlreadyExists) {
			// Already exists is not an error for PUT - return success
			zerolog.Ctx(ctx).Debug().Msg("nar already exists in storage, skipping")

			return nil
		}

		return err
	})
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

	return c.withReadLock(ctx, "DeleteNar", func() error {
		ctx = narURL.
			NewLogger(*zerolog.Ctx(ctx)).
			WithContext(ctx)

		return c.narStore.DeleteNar(ctx, narURL)
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
func (c *Cache) storeNarFromTempFile(ctx context.Context, tempPath string, narURL *nar.URL) (int64, error) {
	f, err := os.Open(tempPath)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error opening the nar from the temporary file")

		return 0, err
	}

	defer f.Close()

	written, err := c.narStore.PutNar(ctx, *narURL, f)
	if err != nil {
		if errors.Is(err, storage.ErrAlreadyExists) {
			// Already exists is not an error - another request stored it first
			zerolog.Ctx(ctx).Debug().Msg("nar already exists in storage, skipping")

			return 0, nil
		}

		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error storing the nar in the store")

		return 0, err
	}

	return written, nil
}

func (c *Cache) pullNarIntoStore(
	ctx context.Context,
	narURL *nar.URL,
	uc *upstream.Cache,
	narInfo *narinfo.NarInfo,
	enableZSTD bool,
	ds *downloadState,
) {
	// Track download completion for cleanup synchronization
	ds.cleanupWg.Add(1)
	defer ds.cleanupWg.Done()

	defer func() {
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

	zerolog.Ctx(ctx).
		Info().
		Msg("downloading the nar from upstream")

	resp, err := c.getNarFromUpstream(ctx, narURL, uc, narInfo, enableZSTD)
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

		// Mark as closed to prevent new readers from adding to WaitGroup
		ds.mu.Lock()
		ds.closed = true
		ds.mu.Unlock()

		ds.wg.Wait() // Then wait for all readers to finish
		os.Remove(ds.assetPath)
	})

	// Signal that temp file is ready for streaming
	ds.startOnce.Do(func() { close(ds.start) })

	err = c.streamResponseToFile(ctx, resp, f, ds)
	if err != nil {
		ds.setError(err)

		return
	}

	written, err := c.storeNarFromTempFile(ctx, ds.assetPath, narURL)
	if err != nil {
		ds.setError(err)

		return
	}

	// Signal that the asset is now in final storage and the distributed lock can be released
	// This prevents the race condition where other instances check hasAsset() before storage completes
	ds.storedOnce.Do(func() { close(ds.stored) })

	if enableZSTD && written > 0 {
		narInfo.FileSize = uint64(written)
	}

	zerolog.Ctx(ctx).
		Info().
		Dur("elapsed", time.Since(now)).
		Msg("download of nar complete")
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

	size, r, err := c.narStore.GetNar(ctx, *narURL)
	if err != nil {
		return 0, nil, fmt.Errorf("error fetching the nar from the store: %w", err)
	}

	err = c.withTransaction(ctx, "getNarFromStore", func(qtx database.Querier) error {
		nr, err := qtx.GetNarFileByHashAndCompressionAndQuery(ctx, database.GetNarFileByHashAndCompressionAndQueryParams{
			Hash:        narURL.Hash,
			Compression: narURL.Compression.String(),
			Query:       narURL.Query.Encode(),
		})
		if err != nil {
			// TODO: If record not found, record it instead!
			if database.IsNotFoundError(err) {
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

	return size, r, nil
}

func (c *Cache) getNarFromUpstream(
	ctx context.Context,
	narURL *nar.URL,
	uc *upstream.Cache,
	narInfo *narinfo.NarInfo,
	enableZSTD bool,
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

	var mutators []func(*http.Request)

	if enableZSTD {
		mutators = append(mutators, zstdMutator(ctx, narURL.Compression))
	}

	ctx = narURL.
		NewLogger(*zerolog.Ctx(ctx)).
		WithContext(ctx)

	var ucs []*upstream.Cache
	if uc != nil {
		ucs = []*upstream.Cache{uc}
	} else {
		ucs = c.getHealthyUpstreams()
	}

	uc, err := c.selectNarUpstream(ctx, narURL, ucs, mutators)
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

	resp, err := uc.GetNar(ctx, *narURL, mutators...)
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

	if enableZSTD && resp.Header.Get("Content-Encoding") == "zstd" {
		narURL.Compression = nar.CompressionTypeZstd

		narInfo.Compression = nar.CompressionTypeZstd.String()
		narInfo.URL = narURL.String()
	}

	return resp, nil
}

func (c *Cache) deleteNarFromStore(ctx context.Context, narURL *nar.URL) error {
	// create a new context not associated with any request because we don't want
	// downstream HTTP request to cancel this.
	ctx = zerolog.Ctx(ctx).WithContext(context.Background())

	if !c.narStore.HasNar(ctx, *narURL) {
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

	err := c.withReadLock(ctx, "GetNarInfo", func() error {
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

			return nil
		} else if !errors.Is(err, storage.ErrNotFound) && !errors.Is(err, errNarInfoPurged) {
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
				return nil
			}

			// If narinfo was purged, continue to fetch from upstream
			if !errors.Is(err, errNarInfoPurged) {
				return c.handleStorageFetchError(ctx, hash, err, &narInfo, &metricAttrs)
			}
		}

		metricAttrs = append(metricAttrs,
			attribute.String("result", "miss"),
			attribute.String("status", "success"),
		)

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

		return err
	})
	if err != nil {
		return nil, err
	}

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

	var enableZSTD bool

	if narInfo.Compression == nar.CompressionTypeNone.String() {
		enableZSTD = true
	}

	ctx = zerolog.Ctx(ctx).
		With().
		Str("nar_url", narInfo.URL).
		Bool("zstd_support", enableZSTD).
		Logger().
		WithContext(ctx)

	// Start a job to also pull the nar but don't wait for it to come back unless
	// we need to alter the filesize/compression. For instance, Harmonia,
	// explicitly returns none for compression but does accept encoding request,
	// if that's the case we should get the compressed version and store that
	// instead.
	if enableZSTD {
		// Use detached context to ensure NAR download completes even if narinfo context is canceled
		detachedCtx := trace.ContextWithSpan(
			zerolog.Ctx(ctx).WithContext(c.baseContext),
			trace.SpanFromContext(ctx),
		)
		narDs := c.prePullNar(ctx, detachedCtx, &narURL, uc, narInfo, enableZSTD)
		<-narDs.done

		err := narDs.getError()
		if err != nil {
			zerolog.Ctx(ctx).
				Error().
				Err(err).
				Msg("error pulling the nar")

			ds.setError(err)

			return
		}
	} else {
		// create a detachedCtx that has the same span and logger as the main
		// context but with the baseContext as parent; This context will not cancel
		// when ctx is canceled allowing us to continue pulling the nar in the
		// background.
		detachedCtx := trace.ContextWithSpan(
			zerolog.Ctx(ctx).WithContext(c.baseContext),
			trace.SpanFromContext(ctx),
		)
		c.prePullNar(ctx, detachedCtx, &narURL, uc, narInfo, enableZSTD)
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
	lockKey := fmt.Sprintf("narinfo:%s", hash)

	return c.withWriteLock(ctx, "PutNarInfo", lockKey, func() error {
		narInfo, err := narinfo.Parse(r)
		if err != nil {
			return fmt.Errorf("error parsing narinfo: %w", err)
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

	return c.withReadLock(ctx, "DeleteNarInfo", func() error {
		ctx = zerolog.Ctx(ctx).
			With().
			Str("narinfo_hash", hash).
			Logger().
			WithContext(ctx)

		return c.deleteNarInfoFromStore(ctx, hash)
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
	uc *upstream.Cache,
	narInfo *narinfo.NarInfo,
	enableZSTD bool,
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
			return c.narStore.HasNar(ctx, *narURL)
		},
		func(ds *downloadState) {
			c.pullNarIntoStore(ctx, narURL, uc, narInfo, enableZSTD, ds)
		},
	)
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

	if !c.narStore.HasNar(ctx, narURL) && !c.hasUpstreamJob(narURL.Hash) {
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
	if errors.Is(storageErr, storage.ErrNotFound) {
		var dbErr error

		*narInfo, dbErr = c.getNarInfoFromDatabase(ctx, hash)
		if dbErr == nil {
			// Migration succeeded while we were checking storage!
			// The source is now the database, not storage. We need to update the metric.
			updateAttr("source", "database")

			return nil // Signal success to caller
		}

		// If the DB retry also fails with a non-NotFound error, it's a more serious issue.
		// We should wrap this error to provide more context for debugging.
		if !errors.Is(dbErr, storage.ErrNotFound) {
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

		ni, narURL, populateErr = c.populateNarInfoFromDatabase(ctx, qtx, hash)

		return populateErr
	})
	if err != nil {
		return nil, err
	}

	// Verify Nar file exists in storage
	if !c.narStore.HasNar(ctx, *narURL) && !c.hasUpstreamJob(narURL.Hash) {
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
	if lat, err := nir.LastAccessedAt.Value(); err == nil && time.Since(lat.(time.Time)) > c.recordAgeIgnoreTouch {
		if _, err := qtx.TouchNarInfo(ctx, hash); err != nil {
			return nil, nil, fmt.Errorf("error touching the narinfo record: %w", err)
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
		if c.narStore.HasNar(ctx, *narURL) {
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

		narFileID, err := createOrUpdateNarFile(ctx, qtx, normalizedNarURL, narInfo.FileSize)
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
	lockKey := "migration:" + hash

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
		if err := locker.Unlock(ctx, lockKey); err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Str("narinfo_hash", hash).Msg("failed to release migration lock")
		}
	}()

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
	narFileID, err := createOrUpdateNarFile(ctx, qtx, normalizedNarURL, narInfo.FileSize)
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

	if !inStorage && !inDatabase {
		return storage.ErrNotFound
	}

	// Delete from database (includes cascading deletes for references, signatures, and links)
	if inDatabase {
		if _, err := c.db.DeleteNarInfoByHash(ctx, hash); err != nil {
			return fmt.Errorf("error deleting narinfo from the database: %w", err)
		}
	}

	// Delete from storage if present
	if inStorage {
		if err := c.narInfoStore.DeleteNarInfo(ctx, hash); err != nil {
			return fmt.Errorf("error deleting narinfo from storage: %w", err)
		}
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
		zerolog.Ctx(ctx).Error().
			Err(err).
			Str("hash", hash).
			Str("lock_key", lockKey).
			Msg("failed to acquire download lock")

		ds := newDownloadState()
		ds.downloadError = fmt.Errorf("failed to acquire download lock: %w", err)

		return ds
	}

	// Double check local jobs and asset presence under lock
	if hasAsset(ctx) {
		// Release the lock before returning
		if err := c.downloadLocker.Unlock(c.baseContext, lockKey); err != nil {
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
		if err := c.downloadLocker.Unlock(c.baseContext, lockKey); err != nil {
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

			if err := c.downloadLocker.Unlock(c.baseContext, lockKey); err != nil {
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

// withReadLock executes fn while holding a read lock on the cache.
// It automatically handles lock acquisition, release, and error logging.
func (c *Cache) withReadLock(ctx context.Context, operation string, fn func() error) error {
	if err := c.cacheLocker.RLock(ctx, cacheLockKey, c.cacheLockTTL); err != nil {
		zerolog.Ctx(ctx).Error().
			Err(err).
			Str("operation", operation).
			Msg("failed to acquire read lock")

		return fmt.Errorf("failed to acquire read lock for %s: %w", operation, err)
	}

	defer func() {
		if err := c.cacheLocker.RUnlock(ctx, cacheLockKey); err != nil {
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
		if err := c.cacheLocker.Unlock(ctx, lockKey); err != nil {
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
		if err := c.cacheLocker.Unlock(ctx, lockKey); err != nil {
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
) ([]string, []nar.URL, error) {
	// 1. METADATA PHASE
	// Find the NarInfos that constitute the oldest `cleanupSize` worth of data.
	// We use the query you provided in the first prompt.
	narInfosToDelete, err := qtx.GetLeastUsedNarInfos(ctx, cleanupSize)
	if err != nil {
		log.Error().Err(err).Msg("error getting least used narinfos")

		return nil, nil, err
	}

	if len(narInfosToDelete) == 0 {
		log.Warn().Msg("cleanup required but no reclaimable narinfos found")

		return nil, nil, nil
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

			return nil, nil, err
		}
	}

	// 2. STORAGE PHASE
	// Now that metadata is gone, some files might have zero references.
	// We find those truly orphaned files.
	orphanedNarFiles, err := qtx.GetOrphanedNarFiles(ctx)
	if err != nil {
		log.Error().Err(err).Msg("error identifying orphaned nar files")

		return nil, nil, err
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

			// Delete the file record from DB
			// Note: We use ID here since we have it, it's slightly faster/safer than Hash
			if _, err := qtx.DeleteNarFileByID(ctx, nf.ID); err != nil {
				log.Error().
					Err(err).
					Str("nar_hash", nf.Hash).
					Msg("error deleting orphaned nar file record")

				return nil, nil, err
			}
		}
	} else {
		log.Info().Msg("no orphaned nar files found (files may be shared with active narinfos)")
	}

	return narInfoHashesToRemove, narURLsToRemove, nil
}

// parallelDeleteFromStores deletes narinfos and nars from stores in parallel.
func (c *Cache) parallelDeleteFromStores(
	ctx context.Context,
	log zerolog.Logger,
	narInfoHashesToRemove []string,
	narURLsToRemove []nar.URL,
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

			narInfoHashesToRemove, narURLsToRemove, err := c.deleteLRURecordsFromDB(ctx, qtx, log, cleanupSize)
			if err != nil || len(narInfoHashesToRemove) == 0 {
				return err
			}

			// Track eviction counts
			lruNarInfosEvictedTotal.Add(ctx, int64(len(narInfoHashesToRemove)))
			lruNarFilesEvictedTotal.Add(ctx, int64(len(narURLsToRemove)))

			// Track bytes freed (approximate as cleanupSize)
			//nolint:gosec // G115: Cleanup size is bounded by cache max size, unlikely to exceed int64 max
			lruBytesFreedTotal.Add(ctx, int64(cleanupSize))

			// Commit the database transaction before deleting from stores
			if err := tx.Commit(); err != nil {
				log.Error().Err(err).Msg("error committing the transaction")

				return err
			}

			// Remove all the files from the store as fast as possible
			c.parallelDeleteFromStores(ctx, log, narInfoHashesToRemove, narURLsToRemove)

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

func zstdMutator(
	ctx context.Context,
	compression nar.CompressionType,
) func(r *http.Request) {
	return func(r *http.Request) {
		zerolog.Ctx(ctx).
			Debug().
			Msg("narinfo compress is none will set Accept-Encoding to zstd")

		r.Header.Set("Accept-Encoding", "zstd")

		cfe := compression.ToFileExtension()
		if cfe != "" {
			cfe = "." + cfe
		}

		r.URL.Path = strings.ReplaceAll(
			r.URL.Path,
			"."+nar.CompressionTypeZstd.ToFileExtension(),
			cfe,
		)
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
	mutators []func(*http.Request),
) (*upstream.Cache, error) {
	return c.selectUpstream(ctx, ucs, func(
		ctx context.Context,
		uc *upstream.Cache,
		wg *sync.WaitGroup,
		ch chan *upstream.Cache,
		errC chan error,
	) {
		defer wg.Done()

		exists, err := uc.HasNar(ctx, *narURL, mutators...)
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

	ch := make(chan *upstream.Cache)
	errC := make(chan error)

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
