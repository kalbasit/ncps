package oidc_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/oidc"
)

func TestVerifier_Verify(t *testing.T) {
	t.Parallel()

	t.Run("valid token", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, nil)
		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "user:123",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, nil)

		claims, err := v.Verify(context.Background(), token)
		require.NoError(t, err)
		assert.Equal(t, mk.issuer(), claims.Issuer)
		assert.Equal(t, "user:123", claims.Subject)
		assert.Equal(t, []string{"test-audience"}, claims.Audience)
	})

	t.Run("expired token returns error", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, nil)
		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "user:123",
			Expiry:   jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		}, nil)

		claims, err := v.Verify(context.Background(), token)
		require.Error(t, err)
		assert.Nil(t, claims)
		assert.ErrorIs(t, err, oidc.ErrTokenValidationFailed)
	})

	t.Run("wrong audience returns error", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, nil)
		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"wrong-audience"},
			Subject:  "user:123",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, nil)

		claims, err := v.Verify(context.Background(), token)
		require.Error(t, err)
		assert.Nil(t, claims)
		assert.ErrorIs(t, err, oidc.ErrTokenValidationFailed)
	})

	t.Run("claim matching with exact sub", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, map[string][]string{
			"sub": {"repo:myorg/myrepo:ref:refs/heads/main"},
		})
		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "repo:myorg/myrepo:ref:refs/heads/main",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, nil)

		claims, err := v.Verify(context.Background(), token)
		require.NoError(t, err)
		assert.Equal(t, "repo:myorg/myrepo:ref:refs/heads/main", claims.Subject)
	})

	t.Run("claim matching with glob pattern", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, map[string][]string{
			"sub": {"repo:myorg/*"},
		})
		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "repo:myorg/myrepo:ref:refs/heads/main",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, nil)

		claims, err := v.Verify(context.Background(), token)
		require.NoError(t, err)
		assert.Equal(t, "repo:myorg/myrepo:ref:refs/heads/main", claims.Subject)
	})

	t.Run("claim matching with multiple patterns (OR within key)", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, map[string][]string{
			"ref": {"refs/heads/main", "refs/tags/*"},
		})
		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "user:123",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, map[string]any{
			"ref": "refs/tags/v1.0.0",
		})

		claims, err := v.Verify(context.Background(), token)
		require.NoError(t, err)
		assert.Equal(t, "user:123", claims.Subject)
	})

	t.Run("claim matching with multiple keys (AND across keys)", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, map[string][]string{
			"sub": {"repo:myorg/*"},
			"ref": {"refs/heads/main"},
		})
		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "repo:myorg/myrepo",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, map[string]any{
			"ref": "refs/heads/main",
		})

		claims, err := v.Verify(context.Background(), token)
		require.NoError(t, err)
		assert.Equal(t, "repo:myorg/myrepo", claims.Subject)
	})

	t.Run("claim mismatch on sub returns error", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, map[string][]string{
			"sub": {"repo:myorg/*"},
		})
		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "repo:otherorg/repo",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, nil)

		claims, err := v.Verify(context.Background(), token)
		require.Error(t, err)
		assert.Nil(t, claims)
		assert.ErrorIs(t, err, oidc.ErrClaimMismatch)
	})

	t.Run("claim mismatch on one key of multiple returns error", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, map[string][]string{
			"sub": {"repo:myorg/*"},
			"ref": {"refs/heads/main"},
		})
		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "repo:myorg/myrepo",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, map[string]any{
			"ref": "refs/heads/feature",
		})

		claims, err := v.Verify(context.Background(), token)
		require.Error(t, err)
		assert.Nil(t, claims)
		assert.ErrorIs(t, err, oidc.ErrClaimMismatch)
	})

	t.Run("missing claim returns error", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, map[string][]string{
			"ref": {"refs/heads/main"},
		})
		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "user:123",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, nil)

		claims, err := v.Verify(context.Background(), token)
		require.Error(t, err)
		assert.Nil(t, claims)
		assert.ErrorIs(t, err, oidc.ErrClaimMismatch)
	})

	t.Run("claim matching with array claim value", func(t *testing.T) {
		t.Parallel()

		mk := newMockOIDC(t)
		v := mk.newVerifier(t, map[string][]string{
			"groups": {"admin-*"},
		})
		token := mk.issueToken(t, jwt.Claims{
			Issuer:   mk.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "user:123",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, map[string]any{
			"groups": []any{"dev-team", "admin-ops"},
		})

		claims, err := v.Verify(context.Background(), token)
		require.NoError(t, err)
		assert.Equal(t, "user:123", claims.Subject)
	})

	t.Run("multi-provider second match works", func(t *testing.T) {
		t.Parallel()

		mk1 := newMockOIDC(t)
		mk2 := newMockOIDC(t)

		cfg := &oidc.Config{
			Policies: []oidc.PolicyConfig{
				{
					Issuer:   mk1.issuer(),
					Audience: "test-audience",
				},
				{
					Issuer:   mk2.issuer(),
					Audience: "test-audience",
				},
			},
		}

		v, err := oidc.New(context.Background(), cfg)
		require.NoError(t, err)

		// Issue token from second provider.
		token := mk2.issueToken(t, jwt.Claims{
			Issuer:   mk2.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "second-provider-user",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, nil)

		claims, err := v.Verify(context.Background(), token)
		require.NoError(t, err)
		assert.Equal(t, "second-provider-user", claims.Subject)
		assert.Equal(t, mk2.issuer(), claims.Issuer)
	})

	t.Run("multi-provider no match returns error", func(t *testing.T) {
		t.Parallel()

		mk1 := newMockOIDC(t)
		mk2 := newMockOIDC(t)
		mkOther := newMockOIDC(t)

		cfg := &oidc.Config{
			Policies: []oidc.PolicyConfig{
				{
					Issuer:   mk1.issuer(),
					Audience: "test-audience",
				},
				{
					Issuer:   mk2.issuer(),
					Audience: "test-audience",
				},
			},
		}

		v, err := oidc.New(context.Background(), cfg)
		require.NoError(t, err)

		// Issue token from an unknown provider.
		token := mkOther.issueToken(t, jwt.Claims{
			Issuer:   mkOther.issuer(),
			Audience: jwt.Audience{"test-audience"},
			Subject:  "unknown-user",
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt: jwt.NewNumericDate(time.Now()),
		}, nil)

		claims, err := v.Verify(context.Background(), token)
		require.Error(t, err)
		assert.Nil(t, claims)
		assert.ErrorIs(t, err, oidc.ErrTokenValidationFailed)
	})
}

func TestMatchGlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		value   string
		want    bool
	}{
		{"exact match", "repo:org/repo", "repo:org/repo", true},
		{"exact mismatch", "repo:org/repo", "repo:org/other", false},
		{"trailing wildcard", "repo:org/*", "repo:org/myrepo", true},
		{"trailing wildcard with slashes", "repo:org/*", "repo:org/my/deep/repo", true},
		{"leading wildcard", "*:refs/heads/main", "ref:refs/heads/main", true},
		{"middle wildcard", "repo:org/*/ref:*", "repo:org/myrepo/ref:refs/heads/main", true},
		{"star matches empty", "repo:org/*", "repo:org/", true},
		{"full wildcard", "*", "anything-goes", true},
		{"no match prefix", "repo:other/*", "repo:org/myrepo", false},
		{"consecutive wildcards", "repo:**/main", "repo:org/repo/main", true},
		{"many consecutive wildcards", "a***b", "aXYZb", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, oidc.MatchGlob(tt.pattern, tt.value))
		})
	}
}

