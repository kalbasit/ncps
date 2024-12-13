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
	"github.com/kalbasit/ncps/pkg/nar"
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
	logger log15.Logger
	router *chi.Mux

	deletePermitted bool
	putPermitted    bool
}

// New returns a new server.
func New(logger log15.Logger, cache *cache.Cache) *Server {
	s := &Server{
		cache:  cache,
		logger: logger,
	}

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

	s.router.Use(middleware.Heartbeat("/healthz"))
	s.router.Use(middleware.RequestID)
	s.router.Use(middleware.RealIP)
	s.router.Use(requestLogger(s.logger))
	s.router.Use(middleware.Recoverer)

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

func (s *Server) getNixCacheInfo(w http.ResponseWriter, _ *http.Request) {
	if _, err := w.Write([]byte(nixCacheInfo)); err != nil {
		s.logger.Error("error writing the response", "error", err)
	}
}

func (s *Server) getNarInfo(withBody bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := chi.URLParam(r, "hash")

		log := s.logger.New("hash", hash)

		narInfo, err := s.cache.GetNarInfo(r.Context(), hash)
		if err != nil {
			if errors.Is(err, cache.ErrNotFound) {
				w.WriteHeader(http.StatusNotFound)

				if _, err := w.Write([]byte(http.StatusText(http.StatusNotFound))); err != nil {
					log.Error("error writing the response", "error", err)
				}

				return
			}

			log.Error("error fetching the narinfo", "error", err)
			w.WriteHeader(http.StatusInternalServerError)

			if _, err := w.Write([]byte(http.StatusText(http.StatusInternalServerError) + " " + err.Error())); err != nil {
				log.Error("error writing the response", "error", err)
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
			log.Error("error writing the narinfo to the response", "error", err)
		}
	}
}

func (s *Server) putNarInfo(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	log := s.logger.New("hash", hash)

	if !s.putPermitted {
		w.WriteHeader(http.StatusMethodNotAllowed)

		if _, err := w.Write([]byte(http.StatusText(http.StatusMethodNotAllowed))); err != nil {
			log.Error("error writing the body to the response", "error", err)
		}

		return
	}

	if err := s.cache.PutNarInfo(r.Context(), hash, r.Body); err != nil {
		log.Error("error putting the NAR in cache", "error", err)
		w.WriteHeader(http.StatusInternalServerError)

		if _, err := w.Write([]byte(http.StatusText(http.StatusInternalServerError) + " " + err.Error())); err != nil {
			log.Error("error writing the body to the response", "error", err)
		}

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteNarInfo(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	log := s.logger.New("hash", hash)

	if !s.deletePermitted {
		w.WriteHeader(http.StatusMethodNotAllowed)

		if _, err := w.Write([]byte(http.StatusText(http.StatusMethodNotAllowed))); err != nil {
			log.Error("error writing the body to the response", "error", err)
		}

		return
	}

	if err := s.cache.DeleteNarInfo(r.Context(), hash); err != nil {
		if errors.Is(err, cache.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)

			if _, err := w.Write([]byte(http.StatusText(http.StatusNotFound))); err != nil {
				log.Error("error writing the body to the response", "error", err)
			}

			return
		}

		w.WriteHeader(http.StatusInternalServerError)

		if _, err := w.Write([]byte(http.StatusText(http.StatusInternalServerError) + " " + err.Error())); err != nil {
			log.Error("error writing the body to the response", "error", err)
		}

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getNar(withBody bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := chi.URLParam(r, "hash")

		nu := nar.URL{Hash: hash, Query: r.URL.Query()}

		log := nu.NewLogger(s.logger)

		var err error

		nu.Compression, err = nar.CompressionTypeFromExtension(chi.URLParam(r, "compression"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)

			if _, err := w.Write([]byte(http.StatusText(http.StatusBadRequest))); err != nil {
				log.Error("error computing the compression", "error", err)
			}

			return
		}

		log = nu.NewLogger(s.logger) // re-create the logger to avoid dups

		size, reader, err := s.cache.GetNar(r.Context(), nu, nil)
		if err != nil {
			if errors.Is(err, cache.ErrNotFound) {
				w.WriteHeader(http.StatusNotFound)

				if _, err := w.Write([]byte(http.StatusText(http.StatusNotFound))); err != nil {
					log.Error("error writing the response", "error", err)
				}

				return
			}

			log.Error("error fetching the nar", "error", err)

			w.WriteHeader(http.StatusInternalServerError)

			if _, err := w.Write([]byte(http.StatusText(http.StatusInternalServerError) + " " + err.Error())); err != nil {
				log.Error("error writing the response", "error", err)
			}

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
			log.Error("error writing the response", "error", err)

			return
		}

		if written != size {
			log.Error("Bytes copied does not match object size", "expected", size, "written", written)
		}
	}
}

func (s *Server) putNar(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	nu := nar.URL{Hash: hash, Query: r.URL.Query()}

	log := nu.NewLogger(s.logger)

	var err error

	nu.Compression, err = nar.CompressionTypeFromExtension(chi.URLParam(r, "compression"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)

		if _, err := w.Write([]byte(http.StatusText(http.StatusBadRequest))); err != nil {
			log.Error("error computing the compression", "error", err)
		}

		return
	}

	log = nu.NewLogger(s.logger) // re-create the logger to avoid dups

	if !s.putPermitted {
		w.WriteHeader(http.StatusMethodNotAllowed)

		if _, err := w.Write([]byte(http.StatusText(http.StatusMethodNotAllowed))); err != nil {
			log.Error("error writing the body to the response", "error", err)
		}

		return
	}

	if err := s.cache.PutNar(r.Context(), nu, r.Body); err != nil {
		log.Error("error putting the NAR in cache", "error", err)
		w.WriteHeader(http.StatusInternalServerError)

		if _, err := w.Write([]byte(http.StatusText(http.StatusInternalServerError) + " " + err.Error())); err != nil {
			log.Error("error writing the body to the response", "error", err)
		}

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteNar(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	nu := nar.URL{Hash: hash, Query: r.URL.Query()}

	log := nu.NewLogger(s.logger)

	var err error

	nu.Compression, err = nar.CompressionTypeFromExtension(chi.URLParam(r, "compression"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)

		if _, err := w.Write([]byte(http.StatusText(http.StatusBadRequest))); err != nil {
			log.Error("error computing the compression", "error", err)
		}

		return
	}

	log = nu.NewLogger(s.logger) // re-create the logger to avoid dups

	if !s.deletePermitted {
		w.WriteHeader(http.StatusMethodNotAllowed)

		if _, err := w.Write([]byte(http.StatusText(http.StatusMethodNotAllowed))); err != nil {
			log.Error("error writing the body to the response", "error", err)
		}

		return
	}

	if err := s.cache.DeleteNar(r.Context(), nu); err != nil {
		if errors.Is(err, cache.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)

			if _, err := w.Write([]byte(http.StatusText(http.StatusNotFound))); err != nil {
				log.Error("error writing the body to the response", "error", err)
			}

			return
		}

		w.WriteHeader(http.StatusInternalServerError)

		if _, err := w.Write([]byte(http.StatusText(http.StatusInternalServerError) + " " + err.Error())); err != nil {
			log.Error("error writing the body to the response", "error", err)
		}

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
