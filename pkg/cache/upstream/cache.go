package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/nixcacheinfo"
)

const (
	otelPackageName = "github.com/kalbasit/ncps/pkg/cache/upstream"

	defaultHTTPTimeout = 3 * time.Second
)

var (
	// ErrURLRequired is returned if the given URL to New is not given.
	ErrURLRequired = errors.New("the URL is required")

	// ErrURLMustContainScheme is returned if the given URL to New did not contain a scheme.
	ErrURLMustContainScheme = errors.New("the URL must contain scheme")

	// ErrInvalidURL is returned if the given hostName to New is not valid.
	ErrInvalidURL = errors.New("the URL is not valid")

	// ErrNotFound is returned if the nar or narinfo were not found.
	ErrNotFound = errors.New("not found")

	// ErrUnexpectedHTTPStatusCode is returned if the response has an unexpected status code.
	ErrUnexpectedHTTPStatusCode = errors.New("unexpected HTTP status code")

	// ErrSignatureValidationFailed is returned if the signature validation of the narinfo has failed.
	ErrSignatureValidationFailed = errors.New("signature validation has failed")

	// ErrTransportCastError is returned if it was not possible to cast http.DefaultTransport to *http.Transport.
	ErrTransportCastError = errors.New("unable to cast http.DefaultTransport to *http.Transport")

	//nolint:gochecknoglobals
	tracer trace.Tracer
)

//nolint:gochecknoinits
func init() {
	tracer = otel.Tracer(otelPackageName)
}

// Cache represents the upstream cache service.
type Cache struct {
	httpClient *http.Client
	url        *url.URL
	priority   uint64
	publicKeys []signature.PublicKey
	netrcAuth  *NetrcCredentials

	mu        sync.RWMutex
	isHealthy bool

	dialerTimeout         time.Duration
	responseHeaderTimeout time.Duration
}

// NetrcCredentials holds authentication credentials.
type NetrcCredentials struct {
	Username string
	Password string
}

// Options contains optional configuration for creating an upstream cache.
type Options struct {
	// PublicKeys is a list of public keys for verifying signatures.
	// If empty, signature verification will be skipped.
	PublicKeys []string

	// NetrcCredentials holds authentication credentials for the upstream cache.
	// If nil, no authentication will be used.
	NetrcCredentials *NetrcCredentials

	// DialerTimeout is the timeout for establishing a TCP connection.
	// If zero, defaults to defaultHTTPTimeout (3s).
	DialerTimeout time.Duration

	// ResponseHeaderTimeout is the timeout for waiting for the server's response headers.
	// If zero, defaults to defaultHTTPTimeout (3s).
	ResponseHeaderTimeout time.Duration
}

// New creates a new upstream cache with the given URL and options.
// Pass nil for opts to use default values.
func New(ctx context.Context, u *url.URL, opts *Options) (*Cache, error) {
	if u == nil {
		return nil, ErrURLRequired
	}

	// Set default timeouts and override if options are provided
	dialerTimeout := defaultHTTPTimeout
	responseHeaderTimeout := defaultHTTPTimeout

	if opts != nil {
		if opts.DialerTimeout > 0 {
			dialerTimeout = opts.DialerTimeout
		}

		if opts.ResponseHeaderTimeout > 0 {
			responseHeaderTimeout = opts.ResponseHeaderTimeout
		}
	}

	c := &Cache{
		url:                   u,
		dialerTimeout:         dialerTimeout,
		responseHeaderTimeout: responseHeaderTimeout,
	}

	if opts.NetrcCredentials != nil {
		c.netrcAuth = opts.NetrcCredentials

		zerolog.Ctx(ctx).
			Info().
			Str("hostname", c.url.Hostname()).
			Str("username", opts.NetrcCredentials.Username).
			Msg("loaded netrc authentication credentials")
	}

	if err := c.setupHTTPClient(); err != nil {
		return nil, err
	}

	zerolog.Ctx(ctx).
		Debug().
		Str("upstream_url", c.url.String()).
		Msg("creating a new upstream cache")

	if err := c.validateURL(u); err != nil {
		return nil, err
	}

	for _, pubKey := range opts.PublicKeys {
		pk, err := signature.ParsePublicKey(pubKey)
		if err != nil {
			return nil, fmt.Errorf("error parsing the public key: %w", err)
		}

		c.publicKeys = append(c.publicKeys, pk)
	}

	if u.Query().Has("priority") {
		priority, err := strconv.ParseUint(u.Query().Get("priority"), 10, 16)
		if err != nil {
			return nil, fmt.Errorf("error parsing the priority from the URL %q: %w", u, err)
		}

		if priority <= 0 {
			c.priority = 40 // Default priority if zero or negative
		} else {
			c.priority = priority
		}
	} else {
		c.priority = 40 // Default priority
	}

	return c, nil
}

