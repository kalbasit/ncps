package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/nixcacheinfo"
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
)

const tracerName = "github.com/kalbasit/ncps/pkg/cache/upstream"

// Cache represents the upstream cache service.
type Cache struct {
	httpClient *http.Client
	url        *url.URL
	tracer     trace.Tracer
	priority   uint64
	publicKeys []signature.PublicKey
}

func New(ctx context.Context, u *url.URL, pubKeys []string) (Cache, error) {
	c := Cache{tracer: otel.Tracer(tracerName)}

	if u == nil {
		return c, ErrURLRequired
	}

	c.url = u

	ctx = zerolog.Ctx(ctx).
		With().
		Str("upstream_url", c.url.String()).
		Logger().
		WithContext(ctx)

	if err := c.validateURL(u); err != nil {
		return c, err
	}

	c.httpClient = &http.Client{
		Transport: &http.Transport{
			// Disable automatic decompression
			DisableCompression: true,
			// Respect proxy environment variables
			// (HTTP_PROXY, HTTPS_PROXY, NO_PROXY, etc.).
			Proxy: http.ProxyFromEnvironment,
		},
	}

	for _, pubKey := range pubKeys {
		pk, err := signature.ParsePublicKey(pubKey)
		if err != nil {
			return c, fmt.Errorf("error parsing the public key: %w", err)
		}

		c.publicKeys = append(c.publicKeys, pk)
	}

	var priority uint64

	if u.Query().Has("priority") {
		parsedPriority, err := strconv.ParseUint(u.Query().Get("priority"), 10, 16)
		if err != nil {
			return c, fmt.Errorf("error parsing the priority from the URL %q: %w", u, err)
		}

		priority = parsedPriority
	}

	if priority < 1 {
		parsedPriority, err := c.parsePriority(ctx)
		if err != nil {
			return c, fmt.Errorf("error parsing the priority for %q: %w", u, err)
		}

		priority = parsedPriority
	}

	c.priority = priority

	return c, nil
}

// GetHostname returns the hostname.
func (c Cache) GetHostname() string { return c.url.Hostname() }

// GetNarInfo returns a parsed NarInfo from the cache server.
func (c Cache) GetNarInfo(ctx context.Context, hash string) (*narinfo.NarInfo, error) {
	u := c.url.JoinPath(helper.NarInfoURLPath(hash)).String()

	ctx, span := c.tracer.Start(
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
func (c Cache) HasNarInfo(ctx context.Context, hash string) (bool, error) {
	u := c.url.JoinPath(helper.NarInfoURLPath(hash)).String()

	ctx, span := c.tracer.Start(
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
func (c Cache) GetNar(ctx context.Context, narURL nar.URL, mutators ...func(*http.Request)) (*http.Response, error) {
	u := narURL.JoinURL(c.url).String()

	ctx, span := c.tracer.Start(
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
func (c Cache) HasNar(ctx context.Context, narURL nar.URL, mutators ...func(*http.Request)) (bool, error) {
	u := narURL.JoinURL(c.url).String()

	ctx, span := c.tracer.Start(
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
func (c Cache) GetPriority() uint64 { return c.priority }

func (c Cache) parsePriority(ctx context.Context) (uint64, error) {
	ctx, cancelFn := context.WithTimeout(ctx, 3*time.Second)
	defer cancelFn()

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url.JoinPath("/nix-cache-info").String(), nil)
	if err != nil {
		return 0, fmt.Errorf("error creating a new request: %w", err)
	}

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

func (c Cache) validateURL(u *url.URL) error {
	if u == nil {
		return ErrURLRequired
	}

	if u.Scheme == "" {
		return ErrURLMustContainScheme
	}

	return nil
}
