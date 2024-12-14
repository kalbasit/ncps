package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/inconshreveable/log15/v3"
	"github.com/mattn/go-sqlite3"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/robfig/cron/v3"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
)

var (
	// ErrPathMustBeAbsolute is returned if the given path to New was not absolute.
	ErrPathMustBeAbsolute = errors.New("path must be absolute")

	// ErrPathMustExist is returned if the given path to New did not exist.
	ErrPathMustExist = errors.New("path must exist")

	// ErrPathMustBeADirectory is returned if the given path to New is not a directory.
	ErrPathMustBeADirectory = errors.New("path must be a directory")

	// ErrPathMustBeWritable is returned if the given path to New is not writable.
	ErrPathMustBeWritable = errors.New("path must be writable")

	// ErrHostnameRequired is returned if the given hostName to New is not given.
	ErrHostnameRequired = errors.New("hostName is required")

	// ErrHostnameMustNotContainScheme is returned if the given hostName to New contained a scheme.
	ErrHostnameMustNotContainScheme = errors.New("hostName must not contain scheme")

	// ErrHostnameNotValid is returned if the given hostName to New is not valid.
	ErrHostnameNotValid = errors.New("hostName is not valid")

	// ErrHostnameMustNotContainPath is returned if the given hostName to New contained a path.
	ErrHostnameMustNotContainPath = errors.New("hostName must not contain a path")

	// ErrNotFound is returned if the nar or narinfo were not found.
	ErrNotFound = errors.New("not found")

	// errNarInfoPurged is returned if the narinfo was purged.
	errNarInfoPurged = errors.New("the narinfo was purged")
)

const recordAgeIgnoreTouch = 5 * time.Minute

// Cache represents the main cache service.
type Cache struct {
	hostName       string
	logger         log15.Logger
	path           string
	secretKey      signature.SecretKey
	upstreamCaches []upstream.Cache
	maxSize        uint64
	db             *database.Queries

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
func New(logger log15.Logger, hostName, cachePath string) (*Cache, error) {
	c := &Cache{
		logger:               logger,
		upstreamJobs:         make(map[string]chan struct{}),
		recordAgeIgnoreTouch: recordAgeIgnoreTouch,
	}

	if err := c.validateHostname(hostName); err != nil {
		return c, err
	}

	if err := c.validatePath(cachePath); err != nil {
		return c, err
	}

	c.hostName = hostName
	c.path = cachePath

	sk, err := c.setupSecretKey()
	if err != nil {
		return c, fmt.Errorf("error setting up the secret key: %w", err)
	}

	c.secretKey = sk

	return c, c.setup()
}

// AddUpstreamCaches adds one or more upstream caches.
func (c *Cache) AddUpstreamCaches(ucs ...upstream.Cache) {
	ucss := append(c.upstreamCaches, ucs...)

	slices.SortFunc(ucss, func(a, b upstream.Cache) int {
		//nolint:gosec
		return int(a.GetPriority() - b.GetPriority())
	})

	c.logger.Info("the order of upstream caches has been determined by priority to be")

	for idx, uc := range ucss {
		c.logger.Info("upstream cache", "idx", idx, "hostname", uc.GetHostname(), "priority", uc.GetPriority())
	}

	c.upstreamCaches = ucss
}

// SetMaxSize sets the maxsize of the cache. This will be used by the LRU
// cronjob to automatically clean-up the store.
func (c *Cache) SetMaxSize(maxSize uint64) { c.maxSize = maxSize }

// SetupCron creates a cron instance in the cache.
func (c *Cache) SetupCron(timezone *time.Location) {
	var opts []cron.Option
	if timezone != nil {
		opts = append(opts, cron.WithLocation(timezone))
	}

	c.cron = cron.New(opts...)

	c.logger.Info("cron setup complete")
}

// AddLRUCronJob adds a job for LRU.
func (c *Cache) AddLRUCronJob(schedule cron.Schedule) {
	c.logger.Info("adding a cronjob for LRU", "next-run", schedule.Next(time.Now()))

	c.cron.Schedule(schedule, cron.FuncJob(c.runLRU))
}

// StartCron starts the cron scheduler in its own go-routine, or no-op if already started.
func (c *Cache) StartCron() {
	c.logger.Info("starting the cron scheduler")

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
	c.mu.RLock()
	defer c.mu.RUnlock()

	log := narURL.NewLogger(c.logger)

	if c.hasNarInStore(log, narURL) {
		return c.getNarFromStore(ctx, log, narURL)
	}

	doneC := c.prePullNar(log, narURL, false, nil, nil)

	log.Debug("pulling nar in a go-routing and will wait for it")
	<-doneC

	if !c.hasNarInStore(log, narURL) {
		return 0, nil, ErrNotFound
	}

	return c.getNarFromStore(ctx, log, narURL)
}

// PutNar records the NAR (given as an io.Reader) into the store.
func (c *Cache) PutNar(_ context.Context, narURL nar.URL, r io.ReadCloser) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, r)

		r.Close()
	}()

	log := narURL.NewLogger(c.logger)

	_, err := c.putNarInStore(log, narURL, r)

	return err
}

