package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"

	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
)

const (
	routeIndex          = "/"
	routeNar            = "/nar/{hash:[a-z0-9]+}.nar"
	routeNarCompression = "/nar/{hash:[a-z0-9]+}.nar.{compression:*}"
	routeNarInfo        = "/{hash:[a-z0-9]+}.narinfo"
	routeCacheInfo      = "/nix-cache-info"

	contentLength      = "Content-Length"
	contentType        = "Content-Type"
	contentTypeNar     = "application/x-nix-nar"
	contentTypeNarInfo = "text/x-nix-narinfo"
	contentTypeJSON    = "application/json"

	nixCacheInfo = `StoreDir: /nix/store
WantMassQuery: 1
Priority: 10`
)

// Server represents the main HTTP server.
type Server struct {
	cache  *cache.Cache
	router *chi.Mux

	deletePermitted bool
	putPermitted    bool
}

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
	baseCfg := otelchimetric.NewBaseConfig("ncps", otelchimetric.WithMeterProvider(mp))

	s.router.Use(middleware.Heartbeat("/healthz"))
	s.router.Use(middleware.RealIP)
	s.router.Use(middleware.Recoverer)
	s.router.Use(requestLogger)
	s.router.Use(
		otelchi.Middleware("ncps", otelchi.WithChiRoutes(s.router)),
		otelchimetric.NewRequestDurationMillis(baseCfg),
		otelchimetric.NewRequestInFlight(baseCfg),
		otelchimetric.NewResponseSizeBytes(baseCfg),
	)

	s.router.Get(routeIndex, s.getIndex)

	s.router.Get(routeCacheInfo, s.getNixCacheInfo)

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
}

func requestLogger(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		reqID := middleware.GetReqID(r.Context())

		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		log := zerolog.Ctx(r.Context()).With().
			Str("method", r.Method).
			Str("request-uri", r.RequestURI).
			Str("from", r.RemoteAddr).
			Str("reqID", reqID).
			Logger()

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
	}

	return http.HandlerFunc(fn)
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
	if _, err := w.Write([]byte(nixCacheInfo)); err != nil {
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

		r = r.WithContext(
			zerolog.Ctx(r.Context()).
				With().
				Str("narinfo-hash", hash).
				Logger().
				WithContext(r.Context()))

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

		narInfoBytes := []byte(narInfo.String())

		h := w.Header()
		h.Set(contentType, contentTypeNarInfo)
		h.Set(contentLength, strconv.Itoa(len(narInfoBytes)))

		if !withBody {
			w.WriteHeader(http.StatusNoContent)

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

	r = r.WithContext(
		zerolog.Ctx(r.Context()).
			With().
			Str("narinfo-hash", hash).
			Logger().
			WithContext(r.Context()))

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

	r = r.WithContext(
		zerolog.Ctx(r.Context()).
			With().
			Str("narinfo-hash", hash).
			Logger().
			WithContext(r.Context()))

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

func (s *Server) getNar(withBody bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := chi.URLParam(r, "hash")

		nu := nar.URL{Hash: hash, Query: r.URL.Query()}

		r = r.WithContext(
			nu.NewLogger(*zerolog.Ctx(r.Context())).
				WithContext(r.Context()))

		var err error

		nu.Compression, err = nar.CompressionTypeFromExtension(chi.URLParam(r, "compression"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		r = r.WithContext(
			nu.NewLogger(*zerolog.Ctx(r.Context())).
				WithContext(r.Context()))

		size, reader, err := s.cache.GetNar(r.Context(), nu)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
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
		h.Set(contentLength, strconv.FormatInt(size, 10))

		if !withBody {
			w.WriteHeader(http.StatusNoContent)

			return
		}

		written, err := io.Copy(w, reader)
		if err != nil {
			zerolog.Ctx(r.Context()).
				Error().
				Err(err).
				Msg("error writing the response")

			return
		}

		if written != size {
			zerolog.Ctx(r.Context()).
				Error().
				Int64("expected", size).
				Int64("written", written).
				Msg("Bytes copied does not match object size")
		}
	}
}

func (s *Server) putNar(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	nu := nar.URL{Hash: hash, Query: r.URL.Query()}

	r = r.WithContext(
		nu.NewLogger(*zerolog.Ctx(r.Context())).
			WithContext(r.Context()))

	var err error

	nu.Compression, err = nar.CompressionTypeFromExtension(chi.URLParam(r, "compression"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	r = r.WithContext(
		nu.NewLogger(*zerolog.Ctx(r.Context())).
			WithContext(r.Context()))

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
}

func (s *Server) deleteNar(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	nu := nar.URL{Hash: hash, Query: r.URL.Query()}

	r = r.WithContext(
		nu.NewLogger(*zerolog.Ctx(r.Context())).
			WithContext(r.Context()))

	var err error

	nu.Compression, err = nar.CompressionTypeFromExtension(chi.URLParam(r, "compression"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	r = r.WithContext(
		nu.NewLogger(*zerolog.Ctx(r.Context())).
			WithContext(r.Context()))

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
}
