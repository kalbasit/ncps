package server

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/riandyrn/otelchi"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	promclient "github.com/prometheus/client_golang/prometheus"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/kalbasit/ncps/pkg/analytics"
	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/narinfo"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/zstd"
)

const (
	routeIndex          = "/"
	routeNar            = "/nar/{hash:" + nar.NormalizedHashPattern + "}.nar"
	routeNarCompression = "/nar/{hash:" + nar.NormalizedHashPattern + "}.nar.{compression:*}"
	routeNarInfo        = "/{hash:" + narinfo.HashPattern + "}.narinfo"
	routeCacheInfo      = "/nix-cache-info"
	routeCachePublicKey = "/pubkey"
	routePinClosure     = "/pin/{hash:" + narinfo.HashPattern + "}.narinfo"
	routePins           = "/pins"
	routeBuildTrace     = "/build-trace-v2/{drvName}/{outputName}"

	contentLength      = "Content-Length"
	contentType        = "Content-Type"
	contentTypeNar     = "application/x-nix-nar"
	contentTypeNarInfo = "text/x-nix-narinfo"
	contentTypeJSON    = "application/json"
	encodingZstd       = "zstd"

	nixCacheInfo = `StoreDir: /nix/store
WantMassQuery: 1
Priority: 10`

	otelPackageName = "github.com/kalbasit/ncps/pkg/server"
)

//nolint:gochecknoglobals
var tracer trace.Tracer

//nolint:gochecknoglobals
var prometheusGatherer promclient.Gatherer

//nolint:gochecknoinits
func init() {
	tracer = otel.Tracer(otelPackageName)
}

// Server represents the main HTTP server.
type Server struct {
	cache  *cache.Cache
	router *chi.Mux

	deletePermitted bool
	getToken        string
	putPermitted    bool
}

// SetPrometheusGatherer configures the server with a Prometheus gatherer for /metrics endpoint.
func SetPrometheusGatherer(gatherer promclient.Gatherer) { prometheusGatherer = gatherer }

// New returns a new server.
func New(cache *cache.Cache) *Server {
	s := &Server{cache: cache}

	s.createRouter()

	return s
}

// SetDeletePermitted configures the server to either allow or deny access to DELETE.
func (s *Server) SetDeletePermitted(dp bool) { s.deletePermitted = dp }

// SetGetToken configures a Bearer token required to access GET and HEAD routes.
// When non-empty, requests without a matching Authorization: Bearer <token> header
// are rejected with 401 Unauthorized. The /healthz and /metrics routes are always
// exempt.
func (s *Server) SetGetToken(token string) { s.getToken = token }

// SetPutPermitted configures the server to either allow or deny access to PUT.
func (s *Server) SetPutPermitted(pp bool) { s.putPermitted = pp }

// ServeHTTP implements http.Handler and turns the Server type into a handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.router.ServeHTTP(w, r) }

func (s *Server) createRouter() {
	s.router = chi.NewRouter()

	s.router.Use(middleware.Heartbeat("/healthz"))
	s.router.Use(middleware.ClientIPFromXFF())
	s.router.Use(recoverer)

	s.router.Use(s.skipTelemetryForInfraRoutes)
	s.router.Use(s.requireGetToken)

	// 1. Register standard routes at the root
	s.registerRoutes(s.router)

	// 2. Register DELETE routes at the root
	s.router.Delete(routeNarInfo, s.deleteNarInfo)
	s.router.Delete(routeNarCompression, s.deleteNar)
	s.router.Delete(routeNar, s.deleteNar)

	// Pin endpoints
	s.router.Post(routePinClosure, s.pinClosure)
	s.router.Delete(routePinClosure, s.unpinClosure)
	s.router.Get(routePins, s.listPins)

	// 2. Register "upload only" routes under /upload
	s.router.Route("/upload", func(r chi.Router) {
		// Middleware to inject the UploadOnly flag
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := cache.WithUploadOnly(r.Context())
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		})

		// register standard routes
		s.registerRoutes(r)

		// register PUT routes
		r.Put(routeNarInfo, s.putNarInfo)
		r.Put(routeNarCompression, s.putNar)
		r.Put(routeNar, s.putNar)
		r.Put(routeBuildTrace, s.putBuildTrace)
	})

	// Add Prometheus metrics endpoint if gatherer is configured
	if prometheusGatherer != nil {
		s.router.Get("/metrics", promhttp.HandlerFor(prometheusGatherer, promhttp.HandlerOpts{}).ServeHTTP)
	}
}

