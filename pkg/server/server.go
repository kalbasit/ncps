package server

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/inconshreveable/log15/v3"
	"github.com/kalbasit/ncps/pkg/cache"
)

const (
	routeNar       = "/nar/{hash:[a-z0-9]+}.nar.{compression:*}"
	routeNarInfo   = "/{hash:[a-z0-9]+}.narinfo"
	routeCacheInfo = "/nix-cache-info"

	contentLength      = "Content-Length"
	contentType        = "Content-Type"
	contentTypeNar     = "application/x-nix-nar"
	contentTypeNarInfo = "text/x-nix-narinfo"

	nixCacheInfo = `StoreDir: /nix/store
WantMassQuery: 1
Priority: 10
`
)

type Server struct {
	cache  cache.Cache
	logger log15.Logger
	router *chi.Mux
}

func New(logger log15.Logger, cache cache.Cache) (Server, error) {
	s := Server{
		cache:  cache,
		logger: logger,
	}

	router, err := createRouter(s)
	if err != nil {
		return s, fmt.Errorf("error creating the router: %w", err)
	}
	s.router = router
	return s, nil
}

func (s Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.router.ServeHTTP(w, r) }

func createRouter(s Server) (*chi.Mux, error) {
	router := chi.NewRouter()

	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	// router.Use(middleware.Timeout(60 * time.Second))
	router.Use(requestLogger(s.logger))
	router.Use(middleware.Recoverer)

	router.Get(routeCacheInfo, s.getNixCacheInfo)

	router.Head(routeNarInfo, s.getNarInfo(false))
	router.Get(routeNarInfo, s.getNarInfo(true))
	// router.Put(routeNarInfo, s.putNarInfo())
	//
	// router.Head(routeNar, s.getNar(false))
	// router.Get(routeNar, s.getNar(true))
	// router.Put(routeNar, s.putNar())

	return router, nil
}

func requestLogger(logger log15.Logger) func(handler http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()
			reqId := middleware.GetReqID(r.Context())

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				entries := []interface{}{
					"status", ww.Status(),
					"elapsed", time.Since(startedAt),
					"from", r.RemoteAddr,
					"reqId", reqId,
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

func (s Server) getNixCacheInfo(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte(nixCacheInfo))
}

func (s Server) getNarInfo(withBody bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, info, err := s.cache.GetNarInfo(chi.URLParam(r, "hash"))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(http.StatusText(http.StatusNotFound)))
				return
			}

			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(http.StatusText(http.StatusInternalServerError)))
			return
		}

		h := w.Header()
		h.Set(contentType, contentTypeNarInfo)
		h.Set(contentLength, strconv.FormatInt(info.Size(), 10))

		if !withBody {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		written, err := io.CopyN(w, f, int64(info.Size()))
		if written != info.Size() {
			s.logger.Error("Bytes copied does not match object size", "expected", info.Size(), "written", written)
		}
	}
}
