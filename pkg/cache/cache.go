package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/inconshreveable/log15/v3"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
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
	db             *database.DB

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

// AddUpstreamCaches adds one or more upstream caches
func (s *Cache) AddUpstreamCaches(ucs ...upstream.Cache) {
	ucss := append(s.upstreamCaches, ucs...)

	slices.SortFunc(ucss, func(a, b upstream.Cache) int {
		//nolint:gosec
		return int(a.GetPriority() - b.GetPriority())
	})

	s.logger.Info("the order of upstream caches has been determined by priority to be")

	for idx, uc := range ucss {
		s.logger.Info("upstream cache", "idx", idx, "hostname", uc.GetHostname(), "priority", uc.GetPriority())
	}

	s.upstreamCaches = ucss
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
func (c *Cache) GetNar(hash, compression string) (int64, io.ReadCloser, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	log := c.logger.New("hash", hash, "compression", compression)

	if c.hasNarInStore(log, hash, compression) {
		return c.getNarFromStore(log, hash, compression)
	}

	errC := make(chan error)

	c.muUpstreamJobs.Lock()

	doneC, ok := c.upstreamJobs[hash]
	if ok {
		log.Info("waiting for an in-progress download to finish")
	} else {
		doneC = make(chan struct{})
		c.upstreamJobs[hash] = doneC

		go c.pullNar(log, hash, compression, doneC, errC)
	}
	c.muUpstreamJobs.Unlock()

	select {
	case err := <-errC:
		close(doneC) // notify other go-routines waiting on the done channel.

		return 0, nil, err
	case <-doneC:
	}

	if !c.hasNarInStore(log, hash, compression) {
		return 0, nil, ErrNotFound
	}

	return c.getNarFromStore(log, hash, compression)
}

// PutNar records the NAR (given as an io.Reader) into the store.
func (c *Cache) PutNar(_ context.Context, hash, compression string, r io.ReadCloser) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, r)

		r.Close()
	}()

	log := c.logger.New("hash", hash, "compression", compression)

	_, err := c.putNarInStore(log, hash, compression, r)

	return err
}

// DeleteNar deletes the nar from the store.
func (c *Cache) DeleteNar(_ context.Context, hash, compression string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	log := c.logger.New("hash", hash, "compression", compression)

	return c.deleteNarFromStore(log, hash, compression)
}

func (c *Cache) pullNar(log log15.Logger, hash, compression string, doneC chan struct{}, errC chan error) {
	now := time.Now()

	log.Info("downloading the nar from upstream")

	size, r, err := c.getNarFromUpstream(log, hash, compression)
	if err != nil {
		c.muUpstreamJobs.Lock()
		delete(c.upstreamJobs, hash)
		c.muUpstreamJobs.Unlock()

		errC <- fmt.Errorf("error getting the narInfo from upstream caches: %w", err)

		return
	}

	defer r.Close()

	written, err := c.putNarInStore(log, hash, compression, r)
	if err != nil {
		c.muUpstreamJobs.Lock()
		delete(c.upstreamJobs, hash)
		c.muUpstreamJobs.Unlock()

		errC <- fmt.Errorf("error storing the narInfo in the store: %w", err)

		return
	}

	log.Info("download complete", "elapsed", time.Since(now))

	if size > 0 && written != size {
		log.Error("bytes written is not the same as Content-Length", "Content-Length", size, "written", written)
	}

	c.muUpstreamJobs.Lock()
	delete(c.upstreamJobs, hash)
	c.muUpstreamJobs.Unlock()

	close(doneC)
}

func (c *Cache) getNarPathInStore(hash, compression string) string {
	return filepath.Join(c.storePath(), helper.NarPath(hash, compression))
}

func (c *Cache) getNarInfoPathInStore(hash string) string {
	return filepath.Join(c.storePath(), helper.NarInfoPath(hash))
}

func (c *Cache) hasNarInStore(log log15.Logger, hash, compression string) bool {
	return c.hasInStore(log, c.getNarPathInStore(hash, compression))
}