// Create a middleware skipper that excludes /metrics and /healthz from telemetry.
func (s *Server) skipTelemetryForInfraRoutes(next http.Handler) http.Handler {
	mp := otel.GetMeterProvider()
	baseCfg := otelchimetric.NewBaseConfig(s.cache.GetHostname(), otelchimetric.WithMeterProvider(mp))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip telemetry middleware for infrastructure endpoints
		if r.URL.Path == "/metrics" || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)

			return
		}

		// Apply all telemetry middleware for other routes
		telemetryChain := otelchi.Middleware(s.cache.GetHostname(), otelchi.WithChiRoutes(s.router))(
			otelchimetric.NewServerRequestDuration(baseCfg)(
				otelchimetric.NewServerActiveRequests(baseCfg)(
					otelchimetric.NewServerResponseBodySize(baseCfg)(
						requestLogger(next),
					),
				),
			),
		)
		telemetryChain.ServeHTTP(w, r)
	})
}

// Extract your existing route definitions into this helper.
func (s *Server) registerRoutes(r chi.Router) {
	r.Get(routeIndex, s.getIndex)
	r.Get(routeCacheInfo, s.getNixCacheInfo)
	r.Get(routeCachePublicKey, s.getNixCachePublicKey)

	r.Head(routeNarInfo, s.getNarInfo(false))
	r.Get(routeNarInfo, s.getNarInfo(true))

	r.Head(routeNarCompression, s.getNar(false))
	r.Get(routeNarCompression, s.getNar(true))

	r.Head(routeNar, s.getNar(false))
	r.Get(routeNar, s.getNar(true))

	r.Head(routeBuildTrace, s.getBuildTrace(false))
	r.Get(routeBuildTrace, s.getBuildTrace(true))
}

// requireGetToken is a middleware that enforces Bearer token authentication for
// GET and HEAD requests when s.getToken is non-empty. Infrastructure endpoints
// (/healthz and /metrics) are always exempt regardless of configuration.
func (s *Server) requireGetToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.getToken == "" {
			next.ServeHTTP(w, r)

			return
		}

		// Only apply to GET and HEAD; other methods have their own guards.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)

			return
		}

		// Infrastructure routes are always exempt.
		if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)

			return
		}

		authHeader := r.Header.Get("Authorization")

		const bearerPrefix = "Bearer "

		// Hash both tokens to a fixed length before the constant-time compare.
		// subtle.ConstantTimeCompare returns early when the slice lengths differ,
		// so comparing the raw variable-length tokens directly would leak the
		// secret's length via a timing side-channel. SHA-256 digests are always
		// 32 bytes, so the comparison time is independent of both token contents
		// and length.
		presented := strings.TrimPrefix(authHeader, bearerPrefix)
		presentedHash := sha256.Sum256([]byte(presented))
		expectedHash := sha256.Sum256([]byte(s.getToken))

		if !strings.HasPrefix(authHeader, bearerPrefix) ||
			subtle.ConstantTimeCompare(presentedHash[:], expectedHash[:]) != 1 {
			// RFC 7235 §4.1: a 401 response must carry a challenge.
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)

			return
		}

		next.ServeHTTP(w, r)
	})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rvr := recover()
			if rvr == nil {
				return
			}

			if rvr == http.ErrAbortHandler { //nolint:err113
				// we don't recover http.ErrAbortHandler so the response
				// to the client is aborted, this should not be logged
				panic(rvr)
			}

			analytics.Ctx(r.Context()).
				LogPanic(r.Context(), rvr, debug.Stack())

			if r.Header.Get("Connection") != "Upgrade" {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

func getZeroLogForRequest(r *http.Request) zerolog.Logger {
	span := trace.SpanFromContext(r.Context())

	from := middleware.GetClientIP(r.Context())
	if from == "" {
		from = r.RemoteAddr
	}

	logContext := zerolog.Ctx(r.Context()).With().
		Str("method", r.Method).
		Str("request_uri", r.RequestURI).
		Str("from", from)

	if span.SpanContext().HasTraceID() {
		logContext = logContext.Str("trace_id", span.SpanContext().TraceID().String())
	}

	if span.SpanContext().HasSpanID() {
		logContext = logContext.Str("span_id", span.SpanContext().SpanID().String())
	}

	return logContext.Logger()
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()

		log := getZeroLogForRequest(r)

		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		defer func() {
			log = log.With().
				Int("status", ww.Status()).
				Dur("elapsed", time.Since(startedAt)).
				Logger()

			switch r.Method {
			case http.MethodHead, http.MethodGet:
				log = log.With().Int("bytes", ww.BytesWritten()).Logger()
			case http.MethodPost, http.MethodPut, http.MethodPatch:
				log = log.With().Int64("bytes", r.ContentLength).Logger()
			}

			log.Info().Msg("handled request")
		}()

		// embed the modified logger in the request.
		r = r.WithContext(log.WithContext(r.Context()))

		next.ServeHTTP(ww, r)
	})
}

