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

	"github.com/mattn/go-sqlite3"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/kalbasit/ncps/pkg/cache/healthcheck"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
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
)

const (
	recordAgeIgnoreTouch = 5 * time.Minute
	tracerName           = "github.com/kalbasit/ncps/pkg/cache"
)

// Cache represents the main cache service.
type Cache struct {
	baseContext context.Context

	hostName       string
	secretKey      signature.SecretKey
	upstreamCaches []*upstream.Cache
	healthChecker  *healthcheck.HealthChecker
	maxSize        uint64
	db             *database.Queries

	tracer trace.Tracer

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

	// upstreamJobs is used to store in-progress jobs for pulling nars from
	// upstream cache so incomping requests for the same nar can find and wait
	// for jobs.
	muUpstreamJobs sync.Mutex
	upstreamJobs   map[string]chan struct{}

	// mu  is used by the LRU garbage collector to freeze access to the cache.
	mu sync.RWMutex

	cron *cron.Cron
}

// New returns a new Cache.
func New(
	ctx context.Context,
	hostName string,
	db *database.Queries,
	configStore storage.ConfigStore,
	narInfoStore storage.NarInfoStore,
	narStore storage.NarStore,
	secretKeyPath string,
) (*Cache, error) {
	c := &Cache{
		baseContext:          ctx,
		db:                   db,
		tracer:               otel.Tracer(tracerName),
		configStore:          configStore,
		narInfoStore:         narInfoStore,
		narStore:             narStore,
		shouldSignNarinfo:    true,
		upstreamJobs:         make(map[string]chan struct{}),
		recordAgeIgnoreTouch: recordAgeIgnoreTouch,
	}

	if err := c.validateHostname(hostName); err != nil {
		return c, err
	}

	c.hostName = hostName

	if err := c.setupSecretKey(ctx, secretKeyPath); err != nil {
		return c, fmt.Errorf("error setting up the secret key: %w", err)
	}

	return c, nil
}

// AddUpstreamCaches adds one or more upstream caches with lazy loading support.
func (c *Cache) AddUpstreamCaches(ctx context.Context, ucs ...*upstream.Cache) {
	c.upstreamCaches = append(c.upstreamCaches, ucs...)
	c.healthChecker = healthcheck.New(c.upstreamCaches)

	// Set up health change notifications for dynamic management
	healthChangeCh := make(chan healthcheck.HealthStatusChange, 100)
	c.healthChecker.SetHealthChangeNotifier(healthChangeCh)

	// Start the health checker
	c.healthChecker.Start(c.baseContext)

	// Start the health change processor
	go c.processHealthChanges(ctx, healthChangeCh)
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
	ctx, span := c.tracer.Start(
		ctx,
		"cache.GetNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
		),
	)
	defer span.End()

	c.mu.RLock()
	defer c.mu.RUnlock()

	ctx = narURL.
		NewLogger(*zerolog.Ctx(ctx)).
		WithContext(ctx)

	if c.narStore.HasNar(ctx, narURL) {
		return c.getNarFromStore(ctx, &narURL)
	}

	// create a detachedCtx that has the same span and logger as the main
	// context but with the baseContext as parent; This context will not cancel
	// when ctx is canceled allowing us to continue pulling the nar in the
	// background.
	detachedCtx := trace.ContextWithSpan(
		zerolog.Ctx(ctx).WithContext(c.baseContext),
		trace.SpanFromContext(ctx),
	)
	doneC := c.prePullNar(detachedCtx, &narURL, nil, nil, false)

	zerolog.Ctx(ctx).
		Debug().
		Msg("pulling nar in a go-routing and will wait for it")
	<-doneC

	return c.getNarFromStore(ctx, &narURL)
}

