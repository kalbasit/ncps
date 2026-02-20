package oidc

import (
	"context"
	"fmt"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
)

// policy wraps a single OIDC authorization policy with its token verifier and config.
type policy struct {
	config   PolicyConfig
	verifier *gooidc.IDTokenVerifier
}

// newPolicy performs OIDC discovery for the given issuer and creates a token
// verifier. It fails if the issuer is unreachable (startup error).
func newPolicy(ctx context.Context, cfg PolicyConfig) (*policy, error) {
	p, err := gooidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery for issuer %q: %w", cfg.Issuer, err)
	}

	verifier := p.Verifier(&gooidc.Config{
		ClientID: cfg.Audience,
	})

	return &policy{
		config:   cfg,
		verifier: verifier,
	}, nil
}
