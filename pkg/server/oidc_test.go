package server_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/oidc"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testhelper"
)

const nixCacheInfoPath = "/nix-cache-info"

// oidcTestServer is a mock OIDC provider for integration tests.
type oidcTestServer struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	keyID  string
}

func newOIDCTestServer(t *testing.T) *oidcTestServer {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	ots := &oidcTestServer{
		key:   key,
		keyID: "test-key-1",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", ots.handleDiscovery)
	mux.HandleFunc("/jwks", ots.handleJWKS)

	ots.server = httptest.NewServer(mux)
	t.Cleanup(ots.server.Close)

	return ots
}

func (o *oidcTestServer) issuer() string {
	return o.server.URL
}

func (o *oidcTestServer) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	doc := map[string]any{
		"issuer":                                o.issuer(),
		"jwks_uri":                              o.server.URL + "/jwks",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func (o *oidcTestServer) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	jwks := jose.JSONWebKeySet{
		Keys: []jose.JSONWebKey{
			{
				Key:       &o.key.PublicKey,
				KeyID:     o.keyID,
				Algorithm: string(jose.RS256),
				Use:       "sig",
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jwks)
}

func (o *oidcTestServer) issueToken(t *testing.T, subject string) string {
	t.Helper()

	signerOpts := jose.SignerOptions{}
	signerOpts.WithType("JWT")
	signerOpts.WithHeader(jose.HeaderKey("kid"), o.keyID)

	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: o.key},
		&signerOpts,
	)
	require.NoError(t, err)

	token, err := jwt.Signed(signer).Claims(jwt.Claims{
		Issuer:   o.issuer(),
		Audience: jwt.Audience{"test-audience"},
		Subject:  subject,
		Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
		IssuedAt: jwt.NewNumericDate(time.Now()),
	}).Serialize()
	require.NoError(t, err)

	return token
}

func (o *oidcTestServer) newVerifier(t *testing.T) *oidc.Verifier {
	t.Helper()

	cfg := &oidc.Config{
		Policies: []oidc.PolicyConfig{
			{
				Issuer:   o.issuer(),
				Audience: "test-audience",
			},
		},
	}

	v, err := oidc.New(context.Background(), cfg)
	require.NoError(t, err)

	return v
}

func setupOIDCTestServer(t *testing.T) (*httptest.Server, *oidcTestServer) {
	t.Helper()

	// Setup a dummy upstream server
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == nixCacheInfoPath {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("StoreDir: /nix/store\nWantMassQuery: 1\nPriority: 40"))

			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(upstreamSrv.Close)

	// Setup database and storage
	dir, err := os.MkdirTemp("", "ncps-oidc-test-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })

	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	ls, err := local.New(context.Background(), dir)
	require.NoError(t, err)

	c, err := cache.New(
		context.Background(), "localhost", db, ls, ls, ls, "",
		locklocal.NewLocker(), locklocal.NewRWLocker(),
		time.Minute, 30*time.Second, time.Minute,
	)
	require.NoError(t, err)

	uc, err := upstream.New(context.Background(), testhelper.MustParseURL(t, upstreamSrv.URL), nil)
	require.NoError(t, err)

	c.AddUpstreamCaches(context.Background(), uc)
	<-c.GetHealthChecker().Trigger()

	// Setup OIDC mock
	ots := newOIDCTestServer(t)
	v := ots.newVerifier(t)

	// Create server with OIDC verifier
	srv := server.New(c, server.WithOIDCVerifier(v))
	srv.SetPutPermitted(true)
	srv.SetDeletePermitted(true)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts, ots
}

//nolint:paralleltest
func TestOIDCIntegration(t *testing.T) {
	t.Run("PUT with OIDC enabled and no token returns 401", func(t *testing.T) {
		ts, _ := setupOIDCTestServer(t)

		req, err := http.NewRequestWithContext(
			context.Background(),
			http.MethodPut,
			ts.URL+"/upload/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1.narinfo",
			strings.NewReader("fake-narinfo"),
		)
		require.NoError(t, err)

		resp, err := ts.Client().Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("PUT with OIDC enabled and valid token reaches handler", func(t *testing.T) {
		ts, ots := setupOIDCTestServer(t)

		token := ots.issueToken(t, "repo:org/repo:ref:refs/heads/main")

		req, err := http.NewRequestWithContext(
			context.Background(),
			http.MethodPut,
			ts.URL+"/upload/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2.narinfo",
			strings.NewReader("fake-narinfo"),
		)
		require.NoError(t, err)

		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := ts.Client().Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		// Should reach handler (may fail due to invalid narinfo content, but not 401)
		assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode)
		assert.NotEqual(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("DELETE with OIDC enabled and no token returns 401", func(t *testing.T) {
		ts, _ := setupOIDCTestServer(t)

		req, err := http.NewRequestWithContext(
			context.Background(),
			http.MethodDelete,
			ts.URL+"/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa3.narinfo",
			nil,
		)
		require.NoError(t, err)

		resp, err := ts.Client().Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("GET with OIDC enabled and no token still works", func(t *testing.T) {
		ts, _ := setupOIDCTestServer(t)

		req, err := http.NewRequestWithContext(
			context.Background(),
			http.MethodGet,
			ts.URL+nixCacheInfoPath,
			nil,
		)
		require.NoError(t, err)

		resp, err := ts.Client().Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("GET narinfo with OIDC enabled and no token still works", func(t *testing.T) {
		ts, _ := setupOIDCTestServer(t)

		req, err := http.NewRequestWithContext(
			context.Background(),
			http.MethodGet,
			ts.URL+"/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa4.narinfo",
			nil,
		)
		require.NoError(t, err)

		resp, err := ts.Client().Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		// Should NOT be 401 (it's a GET). Will be 404 since narinfo doesn't exist.
		assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("no OIDC verifier allows PUT without token", func(t *testing.T) {
		// Setup without OIDC verifier
		upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == nixCacheInfoPath {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("StoreDir: /nix/store\nWantMassQuery: 1\nPriority: 40"))

				return
			}

			w.WriteHeader(http.StatusNotFound)
		}))
		t.Cleanup(upstreamSrv.Close)

		dir, err := os.MkdirTemp("", "ncps-no-oidc-")
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(dir) })

		dbFile := filepath.Join(dir, "db.sqlite")
		testhelper.CreateMigrateDatabase(t, dbFile)

		db, err := database.Open("sqlite:"+dbFile, nil)
		require.NoError(t, err)

		ls, err := local.New(context.Background(), dir)
		require.NoError(t, err)

		c, err := cache.New(
			context.Background(), "localhost", db, ls, ls, ls, "",
			locklocal.NewLocker(), locklocal.NewRWLocker(),
			time.Minute, 30*time.Second, time.Minute,
		)
		require.NoError(t, err)

		uc, err := upstream.New(context.Background(), testhelper.MustParseURL(t, upstreamSrv.URL), nil)
		require.NoError(t, err)

		c.AddUpstreamCaches(context.Background(), uc)
		<-c.GetHealthChecker().Trigger()

		// No OIDC verifier
		srv := server.New(c)
		srv.SetPutPermitted(true)

		ts := httptest.NewServer(srv)
		t.Cleanup(ts.Close)

		req, err := http.NewRequestWithContext(
			context.Background(),
			http.MethodPut,
			ts.URL+"/upload/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa5.narinfo",
			strings.NewReader("fake-narinfo"),
		)
		require.NoError(t, err)

		resp, err := ts.Client().Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		// Should NOT be 401 (no OIDC configured)
		assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode)
	})
}
