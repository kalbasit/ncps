package oidc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const middlewareOtelPackageName = "github.com/kalbasit/ncps/pkg/oidc"

//nolint:gochecknoglobals
var middlewareTracer trace.Tracer

//nolint:gochecknoinits
func init() {
	middlewareTracer = otel.Tracer(middlewareOtelPackageName)
}

type claimsContextKey struct{}

// ClaimsFromContext retrieves the OIDC claims stored in the request context.
func ClaimsFromContext(ctx context.Context) *Claims {
	claims, _ := ctx.Value(claimsContextKey{}).(*Claims)

	return claims
}

// Middleware returns a Chi-compatible HTTP middleware that validates OIDC tokens.
// Accepts both Bearer tokens and Basic auth (password field used as the JWT).
// Basic auth support allows tools like nix copy to authenticate via netrc.
// On success, the verified Claims are stored in the request context.
func (v *Verifier) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, span := middlewareTracer.Start(
				r.Context(),
				"oidc.verifyToken",
				trace.WithSpanKind(trace.SpanKindServer),
			)
			defer span.End()

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeJSONError(w, http.StatusUnauthorized, "missing authorization header")

				zerolog.Ctx(ctx).Warn().Msg("OIDC: missing authorization header")

				return
			}

			rawToken, ok := extractToken(authHeader)
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "invalid authorization header format")

				zerolog.Ctx(ctx).Warn().Msg("OIDC: invalid authorization header format")

				return
			}

			claims, err := v.Verify(ctx, rawToken)
			if err != nil {
				if errors.Is(err, ErrClaimMismatch) {
					writeJSONError(w, http.StatusForbidden, "claim mismatch")

					zerolog.Ctx(ctx).Warn().Err(err).Msg("OIDC: claim mismatch")

					return
				}

				writeJSONError(w, http.StatusUnauthorized, "token validation failed")

				zerolog.Ctx(ctx).Warn().Err(err).Msg("OIDC: token validation failed")

				return
			}

			span.SetAttributes(
				attribute.String("oidc.issuer", claims.Issuer),
				attribute.String("oidc.subject", claims.Subject),
			)

			zerolog.Ctx(ctx).Info().
				Str("oidc_issuer", claims.Issuer).
				Str("oidc_subject", claims.Subject).
				Msg("OIDC: token verified")

			ctx = context.WithValue(ctx, claimsContextKey{}, claims)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractToken extracts the JWT from an Authorization header.
// Supports "Bearer <token>" and "Basic <base64>" (using the password as the JWT).
func extractToken(authHeader string) (string, bool) {
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 {
		return "", false
	}

	scheme := parts[0]
	credentials := parts[1]

	switch {
	case strings.EqualFold(scheme, "Bearer"):
		return credentials, true

	case strings.EqualFold(scheme, "Basic"):
		decoded, err := base64.StdEncoding.DecodeString(credentials)
		if err != nil {
			return "", false
		}

		// Basic auth format is "username:password" — the JWT is in the password field.
		_, password, ok := strings.Cut(string(decoded), ":")
		if !ok || password == "" {
			return "", false
		}

		return password, true

	default:
		return "", false
	}
}

func writeJSONError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
