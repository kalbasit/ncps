package testdata

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/kalbasit/ncps/pkg/helper"
)

type Server struct {
	*httptest.Server

	mu            sync.RWMutex
	maybeHandlers map[string]MaybeHandlerFunc
	priority      int
	publicKeys    []string
}

type MaybeHandlerFunc func(http.ResponseWriter, *http.Request) bool

func PublicKeys() []string {
	return []string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="}
}

func NewTestServer(t *testing.T, priority int) *Server {
	t.Helper()

	s := &Server{
		maybeHandlers: make(map[string]MaybeHandlerFunc),
		priority:      priority,
	}

	s.Server = httptest.NewServer(compressMiddleware(s.handler()))

	return s
}

func (s *Server) AddPublicKey(pk string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.publicKeys = append(s.publicKeys, pk)
}

func (s *Server) AddMaybeHandler(maybeHandler MaybeHandlerFunc) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var idx string

	for {
		idx = helper.MustRandString(10, nil)
		if _, ok := s.maybeHandlers[idx]; !ok {
			break
		}
	}

	s.maybeHandlers[idx] = maybeHandler

	return idx
}

func (s *Server) RemoveMaybeHandler(idx string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.maybeHandlers, idx)
}

func compressMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "zstd" {
			next.ServeHTTP(w, r)

			return
		}

		encoder, err := zstd.NewWriter(w)
		if !requireNoError(w, err) {
			return
		}
		defer encoder.Close()

		zw := &zstdResponseWriter{Writer: encoder, ResponseWriter: w}

		next.ServeHTTP(zw, r)
	})
}

func (s *Server) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		defer s.mu.RUnlock()

		for _, handler := range s.maybeHandlers {
			if handler(w, r) {
				return
			}
		}

		if p := r.Header.Get("ping"); p != "" {
			w.Header().Add("pong", p)
		}

		if r.URL.Path == "/nix-cache-info" {
			_, err := w.Write([]byte(NixStoreInfo(s.priority)))
			requireNoError(w, err)

			return
		}

		for _, entry := range Entries {
			var bs []byte

			if r.URL.Path == "/"+entry.NarInfoHash+".narinfo" {
				bs = []byte(entry.NarInfoText)
			}

			if r.URL.Path == "/nar/"+entry.NarHash+".nar" {
				bs = []byte(entry.NarText)
			}

			if r.URL.Path == "/nar/"+entry.NarHash+".nar."+entry.NarCompression.ToFileExtension() {
				bs = []byte(entry.NarText)
			}

			if len(bs) > 0 {
				if s := r.URL.Query().Get("fakesize"); s != "" {
					size, err := strconv.Atoi(s)
					if !requireNoError(w, err) {
						return
					}

					w.Header().Add("Content-Length", s)
					_, err = w.Write([]byte(strings.Repeat("a", size)))
					requireNoError(w, err)

					return
				}

				w.Header().Add("Content-Length", strconv.Itoa(len(bs)))

				_, err := w.Write(bs)
				requireNoError(w, err)

				return
			}
		}

		w.WriteHeader(http.StatusNotFound)
	})
}

func requireNoError(w http.ResponseWriter, err error) bool {
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)

		//nolint:errcheck
		w.Write([]byte(err.Error()))

		return false
	}

	return true
}

// zstdResponseWriter wraps an http.ResponseWriter to capture the response body.
type zstdResponseWriter struct {
	io.Writer
	http.ResponseWriter

	wroteHeader bool
}

func (zw *zstdResponseWriter) Write(p []byte) (n int, err error) {
	if !zw.wroteHeader {
		zw.WriteHeader(http.StatusOK)
	}

	return zw.Writer.Write(p)
}

func (zw *zstdResponseWriter) WriteHeader(code int) {
	if zw.wroteHeader {
		zw.ResponseWriter.WriteHeader(code)

		return
	}

	zw.wroteHeader = true

	zw.Header().Set("Content-Encoding", "zstd")
	zw.Header().Del("Content-Length")
}