func (c *Cache) setupHTTPClient() error {
	dtP, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return ErrTransportCastError
	}

	// create a copy of the default transport
	dt := dtP.Clone()

	dialer := &net.Dialer{
		Timeout:   c.dialerTimeout,
		KeepAlive: 30 * time.Second,
	}

	// configure dialer with tighter timeout
	dt.DialContext = dialer.DialContext

	// Disable automatic compression handling so we can deal with it ourselves (transparent zstd support).
	dt.DisableCompression = true

	// Set timeout to first byte
	dt.ResponseHeaderTimeout = c.responseHeaderTimeout

	c.httpClient = &http.Client{
		Transport: otelhttp.NewTransport(dt),
	}

	return nil
}

// IsHealthy returns true if the upstream is healthy.
func (c *Cache) IsHealthy() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.isHealthy
}

// SetHealthy sets the health status of the upstream.
func (c *Cache) SetHealthy(isHealthy bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.isHealthy = isHealthy
}

// SetPriority sets the priority of the upstream.
func (c *Cache) SetPriority(priority uint64) {
	c.priority = priority
}

// ParsePriority parses the priority from the upstream.
func (c *Cache) ParsePriority(ctx context.Context) (uint64, error) {
	return c.parsePriority(ctx)
}

// GetHostname returns the hostname.
func (c *Cache) GetHostname() string { return c.url.Hostname() }

// GetNarInfo returns a parsed NarInfo from the cache server.
func (c *Cache) GetNarInfo(ctx context.Context, hash string) (*narinfo.NarInfo, error) {
	u := c.url.JoinPath(helper.NarInfoURLPath(hash)).String()

	ctx, span := tracer.Start(
		ctx,
		"upstream.GetNarInfo",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
			attribute.String("narinfo_url", u),
			attribute.String("upstream_url", c.url.String()),
		),
	)
	defer span.End()

	ctx = zerolog.Ctx(ctx).
		With().
		Str("narinfo_hash", hash).
		Str("narinfo_url", u).
		Str("upstream_url", c.url.String()).
		Logger().
		WithContext(ctx)

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating a new request: %w", err)
	}

	c.addAuthToRequest(r)

	zerolog.Ctx(ctx).
		Info().
		Msg("download the narinfo from upstream")

	resp, err := c.httpClient.Do(r)
	if err != nil {
		return nil, fmt.Errorf("error performing the request: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)

		if resp.StatusCode == http.StatusNotFound {
			return nil, ErrNotFound
		}

		zerolog.Ctx(ctx).
			Error().
			Err(ErrUnexpectedHTTPStatusCode).
			Int("status_code", resp.StatusCode).
			Send()

		return nil, ErrUnexpectedHTTPStatusCode
	}

	ni, err := narinfo.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error parsing the narinfo: %w", err)
	}

	if err := ni.Check(); err != nil {
		return ni, fmt.Errorf("error while checking the narInfo: %w", err)
	}

	if len(c.publicKeys) > 0 {
		if !signature.VerifyFirst(ni.Fingerprint(), ni.Signatures, c.publicKeys) {
			return ni, ErrSignatureValidationFailed
		}
	}

	return ni, nil
}

// HasNarInfo returns true if the narinfo exists upstream.
func (c *Cache) HasNarInfo(ctx context.Context, hash string) (bool, error) {
	u := c.url.JoinPath(helper.NarInfoURLPath(hash)).String()

	ctx, span := tracer.Start(
		ctx,
		"upstream.HasNarInfo",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
			attribute.String("narinfo_url", u),
			attribute.String("upstream_url", c.url.String()),
		),
	)
	defer span.End()

	ctx = zerolog.Ctx(ctx).
		With().
		Str("narinfo_hash", hash).
		Str("narinfo_url", u).
		Str("upstream_url", c.url.String()).
		Logger().
		WithContext(ctx)

	r, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return false, fmt.Errorf("error creating a new request: %w", err)
	}

	c.addAuthToRequest(r)

	zerolog.Ctx(ctx).
		Info().
		Msg("heading the narinfo from upstream")

	resp, err := c.httpClient.Do(r)
	if err != nil {
		return false, fmt.Errorf("error performing the request: %w", err)
	}

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	return resp.StatusCode < http.StatusBadRequest, nil
}

