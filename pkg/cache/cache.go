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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/kalbasit/ncps/pkg/cache/healthcheck"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
)

const (
	recordAgeIgnoreTouch = 5 * time.Minute
	otelPackageName      = "github.com/kalbasit/ncps/pkg/cache"
	cacheLockKey         = "cache"
)

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

	//nolint:gochecknoglobals
	meter metric.Meter

	//nolint:gochecknoglobals
	tracer trace.Tracer

	//nolint:gochecknoglobals
	narServedCount metric.Int64Counter

	//nolint:gochecknoglobals
	narInfoServedCount metric.Int64Counter
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
	lruLocker       lock.RWLocker
	downloadLockTTL time.Duration
	lruLockTTL      time.Duration

	// upstreamJobs is used to store in-progress jobs for pulling nars from
	// upstream cache so incoming requests for the same nar can find and wait
	// for jobs. Protected by upstreamJobsMu for local synchronization.
	upstreamJobsMu sync.Mutex
	upstreamJobs   map[string]*downloadState

	cron *cron.Cron
}

type downloadState struct {
	// Mutex and Condition is used to gate access to this downloadState as well as broadcast chunks
	mu   sync.Mutex
	cond *sync.Cond

	// Information about the asset being downloaded
	wg           sync.WaitGroup
	assetPath    string
	bytesWritten int64
	finalSize    int64

	// Store any download errors in this field
	downloadError error

	// Channel to signal starting the pull and its completion
	done  chan struct{}
	start chan struct{}
}

func newDownloadState() *downloadState {
	ds := &downloadState{
		done:  make(chan struct{}),
		start: make(chan struct{}),
	}

	ds.cond = sync.NewCond(&ds.mu)

	return ds
}

