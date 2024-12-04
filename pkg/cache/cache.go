package cache

import (
	"context"
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
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
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
)

// Cache represents the main cache service.
type Cache struct {
	hostName       string
	logger         log15.Logger
	path           string
	secretKey      signature.SecretKey
	upstreamCaches []upstream.Cache

	mu           sync.Mutex
	upstreamJobs map[string]chan struct{}
}

// New returns a new Cache.
func New(logger log15.Logger, hostName, cachePath string, ucs []upstream.Cache) (*Cache, error) {
	c := &Cache{
		logger:         logger,
		upstreamCaches: ucs,
		upstreamJobs:   make(map[string]chan struct{}),
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

	slices.SortFunc(c.upstreamCaches, func(a, b upstream.Cache) int {
		//nolint:gosec
		return int(a.GetPriority() - b.GetPriority())
	})

	logger.Info("the order of upstream caches has been determined by priority to be")

	for idx, uc := range c.upstreamCaches {
		logger.Info("upstream cache", "idx", idx, "hostname", hostName, "priority", uc.GetPriority())
	}

	return c, c.setupDirs()
}

// GetHostname returns the hostname.
func (c *Cache) GetHostname() string { return c.hostName }

// PublicKey returns the public key of the server.
func (c *Cache) PublicKey() signature.PublicKey { return c.secretKey.ToPublicKey() }

// GetNar returns the nar given a hash and compression from the store. If the
// nar is not found in the store, it's pulled from an upstream, stored in the
// stored and finally returned.
// NOTE: It's the caller responsibility to close the body.
func (c *Cache) GetNar(hash, compression string) (int64, io.ReadCloser, error) {
	if c.hasNarInStore(hash, compression) {
		return c.getNarFromStore(hash, compression)
	}

	log := c.logger.New("hash", hash, "compression", compression)

	errC := make(chan error)

	c.mu.Lock()

	doneC, ok := c.upstreamJobs[hash]
	if ok {
		log.Info("waiting for an in-progress download to finish")
	} else {
		doneC = make(chan struct{})
		c.upstreamJobs[hash] = doneC

		go c.pullNar(log, hash, compression, doneC, errC)
	}
	c.mu.Unlock()

	select {
	case err := <-errC:
		close(doneC) // notify other go-routines waiting on the done channel.

		return 0, nil, err
	case <-doneC:
	}

	if !c.hasNarInStore(hash, compression) {
		return 0, nil, ErrNotFound
	}

	return c.getNarFromStore(hash, compression)
}

func (c *Cache) pullNar(log log15.Logger, hash, compression string, doneC chan struct{}, errC chan error) {
	now := time.Now()

	log.Info("downloading the nar from upstream")

	size, r, err := c.getNarFromUpstream(hash, compression)
	if err != nil {
		c.mu.Lock()
		delete(c.upstreamJobs, hash)
		c.mu.Unlock()

		errC <- fmt.Errorf("error getting the narInfo from upstream caches: %w", err)

		return
	}

	defer r.Close()

	written, err := c.putNarInStore(hash, compression, r)
	if err != nil {
		c.mu.Lock()
		delete(c.upstreamJobs, hash)
		c.mu.Unlock()

		errC <- fmt.Errorf("error storing the narInfo in the store: %w", err)

		return
	}

	log.Info("download complete", "elapsed", time.Since(now))

	if size > 0 && written != size {
		log.Error("bytes written is not the same as Content-Length", "Content-Length", size, "written", written)
	}

	c.mu.Lock()
	delete(c.upstreamJobs, hash)
	c.mu.Unlock()

	close(doneC)
}

func (c *Cache) hasNarInStore(hash, compression string) bool {
	return c.hasInStore(helper.NarPath(hash, compression))
}

func (c *Cache) getNarFromStore(hash, compression string) (int64, io.ReadCloser, error) {
	return c.getFromStore(helper.NarPath(hash, compression))
}

func (c *Cache) getNarFromUpstream(hash, compression string) (int64, io.ReadCloser, error) {
	// create a new context not associated with any request because we don't want
	// pulling from upstream to be associated with a user request.
	ctx := context.Background()

	for _, uc := range c.upstreamCaches {
		size, nar, err := uc.GetNar(ctx, hash, compression)
		if err != nil {
			if !errors.Is(err, upstream.ErrNotFound) {
				c.logger.Error("error fetching the narInfo from upstream", "hostname", uc.GetHostname(), "error", err)
			}

			continue
		}

		return size, nar, nil
	}

	return 0, nil, ErrNotFound
}

func (c *Cache) putNarInStore(hash, compression string, r io.ReadCloser) (int64, error) {
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

	narPath := filepath.Join(c.storePath(), helper.NarPath(hash, compression))

	if err := os.Rename(f.Name(), narPath); err != nil {
		return 0, fmt.Errorf("error creating the nar file %q: %w", narPath, err)
	}

	return written, nil
}

// GetNarInfo returns the narInfo given a hash from the store. If the narInfo
// is not found in the store, it's pulled from an upstream, stored in the
// stored and finally returned.
func (c *Cache) GetNarInfo(ctx context.Context, hash string) (*narinfo.NarInfo, error) {
	if c.hasNarInfoInStore(hash) {
		return c.getNarInfoFromStore(hash)
	}

	narInfo, err := c.getNarInfoFromUpstream(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("error getting the narInfo from upstream caches: %w", err)
	}

	// start a job to also pull the nar
	go c.prePullNar(narInfo.URL)

	if err := c.signNarInfo(narInfo); err != nil {
		return nil, fmt.Errorf("error signing the narinfo: %w", err)
	}

	if err := c.putNarInfoInStore(hash, narInfo); err != nil {
		return nil, fmt.Errorf("error storing the narInfo in the store: %w", err)
	}

	return narInfo, nil
}

func (c *Cache) prePullNar(url string) {
	hash, compression, err := helper.ParseNarURL(url)
	if err != nil {
		c.logger.Error("error parsing the nar URL %q: %w", url, err)

		return
	}

	c.logger.Info("pre-caching NAR ahead of time", "URL", url, "hash", hash, "compression", compression)

	_, nar, err := c.GetNar(hash, compression)
	if err != nil {
		c.logger.Error("error fetching the NAR: %w", err)

		return
	}

	nar.Close()
}

func (c *Cache) signNarInfo(narInfo *narinfo.NarInfo) error {
	sig, err := c.secretKey.Sign(nil, narInfo.Fingerprint())
	if err != nil {
		return fmt.Errorf("error signing the fingerprint: %w", err)
	}

	narInfo.Signatures = append(narInfo.Signatures, sig)

	return nil
}

func (c *Cache) hasNarInfoInStore(hash string) bool {
	return c.hasInStore(helper.NarInfoPath(hash))
}

func (c *Cache) getNarInfoFromStore(hash string) (*narinfo.NarInfo, error) {
	_, r, err := c.getFromStore(helper.NarInfoPath(hash))
	if err != nil {
		return nil, fmt.Errorf("error fetching the narinfo from the store: %w", err)
	}

	defer r.Close()

	return narinfo.Parse(r)
}

func (c *Cache) getNarInfoFromUpstream(ctx context.Context, hash string) (*narinfo.NarInfo, error) {
	for _, uc := range c.upstreamCaches {
		narInfo, err := uc.GetNarInfo(ctx, hash)
		if err != nil {
			if !errors.Is(err, upstream.ErrNotFound) {
				c.logger.Error("error fetching the narInfo from upstream", "hostname", uc.GetHostname(), "error", err)
			}

			continue
		}

		return narInfo, nil
	}

	return nil, ErrNotFound
}

func (c *Cache) putNarInfoInStore(hash string, narInfo *narinfo.NarInfo) error {
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

	narInfoPath := filepath.Join(c.storePath(), helper.NarInfoPath(hash))

	if err := os.Rename(f.Name(), narInfoPath); err != nil {
		return fmt.Errorf("error creating the narinfo file %q: %w", narInfoPath, err)
	}

	return nil
}

func (c *Cache) hasInStore(key string) bool {
	_, err := os.Stat(filepath.Join(c.storePath(), key))

	return err == nil
}

// GetFile returns the file define by its key
// NOTE: It's the caller responsibility to close the file after using it.
func (c *Cache) getFromStore(key string) (int64, io.ReadCloser, error) {
	p := filepath.Join(c.storePath(), key)

	f, err := os.Open(p)
	if err != nil {
		return 0, nil, fmt.Errorf("error opening the file %q: %w", key, err)
	}

	info, err := os.Stat(p)
	if err != nil {
		return 0, nil, fmt.Errorf("error getting the stat for path %q: %w", p, err)
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

func (c *Cache) setupDirs() error {
	if err := os.RemoveAll(c.storeTMPPath()); err != nil {
		return fmt.Errorf("error removing the temporary download directory: %w", err)
	}

	allPaths := []string{
		c.configPath(),
		c.storePath(),
		c.storeNarPath(),
		c.storeTMPPath(),
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