func (s *Server) getIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Add(contentType, contentTypeJSON)
	w.WriteHeader(http.StatusOK)

	body := struct {
		Hostname  string `json:"hostname"`
		Publickey string `json:"publicKey"`
	}{
		Hostname:  s.cache.GetHostname(),
		Publickey: s.cache.PublicKey().String(),
	}

	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		zerolog.Ctx(r.Context()).
			Error().
			Err(err).
			Msg("error writing the response")
	}
}

func (s *Server) getNixCacheInfo(w http.ResponseWriter, r *http.Request) {
	_, span := tracer.Start(
		r.Context(),
		"server.getNixCacheInfo",
		trace.WithSpanKind(trace.SpanKindServer),
	)
	defer span.End()

	if _, err := w.Write([]byte(nixCacheInfo)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		zerolog.Ctx(r.Context()).
			Error().
			Err(err).
			Msg("error writing the response")
	}
}

func (s *Server) getNixCachePublicKey(w http.ResponseWriter, r *http.Request) {
	_, span := tracer.Start(

		r.Context(),
		"server.getNixCachePublicKey",
		trace.WithSpanKind(trace.SpanKindServer),
	)
	defer span.End()

	if _, err := w.Write([]byte(s.cache.PublicKey().String())); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		zerolog.Ctx(r.Context()).
			Error().
			Err(err).
			Msg("error writing the response")
	}
}

// narInfoErrorStatus maps a GetNarInfo error to the HTTP status the narinfo GET
// handler should return. respond is false when the handler should write nothing
// (the client is gone). cache.ErrNarInfoPurged is treated as 404 — defense in
// depth so the internal purge sentinel can never surface to a client as an
// HTTP 500.
func narInfoErrorStatus(err error) (status int, respond bool) {
	switch {
	case errors.Is(err, storage.ErrNotFound), errors.Is(err, cache.ErrNarInfoPurged):
		return http.StatusNotFound, true
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return 0, false
	default:
		return http.StatusInternalServerError, true
	}
}

func (s *Server) getNarInfo(withBody bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := chi.URLParam(r, "hash")

		ctx, span := tracer.Start(
			r.Context(),
			"server.getNarInfo",
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("narinfo_hash", hash),
			),
		)
		defer span.End()

		r = r.WithContext(
			zerolog.Ctx(ctx).
				With().
				Str("narinfo_hash", hash).
				Logger().
				WithContext(ctx),
		)

		narInfo, err := s.cache.GetNarInfo(r.Context(), hash)
		if err != nil {
			status, respond := narInfoErrorStatus(err)
			if !respond {
				return
			}

			if status == http.StatusInternalServerError {
				zerolog.Ctx(r.Context()).
					Error().
					Err(err).
					Msg("error fetching the narinfo")

				http.Error(w, err.Error(), status)

				return
			}

			// For non-500 outcomes (404, including a purged narinfo) write only the
			// generic status text — never leak an internal error message to the client.
			http.Error(w, http.StatusText(status), status)

			return
		}

		// Create a copy of narInfo to avoid race conditions when modifying
		narInfoCopy := *narInfo

		// Normalize the NAR URL in the narinfo to remove any narinfo hash prefix
		if narInfoCopy.URL != "" {
			narURL, err := nar.ParseURL(narInfoCopy.URL)
			if err != nil {
				zerolog.Ctx(r.Context()).
					Error().
					Err(err).
					Msg("error parsing the NAR URL")

				http.Error(w, err.Error(), http.StatusInternalServerError)

				return
			}

			normalizedURL, err := narURL.Normalize()
			if err != nil {
				zerolog.Ctx(r.Context()).
					Error().
					Err(err).
					Msg("error normalizing the NAR URL")

				http.Error(w, err.Error(), http.StatusInternalServerError)

				return
			}

			narInfoCopy.URL = normalizedURL.String()
		}

		narInfoBytes := []byte(narInfoCopy.String())

		h := w.Header()
		h.Set(contentType, contentTypeNarInfo)
		h.Set(contentLength, strconv.Itoa(len(narInfoBytes)))

		if !withBody {
			w.WriteHeader(http.StatusOK)

			return
		}

		if _, err := w.Write(narInfoBytes); err != nil { //nolint:gosec // G705: not user input
			http.Error(w, err.Error(), http.StatusInternalServerError)

			zerolog.Ctx(r.Context()).
				Error().
				Err(err).
				Msg("error writing the narinfo to the response")
		}
	}
}