// DeleteNar deletes the nar from the store.
func (c *Cache) DeleteNar(_ context.Context, narURL nar.URL) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	log := narURL.NewLogger(c.logger)

	return c.deleteNarFromStore(log, narURL)
}

func (c *Cache) pullNar(
	log log15.Logger,
	enableZSTD bool,
	uc *upstream.Cache,
	narInfo *narinfo.NarInfo,
	narURL nar.URL,
	doneC chan struct{},
) {
	done := func() {
		c.muUpstreamJobs.Lock()
		delete(c.upstreamJobs, narURL.Hash)
		c.muUpstreamJobs.Unlock()

		close(doneC)
	}

	now := time.Now()

	log.Info("downloading the nar from upstream")

	resp, err := c.getNarFromUpstream(log, enableZSTD, uc, narInfo, narURL)
	if err != nil {
		log.Error("error getting the narInfo from upstream caches", "error", err)

		done()

		return
	}

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)

		resp.Body.Close()
	}()

	written, err := c.putNarInStore(log, narURL, resp.Body)
	if err != nil {
		log.Error("error storing the narInfo in the store", "error", err)

		done()

		return
	}

	if enableZSTD && written > 0 {
		narInfo.FileSize = uint64(written)
	}

	log.Info("download of nar complete", "elapsed", time.Since(now))

	done()
}

func (c *Cache) getNarPathInStore(narURL nar.URL) string {
	return filepath.Join(c.storeNarPath(), narURL.ToFilePath())
}

func (c *Cache) getNarInfoPathInStore(hash string) string {
	return filepath.Join(c.storeNarInfoPath(), helper.NarInfoFilePath(hash))
}

func (c *Cache) hasNarInStore(log log15.Logger, narURL nar.URL) bool {
	return c.hasInStore(log, c.getNarPathInStore(narURL))
}

func (c *Cache) getNarFromStore(
	ctx context.Context,
	log log15.Logger,
	narURL nar.URL,
) (int64, io.ReadCloser, error) {
	size, r, err := c.getFromStore(log, c.getNarPathInStore(narURL))
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
				log.Error("error rolling back the transaction", "error", err)
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
	log log15.Logger,
	enableZSTD bool,
	uc *upstream.Cache,
	narInfo *narinfo.NarInfo,
	narURL nar.URL,
) (*http.Response, error) {
	// create a new context not associated with any request because we don't want
	// pulling from upstream to be associated with a user request.
	ctx := context.Background()

	var mutators []func(*http.Request)

	if enableZSTD {
		mutators = append(mutators, zstdMutator(log, narURL.Compression))

		narInfo.Compression = nar.CompressionTypeZstd.String()
		narURL.Compression = nar.CompressionTypeZstd
	}

	log = narURL.NewLogger(log)

	var ucs []upstream.Cache
	if uc != nil {
		ucs = []upstream.Cache{*uc}
	} else {
		ucs = c.upstreamCaches
	}

	for _, uc := range ucs {
		resp, err := uc.GetNar(ctx, narURL, mutators...)
		if err != nil {
			if !errors.Is(err, upstream.ErrNotFound) {
				log.Error("error fetching the narInfo from upstream", "hostname", uc.GetHostname(), "error", err)
			}

			continue
		}

		return resp, nil
	}

	return nil, ErrNotFound
}