// GetNar returns the NAR archive from the cache server.
// NOTE: It's the caller responsibility to close the body.
func (c *Cache) GetNar(ctx context.Context, narURL nar.URL, mutators ...func(*http.Request)) (*http.Response, error) {
	u := narURL.JoinURL(c.url).String()

	ctx, span := tracer.Start(
		ctx,
		"upstream.GetNar",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("nar_url", u),
			attribute.String("upstream_url", c.url.String()),
		),
	)
	defer span.End()

	ctx = narURL.NewLogger(
		zerolog.Ctx(ctx).
			With().
			Str("nar_url", u).
			Str("upstream_url", c.url.String()).
			Logger(),
	).WithContext(ctx)

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating a new request: %w", err)
	}

	c.addAuthToRequest(r)

	for _, mutator := range mutators {
		mutator(r)
	}

	zerolog.Ctx(ctx).
		Info().
		Msg("download the nar from upstream")

	resp, err := c.httpClient.Do(r)
	if err != nil {
		return nil, fmt.Errorf("error performing the request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)

		if resp.StatusCode == http.StatusNotFound {
			return nil, ErrNotFound
		}

		zerolog.Ctx(ctx).
			Error().
			Err(ErrUnexpectedHTTPStatusCode).
			Int("status_code", resp.StatusCode).
			Send()

		return nil, ErrUnexpectedHTTPStatusCode
	}

	return resp, nil
}

// HasNar returns true if the NAR exists upstream.
func (c *Cache) HasNar(ctx context.Context, narURL nar.URL, mutators ...func(*http.Request)) (bool, error) {
	u := narURL.JoinURL(c.url).String()

	ctx, span := tracer.Start(
		ctx,
		"upstream.HasNar",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("nar_url", u),
			attribute.String("upstream_url", c.url.String()),
		),
	)
	defer span.End()

	ctx = narURL.NewLogger(
		zerolog.Ctx(ctx).
			With().
			Str("nar_url", u).
			Str("upstream_url", c.url.String()).
			Logger(),
	).WithContext(ctx)

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, fmt.Errorf("error creating a new request: %w", err)
	}

	c.addAuthToRequest(r)

	for _, mutator := range mutators {
		mutator(r)
	}

	zerolog.Ctx(ctx).
		Info().
		Msg("heading the nar from upstream")

	resp, err := c.httpClient.Do(r)
	if err != nil {
		return false, fmt.Errorf("error performing the request: %w", err)
	}

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	return resp.StatusCode < http.StatusBadRequest, nil
}

// GetPriority returns the priority of this upstream cache.
func (c *Cache) GetPriority() uint64 { return c.priority }

func (c *Cache) parsePriority(ctx context.Context) (uint64, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url.JoinPath("/nix-cache-info").String(), nil)
	if err != nil {
		return 0, fmt.Errorf("error creating a new request: %w", err)
	}

	c.addAuthToRequest(r)

	resp, err := c.httpClient.Do(r)
	if err != nil {
		return 0, fmt.Errorf("error performing the request: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		zerolog.Ctx(ctx).
			Error().
			Err(ErrUnexpectedHTTPStatusCode).
			Int("status_code", resp.StatusCode).
			Send()

		return 0, ErrUnexpectedHTTPStatusCode
	}

	nci, err := nixcacheinfo.Parse(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("error parsing the nix-cache-info: %w", err)
	}

	return nci.Priority, nil
}

// addAuthToRequest adds authentication to HTTP request if available.
func (c *Cache) addAuthToRequest(req *http.Request) {
	if c.netrcAuth != nil {
		req.SetBasicAuth(c.netrcAuth.Username, c.netrcAuth.Password)
	}
}

func (c *Cache) validateURL(u *url.URL) error {
	if u == nil {
		return ErrURLRequired
	}

	if u.Scheme == "" {
		return ErrURLMustContainScheme
	}

	return nil
}