func (s *Server) putNarInfo(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	ctx, span := tracer.Start(
		r.Context(),
		"server.putNarInfo",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	r = r.WithContext(
		zerolog.Ctx(ctx).
			With().
			Str("narinfo_hash", hash).
			Logger().
			WithContext(ctx),
	)

	if !s.putPermitted {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)

		return
	}

	if err := s.cache.PutNarInfo(r.Context(), hash, r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		zerolog.Ctx(r.Context()).
			Error().
			Err(err).
			Msg("error putting the NAR in cache")

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getBuildTrace(withBody bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		drvName := chi.URLParam(r, "drvName")
		outputName := strings.TrimSuffix(chi.URLParam(r, "outputName"), ".doi")

		ctx, span := tracer.Start(
			r.Context(),
			"server.getBuildTrace",
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("drv_name", drvName),
				attribute.String("output_name", outputName),
			),
		)
		defer span.End()

		r = r.WithContext(
			zerolog.Ctx(ctx).
				With().
				Str("drv_name", drvName).
				Str("output_name", outputName).
				Logger().
				WithContext(ctx),
		)

		data, err := s.cache.GetBuildTrace(r.Context(), drvName, outputName)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)

				return
			}

			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}

			zerolog.Ctx(r.Context()).
				Error().
				Err(err).
				Msg("error fetching build trace")

			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		h := w.Header()
		h.Set(contentType, "application/json")
		h.Set(contentLength, strconv.Itoa(len(data)))

		if !withBody {
			w.WriteHeader(http.StatusOK)

			return
		}

		if _, err := w.Write(data); err != nil { //nolint:gosec // G705: not user input
			zerolog.Ctx(r.Context()).
				Error().
				Err(err).
				Msg("error writing build trace response")
		}
	}
}

func (s *Server) putBuildTrace(w http.ResponseWriter, r *http.Request) {
	drvName := chi.URLParam(r, "drvName")
	outputName := strings.TrimSuffix(chi.URLParam(r, "outputName"), ".doi")

	ctx, span := tracer.Start(
		r.Context(),
		"server.putBuildTrace",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("drv_name", drvName),
			attribute.String("output_name", outputName),
		),
	)
	defer span.End()

	r = r.WithContext(
		zerolog.Ctx(ctx).
			With().
			Str("drv_name", drvName).
			Str("output_name", outputName).
			Logger().
			WithContext(ctx),
	)

	if !s.putPermitted {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)

		return
	}

	if err := s.cache.PutBuildTrace(r.Context(), drvName, outputName, r.Body); err != nil {
		if errors.Is(err, cache.ErrBadRequest) {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		zerolog.Ctx(r.Context()).
			Error().
			Err(err).
			Msg("error storing build trace")

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteNarInfo(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	ctx, span := tracer.Start(
		r.Context(),
		"server.deleteNarInfo",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	r = r.WithContext(
		zerolog.Ctx(ctx).
			With().
			Str("narinfo_hash", hash).
			Logger().
			WithContext(ctx),
	)

	if !s.deletePermitted {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)

		return
	}

	if err := s.cache.DeleteNarInfo(r.Context(), hash); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)

			return
		}

		zerolog.Ctx(r.Context()).
			Error().
			Err(err).
			Msg("error deleting the narinfo")

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) pinClosure(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	ctx, span := tracer.Start(
		r.Context(),
		"server.pinClosure",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	r = r.WithContext(
		zerolog.Ctx(ctx).
			With().
			Str("narinfo_hash", hash).
			Logger().
			WithContext(ctx),
	)

	if err := s.cache.PinClosure(r.Context(), hash); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)

			return
		}

		zerolog.Ctx(r.Context()).
			Error().
			Err(err).
			Msg("error pinning the closure")

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) unpinClosure(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	ctx, span := tracer.Start(
		r.Context(),
		"server.unpinClosure",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
		),
	)
	defer span.End()

	r = r.WithContext(
		zerolog.Ctx(ctx).
			With().
			Str("narinfo_hash", hash).
			Logger().
			WithContext(ctx),
	)

	if err := s.cache.UnpinClosure(r.Context(), hash); err != nil {
		zerolog.Ctx(r.Context()).
			Error().
			Err(err).
			Msg("error unpinning the closure")

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) listPins(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(
		r.Context(),
		"server.listPins",
		trace.WithSpanKind(trace.SpanKindServer),
	)
	defer span.End()

	closures, err := s.cache.ListPinnedClosures(ctx)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error listing pinned closures")

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	// Build response with just the hashes
	hashes := make([]string, len(closures))
	for i, c := range closures {
		hashes[i] = c.Hash
	}

	w.Header().Set(contentType, contentTypeJSON)

	if err := json.NewEncoder(w).Encode(hashes); err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error encoding response")
	}
}

