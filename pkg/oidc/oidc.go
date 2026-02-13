package oidc

import (
	"context"
	"fmt"
)

// New creates a Verifier that validates tokens against all configured OIDC
// policies. It performs OIDC discovery for each unique issuer at startup and
// returns an error if any issuer is unreachable.
func New(ctx context.Context, cfg *Config) (*Verifier, error) {
	if cfg == nil || len(cfg.Policies) == 0 {
		return nil, ErrNoPolicies
	}

	policies := make([]*policy, 0, len(cfg.Policies))

	for _, pc := range cfg.Policies {
		p, err := newPolicy(ctx, pc)
		if err != nil {
			return nil, fmt.Errorf("initializing OIDC policy: %w", err)
		}

		policies = append(policies, p)
	}

	return &Verifier{policies: policies}, nil
}
