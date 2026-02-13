package oidc

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNoPolicies is returned when the verifier has no policies configured.
	ErrNoPolicies = errors.New("no OIDC policies configured")

	// ErrTokenValidationFailed is returned when no provider could validate the token.
	ErrTokenValidationFailed = errors.New("token validation failed")

	// ErrClaimMismatch is returned when the token's claims do not satisfy the required patterns.
	ErrClaimMismatch = errors.New("claim mismatch")
)

// Claims represents the verified claims from an OIDC token.
type Claims struct {
	Issuer   string
	Subject  string
	Audience []string
	Extra    map[string]any
}

// Verifier validates OIDC tokens against one or more policies.
type Verifier struct {
	policies []*policy
}

// Verify validates the raw JWT token against all configured policies.
// The first policy that successfully verifies the token wins.
// After signature validation, required claims are checked.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (*Claims, error) {
	for _, p := range v.policies {
		idToken, err := p.verifier.Verify(ctx, rawToken)
		if err != nil {
			continue
		}

		// Extract all claims into a map for claim matching and extras.
		var allClaims map[string]any
		if err := idToken.Claims(&allClaims); err != nil {
			return nil, fmt.Errorf("extracting claims: %w", err)
		}

		// Check required claims if configured.
		if len(p.config.Claims) > 0 {
			if err := checkClaims(allClaims, p.config.Claims); err != nil {
				return nil, err
			}
		}

		return &Claims{
			Issuer:   idToken.Issuer,
			Subject:  idToken.Subject,
			Audience: idToken.Audience,
			Extra:    allClaims,
		}, nil
	}

	return nil, ErrTokenValidationFailed
}

// checkClaims verifies the token's claims satisfy all required patterns.
// Each key in required must be present in the token. Within a key, at least one
// pattern must match (OR). Across keys, all must match (AND).
func checkClaims(tokenClaims map[string]any, required map[string][]string) error {
	for claimKey, patterns := range required {
		tokenVal, ok := tokenClaims[claimKey]
		if !ok {
			return fmt.Errorf("%w: missing claim %q", ErrClaimMismatch, claimKey)
		}

		tokenStrings := claimValueToStrings(tokenVal)
		if !anyPatternMatches(patterns, tokenStrings) {
			return fmt.Errorf("%w: claim %q does not match any allowed pattern", ErrClaimMismatch, claimKey)
		}
	}

	return nil
}

// claimValueToStrings converts a claim value to a slice of strings for matching.
// Handles string, []any (array of strings), and falls back to fmt.Sprint.
func claimValueToStrings(val any) []string {
	switch v := val.(type) {
	case string:
		return []string{v}
	case []any:
		strs := make([]string, 0, len(v))

		for _, item := range v {
			if s, ok := item.(string); ok {
				strs = append(strs, s)
			} else {
				strs = append(strs, fmt.Sprint(item))
			}
		}

		return strs
	default:
		return []string{fmt.Sprint(v)}
	}
}

// anyPatternMatches returns true if any pattern matches any of the token values.
func anyPatternMatches(patterns, values []string) bool {
	for _, pattern := range patterns {
		for _, value := range values {
			if MatchGlob(pattern, value) {
				return true
			}
		}
	}

	return false
}

// MatchGlob performs simple glob matching where * matches any sequence of characters
// (including / and empty string). This is intentionally simpler than filepath.Match
// because claim values like "repo:org/repo" contain slashes that should be matchable.
func MatchGlob(pattern, value string) bool {
	// Fast path: no wildcards means exact match.
	if !strings.Contains(pattern, "*") {
		return pattern == value
	}

	// Normalize consecutive wildcards to a single one since they are equivalent.
	for strings.Contains(pattern, "**") {
		pattern = strings.ReplaceAll(pattern, "**", "*")
	}

	parts := strings.Split(pattern, "*")

	// Check that the value starts with the first segment and ends with the last.
	if !strings.HasPrefix(value, parts[0]) {
		return false
	}

	if !strings.HasSuffix(value, parts[len(parts)-1]) {
		return false
	}

	// Walk through remaining segments ensuring they appear in order.
	remaining := value[len(parts[0]):]

	for _, part := range parts[1:] {
		idx := strings.Index(remaining, part)
		if idx < 0 {
			return false
		}

		remaining = remaining[idx+len(part):]
	}

	return true
}