// PutNar records the NAR (given as an io.Reader) into the store.
func (c *Cache) PutNar(ctx context.Context, narURL nar.URL, r io.ReadCloser) error {
	ctx, span := c.tracer.Start(
		ctx,
		"cache.PutNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
		),
	)
	defer span.End()

	c.mu.RLock()
	defer c.mu.RUnlock()

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
	ctx, span := c.tracer.Start(
		ctx,
		"cache.DeleteNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
		),
	)
	defer span.End()

	c.mu.RLock()
	defer c.mu.RUnlock()

	ctx = narURL.
		NewLogger(*zerolog.Ctx(ctx)).
		WithContext(ctx)

	return c.narStore.DeleteNar(ctx, narURL)
}

func (c *Cache) pullNar(
	ctx context.Context,
	narURL *nar.URL,
	uc *upstream.Cache,
	narInfo *narinfo.NarInfo,
	enableZSTD bool,
	doneC chan struct{},
) {
	done := func() {
		c.muUpstreamJobs.Lock()
		delete(c.upstreamJobs, narURL.Hash)
		c.muUpstreamJobs.Unlock()

		close(doneC)
	}

	ctx, span := c.tracer.Start(
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

		done()

		return
	}

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)

		resp.Body.Close()
	}()

	written, err := c.narStore.PutNar(ctx, *narURL, resp.Body)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error storing the narInfo in the store")

		done()

		return
	}

	if enableZSTD && written > 0 {
		narInfo.FileSize = uint64(written)
	}

	zerolog.Ctx(ctx).
		Info().
		Dur("elapsed", time.Since(now)).
		Msg("download of nar complete")

	done()
}

func (c *Cache) getNarFromStore(
	ctx context.Context,
	narURL *nar.URL,
) (int64, io.ReadCloser, error) {
	ctx, span := c.tracer.Start(
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
		return 0, nil, fmt.Errorf("error fetching the narinfo from the store: %w", err)
	}

	tx, err := c.db.DB().Begin()
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
	ctx, span := c.tracer.Start(
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
	ctx, span := c.tracer.Start(
		ctx,
		"cache.GetNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	c.mu.RLock()
	defer c.mu.RUnlock()

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
		narInfo, err = c.getNarInfoFromStore(ctx, hash)
		if err == nil {
			return narInfo, nil
		} else if !errors.Is(err, errNarInfoPurged) {
			return nil, fmt.Errorf("error fetching the narinfo from the store: %w", err)
		}
	}

	doneC := c.prePullNarInfo(ctx, hash)

	zerolog.Ctx(ctx).
		Debug().
		Msg("pulling nar in a go-routing and will wait for it")
	<-doneC

	return c.narInfoStore.GetNarInfo(ctx, hash)
}

func (c *Cache) pullNarInfo(
	ctx context.Context,
	hash string,
	doneC chan struct{},
) {
	done := func() {
		c.muUpstreamJobs.Lock()
		delete(c.upstreamJobs, hash)
		c.muUpstreamJobs.Unlock()

		close(doneC)
	}

	ctx, span := c.tracer.Start(
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

		done()

		return
	}

	narURL, err := nar.ParseURL(narInfo.URL)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Str("nar_url", narInfo.URL).
			Msg("error parsing the nar URL")

		done()

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
		<-c.prePullNar(ctx, &narURL, uc, narInfo, enableZSTD)
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

		done()

		return
	}

	if err := c.narInfoStore.PutNarInfo(ctx, hash, narInfo); err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error storing the narInfo in the store")

		done()

		return
	}

	if err := c.storeInDatabase(ctx, hash, narInfo); err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error storing the narinfo in the database")

		done()

		return
	}

	zerolog.Ctx(ctx).
		Info().
		Dur("elapsed", time.Since(now)).
		Msg("download of narinfo complete")

	done()
}

