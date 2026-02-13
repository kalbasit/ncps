package oidc_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/oidc"
)

func TestMiddleware(t *testing.T) {
	t.Parallel()

	t.Run("no auth header returns 401", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, nil)

		handler := v.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodPut, "/test", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)

		var body map[string]string
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		assert.Equal(t, "missing authorization header", body["error"])
	})

	t.Run("unsupported auth scheme returns 401", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, nil)

		handler := v.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodPut, "/test", nil)
		req.Header.Set("Authorization", "Digest realm=\"test\"")

		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("invalid Bearer token returns 401", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, nil)

		handler := v.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodPut, "/test", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")

		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)

		var body map[string]string
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		assert.Equal(t, "token validation failed", body["error"])
	})

	t.Run("valid Bearer token passes through", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, nil)

		var capturedClaims *oidc.Claims

		handler := v.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedClaims = oidc.ClaimsFromContext(r.Context())

			w.WriteHeader(http.StatusOK)
		}))

		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "repo:org/repo:ref:refs/heads/main",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, nil)

		req := httptest.NewRequest(http.MethodPut, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		require.NotNil(t, capturedClaims)
		assert.Equal(t, "repo:org/repo:ref:refs/heads/main", capturedClaims.Subject)
		assert.Equal(t, mk.issuer(), capturedClaims.Issuer)
	})

	t.Run("valid Basic auth token passes through", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, nil)

		var capturedClaims *oidc.Claims

		handler := v.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedClaims = oidc.ClaimsFromContext(r.Context())

			w.WriteHeader(http.StatusOK)
		}))

		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "repo:myorg/myrepo",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, nil)

		// Encode as Basic auth with empty username (netrc password-only style).
		basicCreds := base64.StdEncoding.EncodeToString([]byte(":" + token))

		req := httptest.NewRequest(http.MethodPut, "/test", nil)
		req.Header.Set("Authorization", "Basic "+basicCreds)

		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		require.NotNil(t, capturedClaims)
		assert.Equal(t, "repo:myorg/myrepo", capturedClaims.Subject)
	})

	t.Run("Basic auth with username and token in password passes through", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, nil)

		handler := v.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "repo:myorg/myrepo",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, nil)

		// Encode as Basic auth with a username (netrc login+password style).
		basicCreds := base64.StdEncoding.EncodeToString([]byte("bearer:" + token))

		req := httptest.NewRequest(http.MethodPut, "/test", nil)
		req.Header.Set("Authorization", "Basic "+basicCreds)

		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("Basic auth with empty password returns 401", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, nil)

		handler := v.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		basicCreds := base64.StdEncoding.EncodeToString([]byte("user:"))

		req := httptest.NewRequest(http.MethodPut, "/test", nil)
		req.Header.Set("Authorization", "Basic "+basicCreds)

		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("Basic auth with invalid JWT in password returns 401", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, nil)

		handler := v.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		basicCreds := base64.StdEncoding.EncodeToString([]byte("user:not-a-jwt"))

		req := httptest.NewRequest(http.MethodPut, "/test", nil)
		req.Header.Set("Authorization", "Basic "+basicCreds)

		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)

		var body map[string]string
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		assert.Equal(t, "token validation failed", body["error"])
	})

	t.Run("claim mismatch returns 403", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, map[string][]string{
			"sub": {"repo:allowed-org/*"},
		})

		handler := v.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "repo:other-org/repo",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, nil)

		req := httptest.NewRequest(http.MethodPut, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code)

		var body map[string]string
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		assert.Equal(t, "claim mismatch", body["error"])
	})

	t.Run("matching claims pass through", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, map[string][]string{
			"sub": {"repo:myorg/*"},
		})

		handler := v.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "repo:myorg/myrepo",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, nil)

		req := httptest.NewRequest(http.MethodPut, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("ClaimsFromContext returns nil when no claims", func(t *testing.T) {
		t.Parallel()

		claims := oidc.ClaimsFromContext(context.Background())
		assert.Nil(t, claims)
	})
}