// withNarURL extracts NAR URL parameters, sets up context with logging and tracing,
// and calls the handler function with the prepared context and NAR URL.
func (s *Server) withNarURL(
	operationName string,
	handler func(http.ResponseWriter, *http.Request, nar.URL),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := chi.URLParam(r, "hash")

		comp, err := nar.CompressionTypeFromExtension(chi.URLParam(r, "compression"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		nu := nar.URL{
			Compression: comp,
			Hash:        hash,
			Query:       r.URL.Query(),
		}

		ctx := nu.NewLogger(*zerolog.Ctx(r.Context())).
			WithContext(r.Context())

		ctx, span := tracer.Start(
			ctx,
			operationName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("nar_hash", hash),
				attribute.String("nar_url", nu.String()),
			),
		)
		defer span.End()

		r = r.WithContext(ctx)

		handler(w, r, nu)
	}
}

func (s *Server) getNar(withBody bool) http.HandlerFunc {
	return s.withNarURL("server.getNar", func(w http.ResponseWriter, r *http.Request, nu nar.URL) {
		// Check for transparent zstd support (only for uncompressed NAR requests)
		var clientAcceptsZstd bool

		if nu.Compression == nar.CompressionTypeNone {
			ae := r.Header.Get("Accept-Encoding")
			for v := range strings.SplitSeq(ae, ",") {
				enc := strings.TrimSpace(strings.Split(v, ";")[0])
				if enc == encodingZstd {
					clientAcceptsZstd = true

					break
				}
			}
		}

		// If client accepts zstd, tell the cache to keep it compressed if possible.
		if clientAcceptsZstd {
			nu.TransparentZstd = true
		}

		// optimization: if this is a HEAD request, we can check if we have the
		// narinfo for this nar and if so, return the size from there.
		if !withBody {
			// For HEAD requests, do NOT signal TransparentZstd — we want the
			// raw stored file size (which may be the zstd-compressed size),
			// not an estimate of the compressed output.
			nu.TransparentZstd = false

			// Only answer 200 from the nar_file record's size when the NAR is
			// actually servable (bytes present), not from the DB record alone.
			// A record without backing bytes (a phantom) must NOT HEAD 200, or a
			// client (e.g. `nix copy`) would skip uploading the NAR and leave a
			// phantom whose later reference check 404s. When not servable (or the
			// storage probe is ambiguous), fall through to GetNar, which resolves
			// upload-only to 404 (so the client re-uploads) and the substituter
			// path to upstream recovery — consistent with GET.
			size, err := s.cache.GetNarFileSize(r.Context(), nu)
			if err == nil && size > 0 {
				if servable, sErr := s.cache.IsNarServable(r.Context(), nu); sErr == nil && servable {
					h := w.Header()
					h.Set(contentType, contentTypeNar)
					h.Set(contentLength, strconv.FormatInt(size, 10))
					w.WriteHeader(http.StatusOK)

					return
				}
			}
		}

		nu, size, reader, err := s.cache.GetNar(r.Context(), nu)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) || errors.Is(err, upstream.ErrNotFound) {
				http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)

				return
			}

			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}

			zerolog.Ctx(r.Context()).
				Error().
				Err(err).
				Msg("error fetching the nar")

			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		defer reader.Close()

		h := w.Header()
		h.Set(contentType, contentTypeNar)

		// Check for transparent compression support (priority: zstd > br > gzip > raw)
		var (
			selectedEncoding string
			servedRawZstd    bool
		)

		if nu.Compression == nar.CompressionTypeNone && withBody {
			// If we already have a zstd stream from the cache, we're done.
			if nu.TransparentZstd {
				// Cache returned a zstd stream (e.g. from .nar.zst file or complete chunks)
				selectedEncoding = encodingZstd
				servedRawZstd = true
			} else {
				selectedEncoding = s.parseAcceptEncoding(r)
			}
		}

		var out io.Writer = w

		if selectedEncoding != "" && !servedRawZstd {
			switch selectedEncoding {
			case encodingZstd:
				pw := zstd.NewPooledWriter(w)
				out = pw

				defer func() {
					if err := pw.Close(); err != nil {
						zerolog.Ctx(r.Context()).Error().Err(err).Msg("failed to close zstd writer")
					}
				}()
			case "br":
				bw := brotli.NewWriter(w)
				out = bw

				defer func() {
					if err := bw.Close(); err != nil {
						zerolog.Ctx(r.Context()).Error().Err(err).Msg("failed to close brotli writer")
					}
				}()
			case "gzip":
				gw := gzip.NewWriter(w)
				out = gw

				defer func() {
					if err := gw.Close(); err != nil {
						zerolog.Ctx(r.Context()).Error().Err(err).Msg("failed to close gzip writer")
					}
				}()
			}
		}

		if selectedEncoding != "" {
			h.Set("Content-Encoding", selectedEncoding)
			// We can't know the compressed size in advance without compressing it all.
			// So we remove Content-Length and use chunked encoding.
			h.Del(contentLength)
		} else if size > 0 {
			h.Set(contentLength, strconv.FormatInt(size, 10))
		}

		if !withBody {
			// If the size is below zero then copy the entire nar to /dev/null and
			// compute the size that way. This usually means the NAR is still being
			// downloaded so the client will have to wait until completion.
			if size <= 0 {
				n, err := io.Copy(io.Discard, reader)
				if err != nil {
					zerolog.Ctx(r.Context()).
						Error().
						Err(err).
						Msg("error reading the nar to compute its size")

					http.Error(w, err.Error(), http.StatusInternalServerError)

					return
				}

				h.Set(contentLength, strconv.FormatInt(n, 10))
			}

			w.WriteHeader(http.StatusOK)

			return
		}

		w.WriteHeader(http.StatusOK)

		written, err := io.Copy(out, reader)
		if err != nil {
			zerolog.Ctx(r.Context()).
				Error().
				Err(err).
				Msg("error writing the response")

			return
		}

		if selectedEncoding == "" && size != -1 && written != size {
			zerolog.Ctx(r.Context()).
				Error().
				Int64("expected", size).
				Int64("written", written).
				Msg("Bytes copied does not match object size")
		}
	})
}

