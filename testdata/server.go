package testdata

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// zstdResponseWriter wraps an http.ResponseWriter to capture the response body.
type zstdResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (zw *zstdResponseWriter) Write(p []byte) (n int, err error) {
	return zw.Writer.Write(p)
}

func PublicKeys() []string {
	return []string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="}
}

func HTTPTestServer(t *testing.T, priority int) *httptest.Server {
	t.Helper()

	return httptest.NewServer(compressMiddleware(handler(priority)))
}

func compressMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "zstd" {
			next.ServeHTTP(w, r)

			return
		}

		w.Header().Set("Content-Encoding", "zstd")

		encoder, err := zstd.NewWriter(w)
		if !requireNoError(w, err) {
			return
		}
		defer encoder.Close()

		zw := &zstdResponseWriter{Writer: encoder, ResponseWriter: w}

		next.ServeHTTP(zw, r)
	})
}

func handler(priority int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.Header.Get("ping"); p != "" {
			w.Header().Add("pong", p)
		}

		if r.URL.Path == "/nix-cache-info" {
			_, err := w.Write([]byte(NixStoreInfo(priority)))
			requireNoError(w, err)

			return
		}

		for _, entry := range Entries {
			var bs []byte

			if r.URL.Path == "/broken-"+entry.NarInfoHash+".narinfo" {
				// mutate the inside
				b := entry.NarInfoText
				b = strings.Replace(b, "References:", "References: notfound-path", -1)

				bs = []byte(b)
			}

			if r.URL.Path == "/"+entry.NarInfoHash+".narinfo" {
				bs = []byte(entry.NarInfoText)
			}

			if r.URL.Path == "/nar/"+entry.NarHash+".nar.xz" {
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
