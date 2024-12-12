package testdata

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func PublicKeys() []string {
	return []string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="}
}

func HTTPTestServer(t *testing.T, priority int) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nix-cache-info" {
			_, err := w.Write([]byte(NixStoreInfo(priority)))
			require.NoError(t, err)

			return
		}

		for _, entry := range Entries {
			if r.URL.Path == "/broken-"+entry.NarInfoHash+".narinfo" {
				// mutate the inside
				b := entry.NarInfoText
				b = strings.Replace(b, "References:", "References: notfound-path", -1)

				_, err := w.Write([]byte(b))
				require.NoError(t, err)

				return
			}

			if r.URL.Path == "/"+entry.NarInfoHash+".narinfo" {
				_, err := w.Write([]byte(entry.NarInfoText))
				require.NoError(t, err)

				return
			}

			if r.URL.Path == "/nar/"+entry.NarHash+".nar.xz" {
				_, err := w.Write([]byte(entry.NarText))
				require.NoError(t, err)

				return
			}
		}

		w.WriteHeader(http.StatusNotFound)
	}))
}