func (s *Server) putNar(w http.ResponseWriter, r *http.Request) {
	s.withNarURL("server.putNar", func(w http.ResponseWriter, r *http.Request, nu nar.URL) {
		if !s.putPermitted {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)

			return
		}

		if err := s.cache.PutNar(r.Context(), nu, r.Body); err != nil {
			zerolog.Ctx(r.Context()).
				Error().
				Err(err).
				Msg("error putting the NAR in cache")

			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}).ServeHTTP(w, r)
}

func (s *Server) deleteNar(w http.ResponseWriter, r *http.Request) {
	s.withNarURL("server.deleteNar", func(w http.ResponseWriter, r *http.Request, nu nar.URL) {
		if !s.deletePermitted {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)

			return
		}

		if err := s.cache.DeleteNar(r.Context(), nu); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)

				return
			}

			zerolog.Ctx(r.Context()).
				Error().
				Err(err).
				Msg("error deleting the nar")

			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}).ServeHTTP(w, r)
}

// parseAcceptEncoding parses the Accept-Encoding header and returns the preferred supported encoding.
func (s *Server) parseAcceptEncoding(r *http.Request) string {
	ae := r.Header.Get("Accept-Encoding")

	clientAccepts := make(map[string]struct{})

	for v := range strings.SplitSeq(ae, ",") {
		// Trim whitespace and remove q-factor if present
		enc := strings.TrimSpace(strings.Split(v, ";")[0])
		if enc != "" {
			clientAccepts[enc] = struct{}{}
		}
	}

	if _, ok := clientAccepts[encodingZstd]; ok {
		return encodingZstd
	} else if _, ok := clientAccepts["br"]; ok {
		return "br"
	} else if _, ok := clientAccepts["gzip"]; ok {
		return "gzip"
	}

	return ""
}
