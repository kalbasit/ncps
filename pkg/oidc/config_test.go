package oidc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/oidc"
)

func TestParseConfigData(t *testing.T) {
	t.Parallel()

	t.Run("empty data returns nil", func(t *testing.T) {
		t.Parallel()

		cfg, err := oidc.ParseConfigData(nil, "yaml")
		require.NoError(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("YAML with no oidc section returns nil", func(t *testing.T) {
		t.Parallel()

		data := []byte(`cache:
  hostname: "test.example.com"
`)

		cfg, err := oidc.ParseConfigData(data, "yaml")
		require.NoError(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("YAML with empty policies returns nil", func(t *testing.T) {
		t.Parallel()

		data := []byte(`cache:
  oidc:
    policies: []
`)

		cfg, err := oidc.ParseConfigData(data, "yaml")
		require.NoError(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("YAML with single policy", func(t *testing.T) {
		t.Parallel()

		data := []byte(`cache:
  oidc:
    policies:
      - issuer: "https://token.actions.githubusercontent.com"
        audience: "ncps.example.com"
`)

		cfg, err := oidc.ParseConfigData(data, "yaml")
		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.Len(t, cfg.Policies, 1)
		assert.Equal(t, "https://token.actions.githubusercontent.com", cfg.Policies[0].Issuer)
		assert.Equal(t, "ncps.example.com", cfg.Policies[0].Audience)
		assert.Empty(t, cfg.Policies[0].Claims)
	})

	t.Run("YAML with multiple policies and claims", func(t *testing.T) {
		t.Parallel()

		data := []byte(`cache:
  oidc:
    policies:
      - issuer: "https://token.actions.githubusercontent.com"
        audience: "ncps.example.com"
        claims:
          sub:
            - "repo:myorg/*"
          ref:
            - "refs/heads/main"
            - "refs/tags/*"
      - issuer: "https://gitlab.example.com"
        audience: "ncps.internal"
`)

		cfg, err := oidc.ParseConfigData(data, "yaml")
		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.Len(t, cfg.Policies, 2)
		assert.Equal(t, "https://token.actions.githubusercontent.com", cfg.Policies[0].Issuer)
		assert.Equal(t, map[string][]string{
			"sub": {"repo:myorg/*"},
			"ref": {"refs/heads/main", "refs/tags/*"},
		}, cfg.Policies[0].Claims)
		assert.Equal(t, "https://gitlab.example.com", cfg.Policies[1].Issuer)
		assert.Empty(t, cfg.Policies[1].Claims)
	})

	t.Run("JSON config", func(t *testing.T) {
		t.Parallel()

		data := []byte(`{
  "cache": {
    "oidc": {
      "policies": [
        {
          "issuer": "https://accounts.google.com",
          "audience": "my-app",
          "claims": {
            "sub": ["user@example.com"]
          }
        }
      ]
    }
  }
}`)

		cfg, err := oidc.ParseConfigData(data, "json")
		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.Len(t, cfg.Policies, 1)
		assert.Equal(t, "https://accounts.google.com", cfg.Policies[0].Issuer)
		assert.Equal(t, map[string][]string{"sub": {"user@example.com"}}, cfg.Policies[0].Claims)
	})

	t.Run("TOML config", func(t *testing.T) {
		t.Parallel()

		data := []byte(`[cache.oidc]
[[cache.oidc.policies]]
issuer = "https://token.actions.githubusercontent.com"
audience = "ncps.example.com"
`)

		cfg, err := oidc.ParseConfigData(data, "toml")
		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.Len(t, cfg.Policies, 1)
		assert.Equal(t, "https://token.actions.githubusercontent.com", cfg.Policies[0].Issuer)
	})

	t.Run("unsupported format returns error", func(t *testing.T) {
		t.Parallel()

		cfg, err := oidc.ParseConfigData([]byte("[section]\nkey=value\n"), "ini")
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.ErrorIs(t, err, oidc.ErrUnsupportedFormat)
	})

	t.Run("missing issuer returns error", func(t *testing.T) {
		t.Parallel()

		data := []byte(`cache:
  oidc:
    policies:
      - audience: "ncps.example.com"
`)

		cfg, err := oidc.ParseConfigData(data, "yaml")
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.ErrorIs(t, err, oidc.ErrMissingIssuer)
	})

	t.Run("missing audience returns error", func(t *testing.T) {
		t.Parallel()

		data := []byte(`cache:
  oidc:
    policies:
      - issuer: "https://token.actions.githubusercontent.com"
`)

		cfg, err := oidc.ParseConfigData(data, "yaml")
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.ErrorIs(t, err, oidc.ErrMissingAudience)
	})
}