// mockOIDC is a test OIDC provider that serves discovery and JWKS endpoints.
type mockOIDC struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	keyID  string
}

func newMockOIDC(t *testing.T) *mockOIDC {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	mk := &mockOIDC{
		key:   key,
		keyID: "test-key-1",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", mk.handleDiscovery)
	mux.HandleFunc("/jwks", mk.handleJWKS)

	mk.server = httptest.NewServer(mux)
	t.Cleanup(mk.server.Close)

	return mk
}

func (m *mockOIDC) issuer() string {
	return m.server.URL
}

func (m *mockOIDC) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	doc := map[string]any{
		"issuer":                                m.issuer(),
		"jwks_uri":                              m.server.URL + "/jwks",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func (m *mockOIDC) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	jwks := jose.JSONWebKeySet{
		Keys: []jose.JSONWebKey{
			{
				Key:       &m.key.PublicKey,
				KeyID:     m.keyID,
				Algorithm: string(jose.RS256),
				Use:       "sig",
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jwks)
}

func (m *mockOIDC) issueToken(t *testing.T, claims jwt.Claims, extraClaims map[string]any) string {
	t.Helper()

	signerOpts := jose.SignerOptions{}
	signerOpts.WithType("JWT")
	signerOpts.WithHeader(jose.HeaderKey("kid"), m.keyID)

	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: m.key},
		&signerOpts,
	)
	require.NoError(t, err)

	builder := jwt.Signed(signer).Claims(claims)
	if extraClaims != nil {
		builder = builder.Claims(extraClaims)
	}

	token, err := builder.Serialize()
	require.NoError(t, err)

	return token
}

func (m *mockOIDC) newVerifier(t *testing.T, claims map[string][]string) *oidc.Verifier {
	t.Helper()

	cfg := &oidc.Config{
		Policies: []oidc.PolicyConfig{
			{
				Issuer:   m.issuer(),
				Audience: "test-audience",
				Claims:   claims,
			},
		},
	}

	v, err := oidc.New(context.Background(), cfg)
	require.NoError(t, err)

	return v
}