func (c *Cache) getNarFromStore(log log15.Logger, hash, compression string) (int64, io.ReadCloser, error) {
	size, r, err := c.getFromStore(log, c.getNarPathInStore(hash, compression))
	if err != nil {
		return 0, nil, fmt.Errorf("error fetching the narinfo from the store: %w", err)
	}

	tx, err := c.db.Begin()
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

	nr, err := c.db.GetNarRecord(tx, hash)
	if err != nil {
		// TODO: If record not found, record it instead!
		if errors.Is(err, database.ErrNotFound) {
			return size, r, nil
		}

		return 0, nil, fmt.Errorf("error fetching the nar record: %w", err)
	}

	if time.Since(nr.LastAccessedAt) > c.recordAgeIgnoreTouch {
		if _, err := c.db.TouchNarRecord(tx, hash); err != nil {
			return 0, nil, fmt.Errorf("error touching the nar record: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, nil, fmt.Errorf("error committing the transaction: %w", err)
	}

	return size, r, nil
}

func (c *Cache) getNarFromUpstream(log log15.Logger, hash, compression string) (int64, io.ReadCloser, error) {
	// create a new context not associated with any request because we don't want
	// pulling from upstream to be associated with a user request.
	ctx := context.Background()

	for _, uc := range c.upstreamCaches {
		size, nar, err := uc.GetNar(ctx, hash, compression)
		if err != nil {
			if !errors.Is(err, upstream.ErrNotFound) {
				log.Error("error fetching the narInfo from upstream", "hostname", uc.GetHostname(), "error", err)
			}

			continue
		}

		return size, nar, nil
	}

	return 0, nil, ErrNotFound
}

func (c *Cache) putNarInStore(_ log15.Logger, hash, compression string, r io.ReadCloser) (int64, error) {
	pattern := hash + "-*.nar"
	if compression != "" {
		pattern += "." + compression
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

	narPath := c.getNarPathInStore(hash, compression)

	if err := os.Rename(f.Name(), narPath); err != nil {
		return 0, fmt.Errorf("error creating the nar file %q: %w", narPath, err)
	}

	return written, nil
}

func (c *Cache) deleteNarFromStore(log log15.Logger, hash, compression string) error {
	if !c.hasNarInStore(log, hash, compression) {
		return ErrNotFound
	}

	return os.Remove(c.getNarPathInStore(hash, compression))
}

// GetNarInfo returns the narInfo given a hash from the store. If the narInfo
// is not found in the store, it's pulled from an upstream, stored in the
// stored and finally returned.
func (c *Cache) GetNarInfo(hash string) (*narinfo.NarInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	log := c.logger.New("hash", hash)

	var (
		narInfo *narinfo.NarInfo
		err     error
	)

	if c.hasNarInfoInStore(log, hash) {
		narInfo, err = c.getNarInfoFromStore(log, hash)
		if err == nil {
			return narInfo, nil
		} else if !errors.Is(err, errNarInfoPurged) {
			return nil, fmt.Errorf("error fetching the narinfo from the store: %w", err)
		}
	}

	narInfo, err = c.getNarInfoFromUpstream(log, hash)
	if err != nil {
		return nil, fmt.Errorf("error getting the narInfo from upstream caches: %w", err)
	}

	// start a job to also pull the nar
	go c.prePullNar(log, narInfo.URL)

	if err := c.signNarInfo(log, narInfo); err != nil {
		return nil, fmt.Errorf("error signing the narinfo: %w", err)
	}

	if err := c.putNarInfoInStore(log, hash, narInfo); err != nil {
		return nil, fmt.Errorf("error storing the narInfo in the store: %w", err)
	}

	return narInfo, c.storeInDatabase(log, hash, narInfo)
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
func (c *Cache) DeleteNarInfo(_ context.Context, hash string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	log := c.logger.New("hash", hash)

	return c.deleteNarInfoFromStore(log, hash)
}

func (c *Cache) prePullNar(log log15.Logger, url string) {
	hash, compression, err := helper.ParseNarURL(url)
	if err != nil {
		c.logger.Error("error parsing the nar URL", "url", url, "error", err)

		return
	}

	log = log.New("hash", hash, "compression", compression)

	log.Info("pre-caching NAR ahead of time", "URL", url)

	_, nar, err := c.GetNar(hash, compression)
	if err != nil {
		log.Error("error fetching the NAR", "error", err)

		return
	}

	nar.Close()
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

func (c *Cache) getNarInfoFromStore(log log15.Logger, hash string) (*narinfo.NarInfo, error) {
	_, r, err := c.getFromStore(log, c.getNarInfoPathInStore(hash))
	if err != nil {
		return nil, fmt.Errorf("error fetching the narinfo from the store: %w", err)
	}

	defer r.Close()

	ni, err := narinfo.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("error parsing the narinfo: %w", err)
	}

	narHash, narCompression, err := helper.ParseNarURL(ni.URL)
	if err != nil {
		// narinfo is invalid, remove it
		if err := c.purgeNarInfo(log, hash, "", ""); err != nil {
			log.Error("error purging the narinfo", "error", err)
		}

		return nil, errNarInfoPurged
	}

	log = log.New("nar-hash", narHash, "nar-compression", narCompression)

	if !c.hasNarInStore(log, narHash, narCompression) && !c.hasUpstreamJob(hash) {
		if err := c.purgeNarInfo(log, hash, narHash, narCompression); err != nil {
			return nil, fmt.Errorf("error purging the narinfo: %w", err)
		}

		return nil, errNarInfoPurged
	}

	tx, err := c.db.Begin()
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

	nir, err := c.db.GetNarInfoRecord(tx, hash)
	if err != nil {
		// TODO: If record not found, record it instead!
		if errors.Is(err, database.ErrNotFound) {
			return ni, nil
		}

		return nil, fmt.Errorf("error fetching the narinfo record: %w", err)
	}

	if time.Since(nir.LastAccessedAt) > c.recordAgeIgnoreTouch {
		if _, err := c.db.TouchNarInfoRecord(tx, hash); err != nil {
			return nil, fmt.Errorf("error touching the narinfo record: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("error committing the transaction: %w", err)
	}

	return ni, nil
}

func (c *Cache) getNarInfoFromUpstream(log log15.Logger, hash string) (*narinfo.NarInfo, error) {
	// create a new context not associated with any request because we don't want
	// pulling from upstream to be associated with a user request.
	ctx := context.Background()

	for _, uc := range c.upstreamCaches {
		narInfo, err := uc.GetNarInfo(ctx, hash)
		if err != nil {
			if !errors.Is(err, upstream.ErrNotFound) {
				log.Error("error fetching the narInfo from upstream", "hostname", uc.GetHostname(), "error", err)
			}

			continue
		}

		return narInfo, nil
	}

	return nil, ErrNotFound
}

func (c *Cache) purgeNarInfo(log log15.Logger, hash, narHash, narCompression string) error {
	tx, err := c.db.Begin()
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

	if err := c.db.DeleteNarInfoRecord(tx, hash); err != nil {
		return fmt.Errorf("error deleting the narinfo record: %w", err)
	}

	if narHash != "" {
		if err := c.db.DeleteNarRecord(tx, narHash); err != nil {
			return fmt.Errorf("error deleting the nar record: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing the transaction: %w", err)
	}

	if c.hasNarInfoInStore(log, hash) {
		if err := c.deleteNarInfoFromStore(log, hash); err != nil {
			return fmt.Errorf("error removing narinfo from store: %w", err)
		}
	}

	if narHash != "" {
		if c.hasNarInStore(log, narHash, narCompression) {
			if err := c.deleteNarFromStore(log, narHash, narCompression); err != nil {
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

	if err := os.Rename(f.Name(), narInfoPath); err != nil {
		return fmt.Errorf("error creating the narinfo file %q: %w", narInfoPath, err)
	}

	return nil
}

func (c *Cache) storeInDatabase(log log15.Logger, hash string, narInfo *narinfo.NarInfo) error {
	log = log.New("nar-url", narInfo.URL)

	log.Info("storing narinfo and nar record in the database")

	tx, err := c.db.Begin()
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

	res, err := c.db.InsertNarInfoRecord(tx, hash)
	if err != nil {
		if errors.Is(err, database.ErrAlreadyExists) {
			log.Warn("narinfo record was not added to database because it already exists")

			return nil
		}

		return fmt.Errorf("error inserting the narinfo record for hash %q in the database: %w", hash, err)
	}

	lid, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("error fetching the last insert ID of the narinfo with hash %q: %w", hash, err)
	}

	narHash, compression, err := helper.ParseNarURL(narInfo.URL)
	if err != nil {
		return fmt.Errorf("error parsing the nar URL: %w", err)
	}

	if _, err := c.db.InsertNarRecord(tx, lid, narHash, compression, narInfo.FileSize); err != nil {
		if errors.Is(err, database.ErrAlreadyExists) {
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

func (c *Cache) deleteNarInfoFromStore(log log15.Logger, hash string) error {
	if !c.hasNarInfoInStore(log, hash) {
		return ErrNotFound
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

func (c *Cache) configPath() string    { return filepath.Join(c.path, "config") }
func (c *Cache) secretKeyPath() string { return filepath.Join(c.configPath(), "cache.key") }
func (c *Cache) storePath() string     { return filepath.Join(c.path, "store") }
func (c *Cache) storeNarPath() string  { return filepath.Join(c.storePath(), "nar") }
func (c *Cache) storeTMPPath() string  { return filepath.Join(c.storePath(), "tmp") }
func (c *Cache) dbDirPath() string     { return filepath.Join(c.path, "var", "ncps", "db") }
func (c *Cache) dbKeyPath() string     { return filepath.Join(c.dbDirPath(), "db.sqlite") }

func (c *Cache) setupDataBase() error {
	db, err := database.Open(c.logger, c.dbKeyPath())
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