// New returns a new Cache.
func New(
	ctx context.Context,
	hostName string,
	db database.Querier,
	configStore storage.ConfigStore,
	narInfoStore storage.NarInfoStore,
	narStore storage.NarStore,
	secretKeyPath string,
	downloadLocker lock.Locker,
	lruLocker lock.RWLocker,
	downloadLockTTL time.Duration,
	lruLockTTL time.Duration,
) (*Cache, error) {
	c := &Cache{
		baseContext:          ctx,
		db:                   db,
		configStore:          configStore,
		narInfoStore:         narInfoStore,
		narStore:             narStore,
		shouldSignNarinfo:    true,
		downloadLocker:       downloadLocker,
		lruLocker:            lruLocker,
		downloadLockTTL:      downloadLockTTL,
		lruLockTTL:           lruLockTTL,
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

	c.healthChecker = healthcheck.New()

	// Set up health change notifications for dynamic management
	healthChangeCh := make(chan healthcheck.HealthStatusChange, 100)
	c.healthChecker.SetHealthChangeNotifier(healthChangeCh)

	// Start the health checker
	c.healthChecker.Start(c.baseContext)

	// Start the health change processor
	go c.processHealthChanges(ctx, healthChangeCh)

	return c, nil
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

// GetHealthChecker returns the instance of haelth checker used by the cache.
// It's useful for testing the behavior of ncps.
func (c *Cache) GetHealthChecker() *healthcheck.HealthChecker { return c.healthChecker }

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

	lockKey := cacheLockKey

	// Acquire read lock to prevent LRU from deleting files during access
	if err := c.lruLocker.RLock(ctx, lockKey, c.lruLockTTL); err != nil {
		zerolog.Ctx(ctx).Error().
			Err(err).
			Msg("failed to acquire read lock for GetNar")

		return 0, nil, fmt.Errorf("failed to acquire read lock for GetNar: %w", err)
	}

	defer func() {
		if err := c.lruLocker.RUnlock(ctx, lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Msg("failed to release read lock for GetNar")
		}
	}()

	ctx = narURL.
		NewLogger(*zerolog.Ctx(ctx)).
		WithContext(ctx)

	if c.narStore.HasNar(ctx, narURL) {
		metricAttrs = append(metricAttrs,
			attribute.String("result", "hit"),
			attribute.String("status", "success"),
		)

		size, reader, err := c.getNarFromStore(ctx, &narURL)
		if err != nil {
			metricAttrs = append(metricAttrs, attribute.String("status", "error"))
		}

		return size, reader, err
	}

	zerolog.Ctx(ctx).
		Debug().
		Msg("pulling nar in a go-routine and will stream the file back to the client")

	// create a detachedCtx that has the same span and logger as the main
	// context but with the baseContext as parent; This context will not cancel
	// when ctx is canceled allowing us to continue pulling the nar in the
	// background.
	detachedCtx := trace.ContextWithSpan(
		zerolog.Ctx(ctx).WithContext(c.baseContext),
		trace.SpanFromContext(ctx),
	)
	ds := c.prePullNar(detachedCtx, &narURL, nil, nil, false)
	ds.wg.Add(1)

	// double check it still does not exist in the store before we continue
	if c.narStore.HasNar(ctx, narURL) {
		ds.wg.Done()

		metricAttrs = append(metricAttrs,
			attribute.String("result", "hit"),
			attribute.String("status", "success"),
		)

		size, reader, err := c.getNarFromStore(ctx, &narURL)
		if err != nil {
			metricAttrs = append(metricAttrs, attribute.String("status", "error"))
		}

		return size, reader, err
	}

	metricAttrs = append(metricAttrs,
		attribute.String("result", "miss"),
		attribute.String("status", "success"),
	)

	<-ds.start

	if ds.downloadError != nil {
		metricAttrs = append(metricAttrs, attribute.String("status", "error"))

		return 0, nil, ds.downloadError
	}

	// create a pipe to stream file down to the http client
	reader, writer := io.Pipe()

	go func() {
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
				return
			}
		}
	}()

	return -1, reader, nil
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

	lockKey := cacheLockKey

	// Acquire read lock to prevent LRU from deleting files during access
	if err := c.lruLocker.RLock(ctx, lockKey, c.lruLockTTL); err != nil {
		zerolog.Ctx(ctx).Error().
			Err(err).
			Msg("failed to acquire read lock for PutNar")

		return fmt.Errorf("failed to acquire read lock for PutNar: %w", err)
	}

	defer func() {
		if err := c.lruLocker.RUnlock(ctx, lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Msg("failed to release read lock for PutNar")
		}
	}()

	ctx = narURL.
		NewLogger(*zerolog.Ctx(ctx)).
		WithContext(ctx)

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, r)

		r.Close()
	}()

	_, err := c.narStore.PutNar(ctx, narURL, r)

	return err
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

	lockKey := cacheLockKey

	// Acquire read lock to prevent LRU from deleting files during access
	if err := c.lruLocker.RLock(ctx, lockKey, c.lruLockTTL); err != nil {
		zerolog.Ctx(ctx).Error().
			Err(err).
			Msg("failed to acquire read lock for DeleteNar")

		return fmt.Errorf("failed to acquire read lock for DeleteNar: %w", err)
	}

	defer func() {
		if err := c.lruLocker.RUnlock(ctx, lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Msg("failed to release read lock for DeleteNar")
		}
	}()

	ctx = narURL.
		NewLogger(*zerolog.Ctx(ctx)).
		WithContext(ctx)

	return c.narStore.DeleteNar(ctx, narURL)
}

