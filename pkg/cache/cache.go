package cache

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

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
)

// Cache represents the main cache service.
type Cache struct {
	hostName       string
	logger         log15.Logger
	path           string
	secretKey      signature.SecretKey
	upstreamCaches []upstream.Cache
}

// New returns a new Cache.
func New(logger log15.Logger, hostName, cachePath string, ucs []upstream.Cache) (Cache, error) {
	c := Cache{logger: logger, upstreamCaches: ucs}

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

	return c, nil
}

// PublicKey returns the public key of the server.
func (c Cache) PublicKey() string { return c.secretKey.ToPublicKey().String() }

// GetNarInfo returns the narInfo given a hash from the store. If the narInfo
// is not found in the store, it's pulled from an upstream, stored in the
// stored and finally returned.
func (c Cache) GetNarInfo(hash string) (*narinfo.NarInfo, error) {
	if c.hasNarInfoInStore(hash) {
		return c.getNarInfoFromStore(hash)
	}

	narInfo, err := c.getNarInfoFromUpstream(hash)
	if err != nil {
		return nil, fmt.Errorf("error getting the narInfo from upstream caches: %w", err)
	}

	if err := c.putNarInfoInStore(hash, narInfo); err != nil {
		return nil, fmt.Errorf("error storing the narInfo in the store: %w", err)
	}

	return narInfo, nil
}

func (c Cache) hasNarInfoInStore(hash string) bool {
	return c.hasInStore(helper.NarInfoPath(hash))
}

func (c Cache) getNarInfoFromStore(hash string) (*narinfo.NarInfo, error) {
	r, err := c.getFromStore(helper.NarInfoPath(hash))
	if err != nil {
		return nil, fmt.Errorf("error fetching the narinfo from the store: %w", err)
	}

	defer r.Close()

	return narinfo.Parse(r)
}

func (c Cache) getNarInfoFromUpstream(hash string) (*narinfo.NarInfo, error) {
	return nil, errors.New("not implemented")
}

func (c Cache) putNarInfoInStore(hash string, narInfo *narinfo.NarInfo) error {
	return errors.New("not implemented")
}

func (c Cache) hasInStore(key string) bool {
	_, err := os.Stat(filepath.Join(c.storePath(), key))
	return err == nil
}

// GetFile retuns the file define by its key
// NOTE: It's the caller responsability to close the file after using it
func (c Cache) getFromStore(key string) (io.ReadCloser, error) {
	f, err := os.Open(filepath.Join(c.storePath(), key))
	if err != nil {
		return nil, fmt.Errorf("error opening the file %q: %w", key, err)
	}

	return f, nil
}

func (c Cache) validateHostname(hostName string) error {
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

func (c Cache) validatePath(cachePath string) error {
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

func (c Cache) isWritable(cachePath string) bool {
	tmpFile, err := os.CreateTemp(cachePath, "write_test")
	if err != nil {
		c.logger.Error("error writing a temp file in the path", "path", cachePath, "error", err)

		return false
	}

	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	return true
}

func (c Cache) storePath() string     { return filepath.Join(c.path, "store") }
func (c Cache) configPath() string    { return filepath.Join(c.path, "config") }
func (c Cache) secretKeyPath() string { return filepath.Join(c.configPath(), "cache.key") }

func (c Cache) setupSecretKey() (signature.SecretKey, error) {
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

func (c Cache) createNewKey() (signature.SecretKey, error) {
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
