package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/klauspost/compress/zstd"
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
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
)

const (
	routeIndex          = "/"
	routeNar            = "/nar/{hash:[a-z0-9]{32,52}}.nar"
	routeNarCompression = "/nar/{hash:[a-z0-9]{32,52}}.nar.{compression:*}"
	routeNarInfo        = "/{hash:[a-z0-9]{32,52}}.narinfo"
	routeCacheInfo      = "/nix-cache-info"
	routeCachePublicKey = "/pubkey"

	contentLength      = "Content-Length"
	contentType        = "Content-Type"
	contentTypeNar     = "application/x-nix-nar"
	contentTypeNarInfo = "text/x-nix-narinfo"
	contentTypeJSON    = "application/json"

	nixCacheInfo = `StoreDir: /nix/store
WantMassQuery: 1
Priority: 10`

	otelPackageName = "github.com/kalbasit/ncps/pkg/server"
)

//nolint:gochecknoglobals
var tracer trace.Tracer

//nolint:gochecknoglobals
var prometheusGatherer promclient.Gatherer

//nolint:gochecknoglobals
var zstdWriterPool = sync.Pool{
	New: func() interface{} {
		// Not providing any options will use the default compression level.
		// The error is ignored as NewWriter(nil) with no options doesn't error.
		enc, _ := zstd.NewWriter(nil)

		return enc
	},
}

//nolint:gochecknoinits
func init() {
	tracer = otel.Tracer(otelPackageName)
}

// Server represents the main HTTP server.
type Server struct {
	cache  *cache.Cache
	router *chi.Mux

	deletePermitted bool
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

// SetPutPermitted configures the server to either allow or deny access to PUT.
func (s *Server) SetPutPermitted(pp bool) { s.putPermitted = pp }

// ServeHTTP implements http.Handler and turns the Server type into a handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.router.ServeHTTP(w, r) }

func (s *Server) createRouter() {
	s.router = chi.NewRouter()

	mp := otel.GetMeterProvider()
	baseCfg := otelchimetric.NewBaseConfig(s.cache.GetHostname(), otelchimetric.WithMeterProvider(mp))

	s.router.Use(middleware.Heartbeat("/healthz"))
	s.router.Use(middleware.RealIP)
	s.router.Use(recoverer)

	// Create a middleware skipper that excludes /metrics and /healthz from telemetry
	skipTelemetryForInfraRoutes := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip telemetry middleware for infrastructure endpoints
			if r.URL.Path == "/metrics" || r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)

				return
			}

			// Apply all telemetry middleware for other routes
			telemetryChain := otelchi.Middleware(s.cache.GetHostname(), otelchi.WithChiRoutes(s.router))(
				otelchimetric.NewRequestDurationMillis(baseCfg)(
					otelchimetric.NewRequestInFlight(baseCfg)(
						otelchimetric.NewResponseSizeBytes(baseCfg)(
							requestLogger(next),
						),
					),
				),
			)
			telemetryChain.ServeHTTP(w, r)
		})
	}

	s.router.Use(skipTelemetryForInfraRoutes)

	s.router.Get(routeIndex, s.getIndex)

	s.router.Get(routeCacheInfo, s.getNixCacheInfo)
	s.router.Get(routeCachePublicKey, s.getNixCachePublicKey)

	s.router.Head(routeNarInfo, s.getNarInfo(false))
	s.router.Get(routeNarInfo, s.getNarInfo(true))
	s.router.Put(routeNarInfo, s.putNarInfo)
	s.router.Delete(routeNarInfo, s.deleteNarInfo)

	s.router.Head(routeNarCompression, s.getNar(false))
	s.router.Get(routeNarCompression, s.getNar(true))
	s.router.Put(routeNarCompression, s.putNar)
	s.router.Delete(routeNarCompression, s.deleteNar)

	s.router.Head(routeNar, s.getNar(false))
	s.router.Get(routeNar, s.getNar(true))
	s.router.Put(routeNar, s.putNar)
	s.router.Delete(routeNar, s.deleteNar)

	// Add Prometheus metrics endpoint if gatherer is configured
	if prometheusGatherer != nil {
		s.router.Get("/metrics", promhttp.HandlerFor(prometheusGatherer, promhttp.HandlerOpts{}).ServeHTTP)
	}
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

	logContext := zerolog.Ctx(r.Context()).With().
		Str("method", r.Method).
		Str("request_uri", r.RequestURI).
		Str("from", r.RemoteAddr)

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

func (s *Server) getNarInfo(withBody bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := chi.URLParam(r, "hash")
		if !helper.IsValidHash(hash) {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)

			return
		}

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
				WithContext(ctx))

		narInfo, err := s.cache.GetNarInfo(r.Context(), hash)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)

				return
			}

			zerolog.Ctx(r.Context()).
				Error().
				Err(err).
				Msg("error fetching the narinfo")

			http.Error(w, err.Error(), http.StatusInternalServerError)

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

			narInfoCopy.URL = narURL.Normalize().String()
		}

		narInfoBytes := []byte(narInfoCopy.String())

		h := w.Header()
		h.Set(contentType, contentTypeNarInfo)
		h.Set(contentLength, strconv.Itoa(len(narInfoBytes)))

		if !withBody {
			w.WriteHeader(http.StatusOK)

			return
		}

		if _, err := w.Write(narInfoBytes); err != nil {
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
	if !helper.IsValidHash(hash) {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)

		return
	}

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
			WithContext(ctx))

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

func (s *Server) deleteNarInfo(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !helper.IsValidHash(hash) {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)

		return
	}

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
			WithContext(ctx))

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

// withNarURL extracts NAR URL parameters, sets up context with logging and tracing,
// and calls the handler function with the prepared context and NAR URL.
func (s *Server) withNarURL(
	operationName string,
	handler func(http.ResponseWriter, *http.Request, nar.URL),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := chi.URLParam(r, "hash")
		if !helper.IsValidHash(hash) {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)

			return
		}

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
		// optimization: if this is a HEAD request, we can check if we have the
		// narinfo for this nar and if so, return the size from there.
		if !withBody {
			size, err := s.cache.GetNarFileSize(r.Context(), nu)
			if err == nil && size > 0 {
				h := w.Header()
				h.Set(contentType, contentTypeNar)
				h.Set(contentLength, strconv.FormatInt(size, 10))

				w.WriteHeader(http.StatusOK)

				return
			}
		}

		size, reader, err := s.cache.GetNar(r.Context(), nu)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) || errors.Is(err, upstream.ErrNotFound) {
				http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)

				return
			}

			zerolog.Ctx(r.Context()).
				Error().
				Err(err).
				Msg("error fetching the nar")

			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		h := w.Header()
		h.Set(contentType, contentTypeNar)

		// Check for transparent compression support
		useZstd := false

		if nu.Compression == nar.CompressionTypeNone && withBody {
			ae := r.Header.Get("Accept-Encoding")
			if strings.Contains(ae, "zstd") {
				useZstd = true
			}
		}

		var out io.Writer = w

		if useZstd {
			enc := zstdWriterPool.Get().(*zstd.Encoder)
			enc.Reset(w)
			out = enc

			defer func() {
				if err := enc.Close(); err != nil {
					zerolog.Ctx(r.Context()).Error().Err(err).Msg("failed to close zstd writer")
				}

				zstdWriterPool.Put(enc)
			}()
		}

		if useZstd {
			h.Set("Content-Encoding", "zstd")
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

		if !useZstd && size != -1 && written != size {
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