func (c *Cache) pullNarIntoStore(
	ctx context.Context,
	narURL *nar.URL,
	uc *upstream.Cache,
	narInfo *narinfo.NarInfo,
	enableZSTD bool,
	ds *downloadState,
) {
	defer func() {
		// Clean up local job tracking
		c.upstreamJobsMu.Lock()
		delete(c.upstreamJobs, narURL.Hash)
		c.upstreamJobsMu.Unlock()

		select {
		case <-ds.start:
		default:
			close(ds.start)
		}

		// Inform watchers that we are fully done and the asset is now in the store.
		close(ds.done)

		// Final broadcast
		ds.cond.Broadcast()
	}()

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
				Info().
				Err(err).
				Msg("error getting the nar from upstream caches")
		}

		ds.downloadError = err

		return
	}

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)

		resp.Body.Close()
	}()

	// create a temporary file to store the nar
	pattern := narURL.Hash + "-*.nar"
	if cext := narURL.Compression.String(); cext != "" {
		pattern += "." + cext
	}

	f, err := os.CreateTemp(c.tempDir, pattern)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error creating the nar file in the temporary directory")

		ds.downloadError = err

		return
	}

	ds.assetPath = f.Name()

	// Wait until nothing is using the asset and remove it
	ds.wg.Add(1)
	defer ds.wg.Done()

	go func() {
		ds.wg.Wait()
		os.Remove(ds.assetPath)
	}()

	close(ds.start) // inform caller that we started to stream

	// Write the response to the temporary file in chunks so clients can pull early and often
	buf := make([]byte, 32*1024)

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]

			// Write the chunk read to the file
			n, writeErr := f.Write(chunk)
			if writeErr != nil {
				zerolog.Ctx(ctx).
					Error().
					Err(err).
					Msg("error storing the chunk in the temporary file")

				ds.downloadError = writeErr

				return
			}

			// Update the state and signal waiting clients
			ds.mu.Lock()
			ds.bytesWritten += int64(n)
			ds.mu.Unlock()
			ds.cond.Broadcast()
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			ds.downloadError = err

			return
		}
	}

	// Writing the NAR to a temporary file is now done, final notification to watchers
	ds.mu.Lock()
	ds.finalSize = ds.bytesWritten
	ds.mu.Unlock()
	ds.cond.Broadcast()

	f.Close()

	f, err = os.Open(f.Name())
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error opening the nar from the temporary file")

		ds.downloadError = err

		return
	}

	defer f.Close()

	written, err := c.narStore.PutNar(ctx, *narURL, f)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error storing the nar in the store")

		ds.downloadError = err

		return
	}

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

	tx, err := c.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("error beginning a transaction: %w", err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil {
			if !errors.Is(err, sql.ErrTxDone) {
				zerolog.Ctx(ctx).
					Error().
					Err(err).
					Msg("error rolling back the transaction")
			}
		}
	}()

	nr, err := c.db.WithTx(tx).GetNarByHash(ctx, narURL.Hash)
	if err != nil {
		// TODO: If record not found, record it instead!
		if errors.Is(err, sql.ErrNoRows) {
			return size, r, nil
		}

		return 0, nil, fmt.Errorf("error fetching the nar record: %w", err)
	}

	if lat, err := nr.LastAccessedAt.Value(); err == nil && time.Since(lat.(time.Time)) > c.recordAgeIgnoreTouch {
		if _, err := c.db.WithTx(tx).TouchNar(ctx, narURL.Hash); err != nil {
			return 0, nil, fmt.Errorf("error touching the nar record: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, nil, fmt.Errorf("error committing the transaction: %w", err)
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

	var mutators []func(*http.Request)

	if enableZSTD {
		mutators = append(mutators, zstdMutator(ctx, narURL.Compression))

		narURL.Compression = nar.CompressionTypeZstd

		narInfo.Compression = nar.CompressionTypeZstd.String()
		narInfo.URL = narURL.String()
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

	return resp, nil
}

func (c *Cache) deleteNarFromStore(ctx context.Context, narURL *nar.URL) error {
	// create a new context not associated with any request because we don't want
	// downstream HTTP request to cancel this.
	ctx = zerolog.Ctx(ctx).WithContext(context.Background())

	if !c.narStore.HasNar(ctx, *narURL) {
		return storage.ErrNotFound
	}

	if _, err := c.db.DeleteNarByHash(ctx, narURL.Hash); err != nil {
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

	lockKey := cacheLockKey

	// Acquire read lock to prevent LRU from deleting files during access
	if err := c.lruLocker.RLock(ctx, lockKey, c.lruLockTTL); err != nil {
		zerolog.Ctx(ctx).Error().
			Err(err).
			Msg("failed to acquire read lock for GetNarInfo")

		return nil, fmt.Errorf("failed to acquire read lock for GetNarInfo: %w", err)
	}

	defer func() {
		if err := c.lruLocker.RUnlock(ctx, lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Msg("failed to release read lock for GetNarInfo")
		}
	}()

	ctx = zerolog.Ctx(ctx).
		With().
		Str("narinfo_hash", hash).
		Logger().
		WithContext(ctx)

	var (
		narInfo *narinfo.NarInfo
		err     error
	)

	if c.narInfoStore.HasNarInfo(ctx, hash) {
		metricAttrs = append(metricAttrs,
			attribute.String("result", "hit"),
			attribute.String("status", "success"),
		)

		narInfo, err = c.getNarInfoFromStore(ctx, hash)
		if err == nil {
			return narInfo, nil
		} else if !errors.Is(err, errNarInfoPurged) {
			metricAttrs = append(metricAttrs, attribute.String("status", "error"))

			return nil, fmt.Errorf("error fetching the narinfo from the store: %w", err)
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

	if ds.downloadError != nil {
		metricAttrs = append(metricAttrs, attribute.String("status", "error"))

		return nil, ds.downloadError
	}

	return c.narInfoStore.GetNarInfo(ctx, hash)
}

func (c *Cache) pullNarInfo(
	ctx context.Context,
	hash string,
	ds *downloadState,
) {
	done := func() {
		// Clean up local job tracking
		c.upstreamJobsMu.Lock()
		delete(c.upstreamJobs, hash)
		c.upstreamJobsMu.Unlock()

		close(ds.done)
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
				Info().
				Err(err).
				Msg("error getting the narInfo from upstream caches")
		}

		ds.downloadError = err

		return
	}

	narURL, err := nar.ParseURL(narInfo.URL)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Str("nar_url", narInfo.URL).
			Msg("error parsing the nar URL")

		ds.downloadError = err

		return
	}

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
		ds := c.prePullNar(ctx, &narURL, uc, narInfo, enableZSTD)
		<-ds.done

		if ds.downloadError != nil {
			zerolog.Ctx(ctx).
				Error().
				Err(err).
				Msg("error pulling the nar")

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
		c.prePullNar(detachedCtx, &narURL, uc, narInfo, enableZSTD)
	}

	if err := c.signNarInfo(ctx, hash, narInfo); err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error signing the narinfo")

		return
	}

	if err := c.narInfoStore.PutNarInfo(ctx, hash, narInfo); err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error storing the narInfo in the store")

		return
	}

	if err := c.storeInDatabase(ctx, hash, narInfo); err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error storing the narinfo in the database")

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

	lockKey := cacheLockKey

	// Acquire read lock to prevent LRU from deleting files during access
	if err := c.lruLocker.RLock(ctx, lockKey, c.lruLockTTL); err != nil {
		zerolog.Ctx(ctx).Error().
			Err(err).
			Msg("failed to acquire read lock for PutNarInfo")

		return fmt.Errorf("failed to acquire read lock for PutNarInfo: %w", err)
	}

	defer func() {
		if err := c.lruLocker.RUnlock(ctx, lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Msg("failed to release read lock for PutNarInfo")
		}
	}()

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

	narInfo, err := narinfo.Parse(r)
	if err != nil {
		return fmt.Errorf("error parsing narinfo: %w", err)
	}

	if err := c.signNarInfo(ctx, hash, narInfo); err != nil {
		return fmt.Errorf("error signing the narinfo: %w", err)
	}

	if err := c.narInfoStore.PutNarInfo(ctx, hash, narInfo); err != nil {
		return fmt.Errorf("error storing the narInfo in the store: %w", err)
	}

	return c.storeInDatabase(ctx, hash, narInfo)
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

	lockKey := cacheLockKey

	// Acquire read lock to prevent LRU from deleting files during access
	if err := c.lruLocker.RLock(ctx, lockKey, c.lruLockTTL); err != nil {
		zerolog.Ctx(ctx).Error().
			Err(err).
			Msg("failed to acquire read lock for DeleteNarInfo")

		return fmt.Errorf("failed to acquire read lock for DeleteNarInfo: %w", err)
	}

	defer func() {
		if err := c.lruLocker.RUnlock(ctx, lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Msg("failed to release read lock for DeleteNarInfo")
		}
	}()

	ctx = zerolog.Ctx(ctx).
		With().
		Str("narinfo_hash", hash).
		Logger().
		WithContext(ctx)

	return c.deleteNarInfoFromStore(ctx, hash)
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

	lockKey := "download:narinfo:" + hash

	// Acquire lock with retry (handled internally by Redis locker)
	// If using local locks, this returns immediately
	// If using Redis and lock fails, this returns immediately (lock will auto-expire)
	if err := c.downloadLocker.Lock(ctx, lockKey, c.downloadLockTTL); err != nil {
		zerolog.Ctx(ctx).Error().
			Err(err).
			Str("hash", hash).
			Msg("failed to acquire download lock for narinfo")

		ds := newDownloadState()
		ds.downloadError = fmt.Errorf("failed to acquire download lock for narinfo: %w", err)

		return ds
	}

	defer func() {
		if err := c.downloadLocker.Unlock(ctx, lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Str("hash", hash).
				Msg("failed to release download lock for narinfo")
		}
	}()

	// Check if NarInfo is already in storage (critical for distributed deduplication)
	// Another instance may have downloaded it while we were waiting for the lock
	if c.narInfoStore.HasNarInfo(ctx, hash) {
		zerolog.Ctx(ctx).Debug().
			Str("hash", hash).
			Msg("NarInfo already in storage, skipping download")

		// Return a completed downloadState
		ds := newDownloadState()
		close(ds.start)
		close(ds.done)

		return ds
	}

	// Check upstreamJobs map (protected by local mutex) and create downloadState if needed
	c.upstreamJobsMu.Lock()

	ds, ok := c.upstreamJobs[hash]
	if !ok {
		ds = newDownloadState()
		c.upstreamJobs[hash] = ds

		// Start download in background (must be done while holding the lock to ensure
		// the job is registered before we release the distributed lock)
		go c.pullNarInfo(ctx, hash, ds)
	}

	c.upstreamJobsMu.Unlock()

	return ds
}

func (c *Cache) prePullNar(
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

	lockKey := "download:nar:" + narURL.Hash

	// Acquire lock with retry (handled internally by Redis locker)
	// If using local locks, this returns immediately
	// If using Redis and lock fails, this returns immediately (lock will auto-expire)
	if err := c.downloadLocker.Lock(ctx, lockKey, c.downloadLockTTL); err != nil {
		zerolog.Ctx(ctx).Error().
			Err(err).
			Str("hash", narURL.Hash).
			Msg("failed to acquire download lock for nar")

		ds := newDownloadState()
		ds.downloadError = fmt.Errorf("failed to acquire download lock for nar: %w", err)

		return ds
	}

	defer func() {
		if err := c.downloadLocker.Unlock(ctx, lockKey); err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Str("hash", narURL.Hash).
				Msg("failed to release download lock for nar")
		}
	}()

	// Check if NAR is already in storage (critical for distributed deduplication)
	// Another instance may have downloaded it while we were waiting for the lock
	if c.narStore.HasNar(ctx, *narURL) {
		zerolog.Ctx(ctx).Debug().
			Str("hash", narURL.Hash).
			Msg("NAR already in storage, skipping download")

		// Return a completed downloadState
		ds := newDownloadState()
		close(ds.start)
		close(ds.done)

		return ds
	}

	// Check upstreamJobs map (protected by local mutex) and create downloadState if needed
	c.upstreamJobsMu.Lock()

	ds, ok := c.upstreamJobs[narURL.Hash]
	if !ok {
		ds = newDownloadState()
		c.upstreamJobs[narURL.Hash] = ds

		// Start download in background (must be done while holding the lock to ensure
		// the job is registered before we release the distributed lock)
		go c.pullNarIntoStore(ctx, narURL, uc, narInfo, enableZSTD, ds)
	}

	c.upstreamJobsMu.Unlock()

	return ds
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

	tx, err := c.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error beginning a transaction: %w", err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil {
			if !errors.Is(err, sql.ErrTxDone) {
				zerolog.Ctx(ctx).
					Error().
					Err(err).
					Msg("error rolling back the transaction")
			}
		}
	}()

	nir, err := c.db.WithTx(tx).GetNarInfoByHash(ctx, hash)
	if err != nil {
		// TODO: If record not found, record it instead!
		if errors.Is(err, sql.ErrNoRows) {
			return ni, nil
		}

		return nil, fmt.Errorf("error fetching the narinfo record: %w", err)
	}

	if lat, err := nir.LastAccessedAt.Value(); err == nil && time.Since(lat.(time.Time)) > c.recordAgeIgnoreTouch {
		if _, err := c.db.WithTx(tx).TouchNarInfo(ctx, hash); err != nil {
			return nil, fmt.Errorf("error touching the narinfo record: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("error committing the transaction: %w", err)
	}

	return ni, nil
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

	tx, err := c.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning a transaction: %w", err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil {
			if !errors.Is(err, sql.ErrTxDone) {
				zerolog.Ctx(ctx).
					Error().
					Err(err).
					Msg("error rolling back the transaction")
			}
		}
	}()

	if _, err := c.db.WithTx(tx).DeleteNarInfoByHash(ctx, hash); err != nil {
		return fmt.Errorf("error deleting the narinfo record: %w", err)
	}

	if narURL.Hash != "" {
		if _, err := c.db.WithTx(tx).DeleteNarByHash(ctx, narURL.Hash); err != nil {
			return fmt.Errorf("error deleting the nar record: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing the transaction: %w", err)
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
		Msg("storing narinfo and nar record in the database")

	tx, err := c.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning a transaction: %w", err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil {
			if !errors.Is(err, sql.ErrTxDone) {
				zerolog.Ctx(ctx).
					Error().
					Err(err).
					Msg("error rolling back the transaction")
			}
		}
	}()

	nir, err := c.db.WithTx(tx).CreateNarInfo(ctx, hash)
	if err != nil {
		if database.IsDuplicateKeyError(err) {
			zerolog.Ctx(ctx).
				Warn().
				Msg("narinfo record was not added to database because it already exists")

			return nil
		}

		return fmt.Errorf("error inserting the narinfo record for hash %q in the database: %w", hash, err)
	}

	narURL, err := nar.ParseURL(narInfo.URL)
	if err != nil {
		return fmt.Errorf("error parsing the nar URL: %w", err)
	}

	_, err = c.db.WithTx(tx).CreateNar(ctx, database.CreateNarParams{
		NarInfoID:   nir.ID,
		Hash:        narURL.Hash,
		Compression: narURL.Compression.String(),
		Query:       narURL.Query.Encode(),
		FileSize:    narInfo.FileSize,
	})
	if err != nil {
		if database.IsDuplicateKeyError(err) {
			zerolog.Ctx(ctx).
				Warn().
				Msg("nar record was not added to database because it already exists")

			return nil
		}

		return fmt.Errorf("error inserting the nar record in the database: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing the transaction: %w", err)
	}

	return nil
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

	if !c.narInfoStore.HasNarInfo(ctx, hash) {
		return storage.ErrNotFound
	}

	if _, err := c.db.DeleteNarInfoByHash(ctx, hash); err != nil {
		return fmt.Errorf("error deleting narinfo from the database: %w", err)
	}

	return c.narInfoStore.DeleteNarInfo(ctx, hash)
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
	if secretKeyPath != "" {
		skc, err := os.ReadFile(secretKeyPath)
		if err != nil {
			return fmt.Errorf("error reading the given secret key located at %q: %w", secretKeyPath, err)
		}

		c.secretKey, err = signature.LoadSecretKey(string(skc))
		if err != nil {
			return fmt.Errorf("error loading the given secret key located at %q: %w", secretKeyPath, err)
		}

		return nil
	}

	var err error

	c.secretKey, err = c.configStore.GetSecretKey(ctx)
	if err == nil {
		return nil
	}

	if !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("error fetching the secret key from the store: %w", err)
	}

	c.secretKey, _, err = signature.GenerateKeypair(c.hostName, nil)
	if err != nil {
		return fmt.Errorf("error generating a secret key pair: %w", err)
	}

	if err = c.configStore.PutSecretKey(ctx, c.secretKey); err != nil {
		return fmt.Errorf("error storing the generated secret key in the store: %w", err)
	}

	return nil
}

