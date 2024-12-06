package testdata

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func HTTPTestServer(t *testing.T, priority int) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nix-cache-info" {
			if _, err := w.Write([]byte(NixStoreInfo(priority))); err != nil {
				t.Fatalf("expected no error got: %s", err)
			}

			return
		}

		for _, entry := range Entries {
			if r.URL.Path == "/broken-"+entry.NarInfoHash+".narinfo" {
				// mutate the inside
				b := entry.NarInfoText
				b = strings.Replace(b, "References:", "References: notfound-path", -1)

				if _, err := w.Write([]byte(b)); err != nil {
					t.Fatalf("error writing the nar to the response: %s", err)
				}

				return
			}

			if r.URL.Path == "/"+entry.NarInfoHash+".narinfo" {
				if _, err := w.Write([]byte(entry.NarInfoText)); err != nil {
					t.Fatalf("error writing the nar to the response: %s", err)
				}

				return
			}

			if r.URL.Path == "/nar/"+entry.NarHash+".nar" {
				if _, err := w.Write([]byte(entry.NarText)); err != nil {
					t.Fatalf("error writing the nar to the response: %s", err)
				}

				return
			}

			if r.URL.Path == "/nar/"+entry.NarHash+".nar.xz" {
				if _, err := w.Write([]byte(entry.NarText)); err != nil {
					t.Fatalf("error writing the nar to the response: %s", err)
				}

				return
			}
		}

		w.WriteHeader(http.StatusNotFound)
	}))
}