//nolint:unparam
func (c *Cache) putNarInStore(_ log15.Logger, narURL nar.URL, r io.ReadCloser) (int64, error) {
	pattern := narURL.Hash + "-*.nar"
	if cext := narURL.Compression.String(); cext != "" {
		pattern += "." + cext
	}

	f, err := os.CreateTemp(c.storeTMPPath(), pattern)
	if err != nil {
		return 0, fmt.Errorf("error creating the temporary directory: %w", err)
	}

	written, err := io.Copy(f, r)
	if err != nil {
		f.Close()
		os.Remove(f.Name())

		return 0, fmt.Errorf("error writing the nar to the temporary file: %w", err)
	}

	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("error closing the temporary file: %w", err)
	}

	narPath := c.getNarPathInStore(narURL)

	if err := os.MkdirAll(filepath.Dir(narPath), 0o700); err != nil {
		return 0, fmt.Errorf("error creating the directories for %q: %w", narPath, err)
	}

	if err := os.Rename(f.Name(), narPath); err != nil {
		return 0, fmt.Errorf("error creating the nar file %q: %w", narPath, err)
	}

	return written, nil
}

func (c *Cache) deleteNarFromStore(log log15.Logger, narURL nar.URL) error {
	if !c.hasNarInStore(log, narURL) {
		return ErrNotFound
	}

	// create a new context not associated with any request because we don't want
	// downstream HTTP request to cancel this.
	ctx := context.Background()

	if _, err := c.db.DeleteNarByHash(ctx, narURL.Hash); err != nil {
		return fmt.Errorf("error deleting narinfo from the database: %w", err)
	}

	return os.Remove(c.getNarPathInStore(narURL))
}

// GetNarInfo returns the narInfo given a hash from the store. If the narInfo
// is not found in the store, it's pulled from an upstream, stored in the
// stored and finally returned.
func (c *Cache) GetNarInfo(ctx context.Context, hash string) (*narinfo.NarInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	log := c.logger.New("hash", hash)

	var (
		narInfo *narinfo.NarInfo
		err     error
	)

	if c.hasNarInfoInStore(log, hash) {
		narInfo, err = c.getNarInfoFromStore(ctx, log, hash)
		if err == nil {
			return narInfo, nil
		} else if !errors.Is(err, errNarInfoPurged) {
			return nil, fmt.Errorf("error fetching the narinfo from the store: %w", err)
		}
	}

	doneC := c.prePullNarInfo(log, hash)

	log.Debug("pulling nar in a go-routing and will wait for it")
	<-doneC

	if !c.hasNarInfoInStore(log, hash) {
		return nil, ErrNotFound
	}

	return c.getNarInfoFromStore(ctx, log, hash)
}