// PutNarInfo records the narInfo (given as an io.Reader) into the store and signs it.
func (c *Cache) PutNarInfo(ctx context.Context, hash string, r io.ReadCloser) error {
	ctx, span := c.tracer.Start(
		ctx,
		"cache.PutNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	c.mu.RLock()
	defer c.mu.RUnlock()

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
	ctx, span := c.tracer.Start(
		ctx,
		"cache.DeleteNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	c.mu.RLock()
	defer c.mu.RUnlock()

	ctx = zerolog.Ctx(ctx).
		With().
		Str("narinfo_hash", hash).
		Logger().
		WithContext(ctx)

	return c.deleteNarInfoFromStore(ctx, hash)
}

func (c *Cache) prePullNarInfo(ctx context.Context, hash string) chan struct{} {
	ctx, span := c.tracer.Start(
		ctx,
		"cache.prePullNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	c.muUpstreamJobs.Lock()

	doneC, ok := c.upstreamJobs[hash]
	if ok {
		zerolog.Ctx(ctx).
			Info().
			Msg("waiting for an in-progress download of narinfo to finish")
	} else {
		doneC = make(chan struct{})
		c.upstreamJobs[hash] = doneC

		go c.pullNarInfo(ctx, hash, doneC)
	}
	c.muUpstreamJobs.Unlock()

	return doneC
}

func (c *Cache) prePullNar(
	ctx context.Context,
	narURL *nar.URL,
	uc *upstream.Cache,
	narInfo *narinfo.NarInfo,
	enableZSTD bool,
) chan struct{} {
	ctx, span := c.tracer.Start(
		ctx,
		"cache.prePullNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
		),
	)
	defer span.End()

	c.muUpstreamJobs.Lock()

	doneC, ok := c.upstreamJobs[narURL.Hash]
	if !ok {
		doneC = make(chan struct{})
		c.upstreamJobs[narURL.Hash] = doneC

		go c.pullNar(ctx, narURL, uc, narInfo, enableZSTD, doneC)
	}
	c.muUpstreamJobs.Unlock()

	return doneC
}

func (c *Cache) signNarInfo(ctx context.Context, hash string, narInfo *narinfo.NarInfo) error {
	if !c.shouldSignNarinfo {
		return nil
	}

	_, span := c.tracer.Start(
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
	ctx, span := c.tracer.Start(
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
			Msg("narinfo was requested but no nar was found requesting a purge")

		if err := c.purgeNarInfo(ctx, hash, &narURL); err != nil {
			return nil, fmt.Errorf("error purging the narinfo: %w", err)
		}

		return nil, errNarInfoPurged
	}

	tx, err := c.db.DB().Begin()
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
	ctx, span := c.tracer.Start(
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
	ctx, span := c.tracer.Start(
		ctx,
		"cache.purgeNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
			attribute.String("narinfo_url", narURL.String()),
		),
	)
	defer span.End()

	tx, err := c.db.DB().Begin()
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
	ctx, span := c.tracer.Start(
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

	tx, err := c.db.DB().Begin()
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
		if database.ErrorIsNo(err, sqlite3.ErrConstraint) {
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
		if database.ErrorIsNo(err, sqlite3.ErrConstraint) {
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
	ctx, span := c.tracer.Start(
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
	c.muUpstreamJobs.Lock()
	_, ok := c.upstreamJobs[hash]
	c.muUpstreamJobs.Unlock()

	return ok
}

func (c *Cache) runLRU(ctx context.Context) func() {
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()

		log := zerolog.Ctx(ctx).With().
			Str("op", "lru").
			Uint64("max_size", c.maxSize).
			Logger()

		log.Info().Msg("running LRU")

		tx, err := c.db.DB().Begin()
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

		if !narTotalSize.Valid {
			log.Error().Msg("SUM(file_size) returned NULL")

			return
		}

		log = log.With().Float64("nar_total_size", narTotalSize.Float64).Logger()

		if uint64(narTotalSize.Float64) <= c.maxSize {
			log.Info().Msg("store size is less than max-size, not removing any nars")

			return
		}

		cleanupSize := uint64(narTotalSize.Float64) - c.maxSize

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
	var healthyUpstreams []*upstream.Cache
	for _, u := range c.upstreamCaches {
		// With lazy loading, we include all upstreams that are either healthy
		// or have not been explicitly marked as unhealthy yet
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

// processHealthChanges handles health status changes for upstreams
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
