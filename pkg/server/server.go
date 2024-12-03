package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/inconshreveable/log15/v3"
	"github.com/kalbasit/ncps/pkg/cache"
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
	cache  cache.Cache
	logger log15.Logger
	router *chi.Mux
}

// New returns a new server.
func New(logger log15.Logger, cache cache.Cache) Server {
	s := Server{
		cache:  cache,
		logger: logger,
	}

	s.router = createRouter(s)

	return s
}

// ServeHTTP implements http.Handler and turns the Server type into a handler.
func (s Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.router.ServeHTTP(w, r) }

func createRouter(s Server) *chi.Mux {
	router := chi.NewRouter()

	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(requestLogger(s.logger))
	router.Use(middleware.Recoverer)

	router.Get(routeIndex, s.getIndex)

	router.Get(routeCacheInfo, s.getNixCacheInfo)

	router.Head(routeNarInfo, s.getNarInfo(false))
	router.Get(routeNarInfo, s.getNarInfo(true))

	router.Head(routeNarCompression, s.getNar(false))
	router.Get(routeNarCompression, s.getNar(true))

	router.Head(routeNar, s.getNar(false))
	router.Get(routeNar, s.getNar(true))

	return router
}

func requestLogger(logger log15.Logger) func(handler http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()
			reqID := middleware.GetReqID(r.Context())

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				entries := []interface{}{
					"status", ww.Status(),
					"elapsed", time.Since(startedAt),
					"from", r.RemoteAddr,
					"reqID", reqID,
				}

				switch r.Method {
				case http.MethodHead, http.MethodGet:
					entries = append(entries, "bytes", ww.BytesWritten())
				case http.MethodPost, http.MethodPut, http.MethodPatch:
					entries = append(entries, "bytes", r.ContentLength)
				}

				logger.Info(fmt.Sprintf("%s %s", r.Method, r.RequestURI), entries...)
			}()

			next.ServeHTTP(ww, r)
		}

		return http.HandlerFunc(fn)
	}
}

func (s *Server) getIndex(w http.ResponseWriter, _ *http.Request) {
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
		s.logger.Error("error writing the body to the response", "error", err)
	}
}

func (s Server) getNixCacheInfo(w http.ResponseWriter, _ *http.Request) {
	if _, err := w.Write([]byte(nixCacheInfo)); err != nil {
		s.logger.Error("error writing the response", "error", err)
	}
}

func (s Server) getNarInfo(withBody bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := chi.URLParam(r, "hash")

		narInfo, err := s.cache.GetNarInfo(r.Context(), hash)
		if err != nil {
			if errors.Is(err, cache.ErrNotFound) {
				w.WriteHeader(http.StatusNotFound)

				if _, err := w.Write([]byte(http.StatusText(http.StatusNotFound))); err != nil {
					s.logger.Error("error writing the response", "error", err)
				}

				return
			}

			s.logger.Error("error fetching the narinfo", "hash", hash, "error", err)
			w.WriteHeader(http.StatusInternalServerError)

			if _, err := w.Write([]byte(http.StatusText(http.StatusInternalServerError))); err != nil {
				s.logger.Error("error writing the response", "error", err)
			}

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
			s.logger.Error("error writing the narinfo to the response", "error", err)
		}
	}
}

func (s Server) getNar(withBody bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := chi.URLParam(r, "hash")
		compression := chi.URLParam(r, "compression")

		size, reader, err := s.cache.GetNar(hash, compression)
		if err != nil {
			if errors.Is(err, cache.ErrNotFound) {
				w.WriteHeader(http.StatusNotFound)

				if _, err := w.Write([]byte(http.StatusText(http.StatusNotFound))); err != nil {
					s.logger.Error("error writing the response", "error", err)
				}

				return
			}

			s.logger.Error("error fetching the nar", "hash", hash, "compression", compression, "error", err)
			w.WriteHeader(http.StatusInternalServerError)

			if _, err := w.Write([]byte(http.StatusText(http.StatusInternalServerError))); err != nil {
				s.logger.Error("error writing the response", "error", err)
			}

			return
		}

		h := w.Header()
		h.Set(contentType, contentTypeNarInfo)
		h.Set(contentLength, strconv.FormatInt(size, 10))

		if !withBody {
			w.WriteHeader(http.StatusNoContent)

			return
		}

		written, err := io.Copy(w, reader)
		if err != nil {
			s.logger.Error("error writing the response", "error", err)
		}

		if written != size {
			s.logger.Error("Bytes copied does not match object size", "expected", size, "written", written)
		}
	}
}