func (c *Cache) pullNarInfo(
	log log15.Logger,
	hash string,
	doneC chan struct{},
) {
	done := func() {
		c.muUpstreamJobs.Lock()
		delete(c.upstreamJobs, hash)
		c.muUpstreamJobs.Unlock()

		close(doneC)
	}

	now := time.Now()

	uc, narInfo, err := c.getNarInfoFromUpstream(log, hash)
	if err != nil {
		log.Error("error getting the narInfo from upstream caches", "error", err)

		done()

		return
	}

	narURL, err := nar.ParseURL(narInfo.URL)
	if err != nil {
		log.Error("error parsing the nar URL", "nar-url", narInfo.URL, "error", err)

		done()

		return
	}

	var enableZSTD bool

	if narInfo.Compression == nar.CompressionTypeNone.String() {
		log = log.New("zstd-enabled", true)
		enableZSTD = true
	} else {
		log = log.New("zstd-enabled", false)
	}

	// start a job to also pull the nar but don't wait for it to cme back
	narDoneC := c.prePullNar(log, narURL, enableZSTD, uc, narInfo)

	// Harmonia, for example, explicitly returns none for compression but does
	// accept encoding request, if that's the case we should get the compressed
	// version and store that instead.
	if enableZSTD {
		<-narDoneC
	}

	if err := c.signNarInfo(log, narInfo); err != nil {
		log.Error("error signing the narinfo", "error", err)

		done()

		return
	}

	if err := c.putNarInfoInStore(log, hash, narInfo); err != nil {
		log.Error("error storing the narInfo in the store", "error", err)

		done()

		return
	}

	if err := c.storeInDatabase(log, hash, narInfo); err != nil {
		log.Error("error storing the narinfo in the database", "error", err)

		done()

		return
	}

	log.Info("download of narinfo complete", "elapsed", time.Since(now))

	done()
}

// PutNarInfo records the narInfo (given as an io.Reader) into the store and signs it.
func (c *Cache) PutNarInfo(_ context.Context, hash string, r io.ReadCloser) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, r)

		r.Close()
	}()

	log := c.logger.New("hash", hash)

	narInfo, err := narinfo.Parse(r)
	if err != nil {
		return fmt.Errorf("error parsing narinfo: %w", err)
	}

	if err := c.signNarInfo(log, narInfo); err != nil {
		return fmt.Errorf("error signing the narinfo: %w", err)
	}

	if err := c.putNarInfoInStore(log, hash, narInfo); err != nil {
		return fmt.Errorf("error storing the narInfo in the store: %w", err)
	}

	return c.storeInDatabase(log, hash, narInfo)
}

// DeleteNarInfo deletes the narInfo from the store.
func (c *Cache) DeleteNarInfo(ctx context.Context, hash string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	log := c.logger.New("hash", hash)

	return c.deleteNarInfoFromStore(ctx, log, hash)
}

func (c *Cache) prePullNarInfo(log log15.Logger, hash string) chan struct{} {
	c.muUpstreamJobs.Lock()

	doneC, ok := c.upstreamJobs[hash]
	if ok {
		log.Info("waiting for an in-progress download of narinfo to finish")
	} else {
		doneC = make(chan struct{})
		c.upstreamJobs[hash] = doneC

		go c.pullNarInfo(log, hash, doneC)
	}
	c.muUpstreamJobs.Unlock()

	return doneC
}

func (c *Cache) prePullNar(
	log log15.Logger,
	narURL nar.URL,
	enableZSTD bool,
	uc *upstream.Cache,
	narInfo *narinfo.NarInfo,
) chan struct{} {
	c.muUpstreamJobs.Lock()

	doneC, ok := c.upstreamJobs[narURL.Hash]
	if !ok {
		doneC = make(chan struct{})
		c.upstreamJobs[narURL.Hash] = doneC

		go c.pullNar(log, enableZSTD, uc, narInfo, narURL, doneC)
	}
	c.muUpstreamJobs.Unlock()

	return doneC
}

func (c *Cache) signNarInfo(_ log15.Logger, narInfo *narinfo.NarInfo) error {
	sig, err := c.secretKey.Sign(nil, narInfo.Fingerprint())
	if err != nil {
		return fmt.Errorf("error signing the fingerprint: %w", err)
	}

	narInfo.Signatures = append(narInfo.Signatures, sig)

	return nil
}

