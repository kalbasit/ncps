package testdata

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func PublicKeys() []string {
	return []string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="}
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

func HTTPTestServer(t *testing.T, priority int) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nix-cache-info" {
			_, err := w.Write([]byte(NixStoreInfo(priority)))
			requireNoError(w, err)
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
				w.Header().Add("Content-Length", strconv.Itoa(len(bs)))

				_, err := w.Write(bs)
				requireNoError(w, err)

				return
			}
		}

		w.WriteHeader(http.StatusNotFound)
	}))
}