func (c *Cache) hasUpstreamJob(hash string) bool {
	c.upstreamJobsMu.Lock()
	defer c.upstreamJobsMu.Unlock()

	_, ok := c.upstreamJobs[hash]

	return ok
}

func (c *Cache) runLRU(ctx context.Context) func() {
	return func() {
		lockKey := "lru"

		// Try to acquire LRU lock (non-blocking)
		acquired, err := c.lruLocker.TryLock(ctx, lockKey, c.lruLockTTL)
		if err != nil {
			zerolog.Ctx(ctx).Error().
				Err(err).
				Msg("error trying to acquire LRU lock, skipping LRU run")

			return
		}

		if !acquired {
			// Another instance is running LRU, skip this run
			zerolog.Ctx(ctx).Info().
				Msg("another instance is running LRU, skipping")

			return
		}

		defer func() {
			if err := c.lruLocker.Unlock(ctx, lockKey); err != nil {
				zerolog.Ctx(ctx).Error().
					Err(err).
					Msg("failed to release LRU lock")
			}
		}()

		log := zerolog.Ctx(ctx).With().
			Str("op", "lru").
			Uint64("max_size", c.maxSize).
			Logger()

		log.Info().Msg("running LRU")

		tx, err := c.db.DB().BeginTx(ctx, nil)
		if err != nil {
			log.Error().Err(err).Msg("error beginning a transaction")

			return
		}

		defer func() {
			if err := tx.Rollback(); err != nil {
				if !errors.Is(err, sql.ErrTxDone) {
					log.Error().Err(err).Msg("error rolling back the transaction")
				}
			}
		}()

		narTotalSize, err := c.db.WithTx(tx).GetNarTotalSize(ctx)
		if err != nil {
			log.Error().Err(err).Msg("error fetching the total nar size")

			return
		}

		if narTotalSize == 0 {
			log.Info().Msg("SUM(file_size) is zero, nothing to clean up")

			return
		}

		log = log.With().Int64("nar_total_size", narTotalSize).Logger()

		//nolint:gosec
		if uint64(narTotalSize) <= c.maxSize {
			log.Info().Msg("store size is less than max-size, not removing any nars")

			return
		}

		//nolint:gosec
		cleanupSize := uint64(narTotalSize) - c.maxSize

		log = log.With().Uint64("cleanup_size", cleanupSize).Logger()

		log.Info().Msg("going to remove nars")

		nars, err := c.db.WithTx(tx).GetLeastUsedNars(ctx, cleanupSize)
		if err != nil {
			log.Error().Err(err).Msg("error getting the least used nars up to cleanup-size")

			return
		}

		if len(nars) == 0 {
			log.Warn().Msg("nars needed to be removed but none were returned in the query")

			return
		}

		log.Info().Int("count_nars", len(nars)).Msg("found this many nars to remove")

		narInfoHashesToRemove := make([]string, 0, len(nars))
		narURLsToRemove := make([]nar.URL, 0, len(nars))

		for _, narRecord := range nars {
			narInfo, err := c.db.WithTx(tx).GetNarInfoByID(ctx, narRecord.NarInfoID)
			if err == nil {
				log.Info().Str("narinfo_hash", narInfo.Hash).Msg("deleting narinfo record")

				if _, err := c.db.WithTx(tx).DeleteNarInfoByHash(ctx, narInfo.Hash); err != nil {
					log.Error().
						Err(err).
						Str("narinfo_hash", narInfo.Hash).
						Msg("error removing narinfo from database")
				}

				narInfoHashesToRemove = append(narInfoHashesToRemove, narInfo.Hash)
			} else {
				log.Error().
					Err(err).
					Int64("ID", narRecord.NarInfoID).
					Msg("error fetching narinfo from the database")
			}

			log.Info().Str("nar_hash", narRecord.Hash).Msg("deleting nar record")

			if _, err := c.db.WithTx(tx).DeleteNarByHash(ctx, narRecord.Hash); err != nil {
				log.Error().
					Err(err).
					Str("nar_hash", narRecord.Hash).
					Msg("error removing nar from database")
			}

			// NOTE: we don't need the query when working with store so it's
			// explicitly omitted.
			narURLsToRemove = append(narURLsToRemove, nar.URL{
				Hash:        narRecord.Hash,
				Compression: nar.CompressionTypeFromString(narRecord.Compression),
			})
		}

		// remove all the files from the store as fast as possible.

		var wg sync.WaitGroup

		for _, hash := range narInfoHashesToRemove {
			wg.Add(1)

			go func() {
				defer wg.Done()

				log := log.With().Str("narinfo_hash", hash).Logger()

				log.Info().Msg("deleting narinfo from store")

				if err := c.narInfoStore.DeleteNarInfo(ctx, hash); err != nil {
					log.Error().
						Err(err).
						Msg("error removing the narinfo from the store")
				}
			}()
		}

		for _, narURL := range narURLsToRemove {
			wg.Add(1)

			go func() {
				defer wg.Done()

				log := log.With().Str("nar_url", narURL.String()).Logger()

				log.Info().Msg("deleting nar from store")

				if err := c.narStore.DeleteNar(ctx, narURL); err != nil {
					log.Error().
						Err(err).
						Msg("error removing the nar from the store")
				}
			}()
		}

		wg.Wait()

		// finally commit the database transaction

		if err := tx.Commit(); err != nil {
			log.Error().Err(err).Msg("error committing the transaction")
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

		go selectFn(ctx, uc, &wg, ch, errC)
	}

	go func() {
		wg.Wait()

		close(ch)
	}()

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