func (c *Cache) hasNarInfoInStore(log log15.Logger, hash string) bool {
	return c.hasInStore(log, c.getNarInfoPathInStore(hash))
}

func (c *Cache) getNarInfoFromStore(ctx context.Context, log log15.Logger, hash string) (*narinfo.NarInfo, error) {
	_, r, err := c.getFromStore(log, c.getNarInfoPathInStore(hash))
	if err != nil {
		return nil, fmt.Errorf("error fetching the narinfo from the store: %w", err)
	}

	defer r.Close()

	ni, err := narinfo.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("error parsing the narinfo: %w", err)
	}

	narURL, err := nar.ParseURL(ni.URL)
	if err != nil {
		log.Error("error parsing the nar-url", "nar-url", ni.URL, "error", err)

		// narinfo is invalid, remove it
		if err := c.purgeNarInfo(ctx, log, hash, narURL); err != nil {
			log.Error("error purging the narinfo", "error", err)
		}

		return nil, errNarInfoPurged
	}

	log = narURL.NewLogger(log)

	if !c.hasNarInStore(log, narURL) && !c.hasUpstreamJob(narURL.Hash) {
		log.Error("narinfo was requested but no nar was found requesting a purge")

		if err := c.purgeNarInfo(ctx, log, hash, narURL); err != nil {
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
				log.Error("error rolling back the transaction", "error", err)
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

func (c *Cache) getNarInfoFromUpstream(log log15.Logger, hash string) (*upstream.Cache, *narinfo.NarInfo, error) {
	// create a new context not associated with any request because we don't want
	// downstream HTTP request to cancel this.
	ctx := context.Background()

	for _, uc := range c.upstreamCaches {
		narInfo, err := uc.GetNarInfo(ctx, hash)
		if err != nil {
			if !errors.Is(err, upstream.ErrNotFound) {
				log.Error("error fetching the narInfo from upstream", "hostname", uc.GetHostname(), "error", err)
			}

			continue
		}

		return nil, narInfo, nil
	}

	return nil, nil, ErrNotFound
}

func (c *Cache) purgeNarInfo(ctx context.Context, log log15.Logger, hash string, narURL nar.URL) error {
	tx, err := c.db.DB().Begin()
	if err != nil {
		return fmt.Errorf("error beginning a transaction: %w", err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil {
			if !errors.Is(err, sql.ErrTxDone) {
				log.Error("error rolling back the transaction", "error", err)
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

	if c.hasNarInfoInStore(log, hash) {
		if err := c.deleteNarInfoFromStore(ctx, log, hash); err != nil {
			return fmt.Errorf("error removing narinfo from store: %w", err)
		}
	}

	if narURL.Hash != "" {
		if c.hasNarInStore(log, narURL) {
			if err := c.deleteNarFromStore(log, narURL); err != nil {
				return fmt.Errorf("error removing nar from store: %w", err)
			}
		}
	}

	return nil
}

func (c *Cache) putNarInfoInStore(_ log15.Logger, hash string, narInfo *narinfo.NarInfo) error {
	f, err := os.CreateTemp(c.storeTMPPath(), hash+"-*.narinfo")
	if err != nil {
		return fmt.Errorf("error creating the temporary directory: %w", err)
	}

	if _, err := f.WriteString(narInfo.String()); err != nil {
		f.Close()
		os.Remove(f.Name())

		return fmt.Errorf("error writing the narinfo to the temporary file: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("error closing the temporary file: %w", err)
	}

	narInfoPath := c.getNarInfoPathInStore(hash)

	if err := os.MkdirAll(filepath.Dir(narInfoPath), 0o700); err != nil {
		return fmt.Errorf("error creating the directories for %q: %w", narInfoPath, err)
	}

	if err := os.Rename(f.Name(), narInfoPath); err != nil {
		return fmt.Errorf("error creating the narinfo file %q: %w", narInfoPath, err)
	}

	return nil
}

func (c *Cache) storeInDatabase(log log15.Logger, hash string, narInfo *narinfo.NarInfo) error {
	// create a new context not associated with any request because we don't want
	// downstream HTTP request to cancel this.
	ctx := context.Background()

	log = log.New("nar-url", narInfo.URL)

	log.Info("storing narinfo and nar record in the database")

	tx, err := c.db.DB().Begin()
	if err != nil {
		return fmt.Errorf("error beginning a transaction: %w", err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil {
			if !errors.Is(err, sql.ErrTxDone) {
				log.Error("error rolling back the transaction", "error", err)
			}
		}
	}()

	nir, err := c.db.WithTx(tx).CreateNarInfo(ctx, hash)
	if err != nil {
		if database.ErrorIsNo(err, sqlite3.ErrConstraint) {
			log.Warn("narinfo record was not added to database because it already exists")

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
			log.Warn("nar record was not added to database because it already exists")

			return nil
		}

		return fmt.Errorf("error inserting the nar record in the database: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing the transaction: %w", err)
	}

	return nil
}

func (c *Cache) deleteNarInfoFromStore(ctx context.Context, log log15.Logger, hash string) error {
	if !c.hasNarInfoInStore(log, hash) {
		return ErrNotFound
	}

	if _, err := c.db.DeleteNarInfoByHash(ctx, hash); err != nil {
		return fmt.Errorf("error deleting narinfo from the database: %w", err)
	}

	return os.Remove(c.getNarInfoPathInStore(hash))
}

func (c *Cache) hasInStore(_ log15.Logger, path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

// GetFile returns the file define by its key
// NOTE: It's the caller responsibility to close the file after using it.
func (c *Cache) getFromStore(_ log15.Logger, path string) (int64, io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, fmt.Errorf("error opening the file %q: %w", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return 0, nil, fmt.Errorf("error getting the stat for path %q: %w", path, err)
	}

	return info.Size(), f, nil
}

func (c *Cache) validateHostname(hostName string) error {
	if hostName == "" {
		c.logger.Error("given hostname is empty", "hostName", hostName)

		return ErrHostnameRequired
	}

	u, err := url.Parse(hostName)
	if err != nil {
		c.logger.Error("failed to parse the hostname", "hostName", hostName, "error", err)

		return fmt.Errorf("error parsing the hostName %q: %w", hostName, err)
	}

	if u.Scheme != "" {
		c.logger.Error("hostname should not contain a scheme", "hostName", hostName, "scheme", u.Scheme)

		return ErrHostnameMustNotContainScheme
	}

	if strings.Contains(hostName, "/") {
		c.logger.Error("hostname should not contain a path", "hostName", hostName)

		return ErrHostnameMustNotContainPath
	}

	return nil
}

func (c *Cache) validatePath(cachePath string) error {
	if !filepath.IsAbs(cachePath) {
		c.logger.Error("path is not absolute", "path", cachePath)

		return ErrPathMustBeAbsolute
	}

	info, err := os.Stat(cachePath)
	if errors.Is(err, fs.ErrNotExist) {
		c.logger.Error("path does not exist", "path", cachePath)

		return ErrPathMustExist
	}

	if !info.IsDir() {
		c.logger.Error("path is not a directory", "path", cachePath)

		return ErrPathMustBeADirectory
	}

	if !c.isWritable(cachePath) {
		return ErrPathMustBeWritable
	}

	return nil
}

func (c *Cache) isWritable(cachePath string) bool {
	tmpFile, err := os.CreateTemp(cachePath, "write_test")
	if err != nil {
		c.logger.Error("error writing a temp file in the path", "path", cachePath, "error", err)

		return false
	}

	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	return true
}

func (c *Cache) setup() error {
	if err := c.setupDirs(); err != nil {
		return fmt.Errorf("error setting up the cache directory: %w", err)
	}

	if err := c.setupDataBase(); err != nil {
		return fmt.Errorf("error setting up the database: %w", err)
	}

	return nil
}

func (c *Cache) setupDirs() error {
	if err := os.RemoveAll(c.storeTMPPath()); err != nil {
		return fmt.Errorf("error removing the temporary download directory: %w", err)
	}

	allPaths := []string{
		c.configPath(),
		c.storePath(),
		c.storeNarInfoPath(),
		c.storeNarPath(),
		c.storeTMPPath(),
		c.dbDirPath(),
	}

	for _, p := range allPaths {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return fmt.Errorf("error creating the directory %q: %w", p, err)
		}
	}

	return nil
}

func (c *Cache) configPath() string       { return filepath.Join(c.path, "config") }
func (c *Cache) secretKeyPath() string    { return filepath.Join(c.configPath(), "cache.key") }
func (c *Cache) storePath() string        { return filepath.Join(c.path, "store") }
func (c *Cache) storeNarInfoPath() string { return filepath.Join(c.storePath(), "narinfo") }
func (c *Cache) storeNarPath() string     { return filepath.Join(c.storePath(), "nar") }
func (c *Cache) storeTMPPath() string     { return filepath.Join(c.storePath(), "tmp") }
func (c *Cache) dbDirPath() string        { return filepath.Join(c.path, "var", "ncps", "db") }
func (c *Cache) dbKeyPath() string        { return filepath.Join(c.dbDirPath(), "db.sqlite") }

func (c *Cache) setupDataBase() error {
	db, err := database.Open(c.dbKeyPath())
	if err != nil {
		return fmt.Errorf("error opening the database %q: %w", c.dbKeyPath(), err)
	}

	c.db = db

	return nil
}

func (c *Cache) setupSecretKey() (signature.SecretKey, error) {
	f, err := os.Open(c.secretKeyPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c.createNewKey()
		}

		return signature.SecretKey{}, fmt.Errorf("error reading the secret key from %q: %w", c.secretKeyPath(), err)
	}
	defer f.Close()

	skc, err := io.ReadAll(f)
	if err != nil {
		return signature.SecretKey{}, fmt.Errorf("error reading the secret key from %q: %w", c.secretKeyPath(), err)
	}

	sk, err := signature.LoadSecretKey(string(skc))
	if err != nil {
		return signature.SecretKey{}, fmt.Errorf("error loading the secret key: %w", err)
	}

	return sk, nil
}

func (c *Cache) createNewKey() (signature.SecretKey, error) {
	if err := os.MkdirAll(filepath.Dir(c.secretKeyPath()), 0o700); err != nil {
		return signature.SecretKey{}, fmt.Errorf("error creating the parent directories for %q: %w", c.secretKeyPath(), err)
	}

	secretKey, _, err := signature.GenerateKeypair(c.hostName, nil)
	if err != nil {
		return secretKey, fmt.Errorf("error generating a new secret key: %w", err)
	}

	f, err := os.Create(c.secretKeyPath())
	if err != nil {
		return secretKey, fmt.Errorf("error creating the cache key file %q: %w", c.secretKeyPath(), err)
	}

	defer f.Close()

	if _, err := f.WriteString(secretKey.String()); err != nil {
		return secretKey, fmt.Errorf("error writing the secret key to %q: %w", c.secretKeyPath(), err)
	}

	return secretKey, nil
}

func (c *Cache) hasUpstreamJob(hash string) bool {
	c.muUpstreamJobs.Lock()
	_, ok := c.upstreamJobs[hash]
	c.muUpstreamJobs.Unlock()

	return ok
}

func (c *Cache) runLRU() {
	c.mu.Lock()
	defer c.mu.Unlock()

	log := c.logger.New("op", "lru", "max-size", c.maxSize)
	log.Info("running LRU")

	// TODO: Possibly trickle ctx down
	ctx := context.Background()

	tx, err := c.db.DB().Begin()
	if err != nil {
		log.Error("error beginning a transaction", "error", err)

		return
	}

	defer func() {
		if err := tx.Rollback(); err != nil {
			if !errors.Is(err, sql.ErrTxDone) {
				log.Error("error rolling back the transaction", "error", err)
			}
		}
	}()

	narTotalSize, err := c.db.WithTx(tx).GetNarTotalSize(ctx)
	if err != nil {
		log.Error("error fetching the total nar size", "error", err)

		return
	}

	if !narTotalSize.Valid {
		log.Error("SUM(file_size) returned NULL")

		return
	}

	log = log.New("nar-total-size", narTotalSize)

	if uint64(narTotalSize.Float64) <= c.maxSize {
		log.Info("store size is less than max-size, not removing any nars")

		return
	}

	cleanupSize := uint64(narTotalSize.Float64) - c.maxSize

	log = log.New("cleanup-size", cleanupSize)

	log.Info("going to remove nars")

	nars, err := c.db.WithTx(tx).GetLeastUsedNars(ctx, cleanupSize)
	if err != nil {
		log.Error("error getting the least used nars up to cleanup-size", "error", err)

		return
	}

	if len(nars) == 0 {
		log.Warn("nars needed to be removed but none were returned in the query")

		return
	}

	log.Info("found this many nars to remove", "count-nars", len(nars))

	filesToRemove := make([]string, 0, 2*len(nars))

	for _, narRecord := range nars {
		narInfo, err := c.db.WithTx(tx).GetNarInfoByID(ctx, narRecord.NarInfoID)
		if err == nil {
			log.Info("deleting narinfo record", "narinfo-hash", narInfo.Hash)

			if _, err := c.db.WithTx(tx).DeleteNarInfoByHash(ctx, narInfo.Hash); err != nil {
				log.Error("error removing narinfo from database", "hash", narInfo.Hash, "error", err)
			}

			filesToRemove = append(filesToRemove,
				c.getNarInfoPathInStore(narInfo.Hash),
			)
		} else {
			log.Error("error fetching narinfo from the database", "ID", narRecord.NarInfoID, "error", err)
		}

		log.Info("deleting nar record", "nar-hash", narRecord.Hash)

		if _, err := c.db.WithTx(tx).DeleteNarByHash(ctx, narRecord.Hash); err != nil {
			log.Error("error removing nar from database", "hash", narRecord.Hash, "error", err)
		}

		filesToRemove = append(filesToRemove,
			// NOTE: we don't need the query when working with store so it's
			// explicitly omitted.
			c.getNarPathInStore(nar.URL{
				Hash:        narRecord.Hash,
				Compression: nar.CompressionTypeFromString(narRecord.Compression),
			}),
		)
	}

	// remove all the files from the store as fast as possible.

	var wg sync.WaitGroup

	for _, f := range filesToRemove {
		wg.Add(1)

		go func() {
			defer wg.Done()

			log.Info("deleting file from store", "path", f)

			if err := os.Remove(f); err != nil {
				log.Error("error removing the file", "file-to-remove", f, "error", err)
			}
		}()
	}

	wg.Wait()

	// finally commit the database transaction

	if err := tx.Commit(); err != nil {
		log.Error("error committing the transaction", "error", err)
	}
}

func zstdMutator(log log15.Logger, compression nar.CompressionType) func(r *http.Request) {
	return func(r *http.Request) {
		log.Info("narinfo compress is none will set Accept-Encoding to zstd")
		r.Header.Set("Accept-Encoding", "zstd")

		cfe := compression.ToFileExtension()
		if cfe != "" {
			cfe = "." + cfe
		}

		r.URL.Path = strings.Replace(
			r.URL.Path,
			"."+nar.CompressionTypeZstd.ToFileExtension(),
			cfe,
			-1,
		)
	}
}
