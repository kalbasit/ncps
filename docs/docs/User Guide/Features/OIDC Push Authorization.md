# OIDC Push Authorization

## Overview

OIDC Push Authorization secures PUT and DELETE requests to your ncps cache using OpenID Connect tokens. This lets CI/CD systems like GitHub Actions and GitLab CI push to the cache using their native OIDC tokens — no stored secrets needed.

When configured, ncps validates the JWT token against the provider's published signing keys (via OIDC discovery) and optionally checks claim values against glob patterns. GET and HEAD requests are never affected.

If no OIDC policies are configured, existing behavior is preserved — PUT and DELETE are controlled solely by the `allow-put-verb` and `allow-delete-verb` flags.

## How It Works

1. **OIDC Discovery**: At startup, ncps contacts each configured issuer's `/.well-known/openid-configuration` endpoint to discover the JSON Web Key Set (JWKS) used for token verification. JWKS are cached and refreshed automatically.
1. **Token Extraction**: The middleware extracts the JWT from the `Authorization` header. Both `Bearer <token>` and `Basic <base64>` (with the JWT in the password field) are supported. Basic auth support enables tools like `nix copy` to authenticate via netrc files.
1. **Signature Verification**: The JWT is verified against each configured policy's issuer and audience until one matches.
1. **Claims Matching**: If the matching policy has `claims` configured, the token's claim values are checked against the required patterns. All claim keys must match (AND), and within a key, any pattern can match (OR).
1. **Authorization**: On success, the verified claims are stored in the request context and the request proceeds. On failure, the middleware returns 401 (invalid/missing token) or 403 (valid token but claims don't match).

## Configuration

OIDC policies are configured in the `cache.oidc` section of your configuration file. Each policy requires an `issuer` (the OIDC provider URL) and an `audience` (which must match the token's `aud` claim). An optional `claims` map restricts access based on token claim values.

### Basic Configuration

A minimal configuration with a single GitHub Actions policy:

```yaml
cache:
  allow-put-verb: true
  oidc:
    policies:
      - issuer: "https://token.actions.githubusercontent.com"
        audience: "ncps.example.com"
```

### Claims Matching

The `claims` field lets you restrict which tokens are authorized based on their claim values. Each key is a JWT claim name, and each value is a list of glob patterns.

- **Within a claim key**, patterns are ORed — any pattern matching grants access for that key.
- **Across claim keys**, conditions are ANDed — all keys must match.
- **Across policies**, the first matching policy wins.

```yaml
cache:
  allow-put-verb: true
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
```

This policy allows pushes from any repository in `myorg`, but only from the `main` branch or any tag.

### Glob Patterns

The `*` wildcard matches any sequence of characters, including `/` and the empty string. This is intentionally simpler than filesystem globbing to work naturally with claim values like `repo:org/repo:ref:refs/heads/main`.

| Pattern | Matches | Doesn't Match |
| --- | --- | --- |
| `repo:myorg/*` | `repo:myorg/foo`, `repo:myorg/foo/bar` | `repo:other/foo` |
| `refs/heads/*` | `refs/heads/main`, `refs/heads/feature/x` | `refs/tags/v1.0` |
| `*` | anything | (matches everything) |
| `repo:myorg/myrepo` | `repo:myorg/myrepo` (exact) | `repo:myorg/other` |

### Multiple Policies

You can configure multiple policies. The first policy whose issuer and audience match the token is used for verification:

```yaml
cache:
  allow-put-verb: true
  oidc:
    policies:
      - issuer: "https://token.actions.githubusercontent.com"
        audience: "ncps.example.com"
        claims:
          sub:
            - "repo:myorg/*"
      - issuer: "https://gitlab.example.com"
        audience: "ncps.internal"
```

## GitHub Actions Example

### Server Configuration

```yaml
cache:
  hostname: "ncps.example.com"
  allow-put-verb: true
  oidc:
    policies:
      - issuer: "https://token.actions.githubusercontent.com"
        audience: "ncps.example.com"
        claims:
          sub:
            - "repo:myorg/*"
```

### Workflow

The workflow requests an OIDC token with your cache hostname as the audience, writes it to a netrc file, and uses `nix copy` to push:

```yaml
name: Build and Push to Cache
on:
  push:
    branches: [main]

permissions:
  id-token: write
  contents: read

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: cachix/install-nix-action@v30

      - name: Build
        run: nix build .#default

      - name: Push to cache
        run: |
          TOKEN=$(curl -sS -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
            "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=ncps.example.com" | jq -r '.value')

          echo "machine ncps.example.com password $TOKEN" > ~/.netrc

          nix copy --to https://ncps.example.com ./result
```

The `permissions.id-token: write` setting is required for the workflow to request OIDC tokens. The `audience` parameter must match the `audience` configured in your ncps policy.

## Authentication Methods

The middleware accepts JWTs in two forms:

**Bearer token** (standard):

```
Authorization: Bearer <jwt>
```

**Basic auth** (for netrc compatibility):

```
Authorization: Basic <base64(username:jwt)>
```

The username is ignored — only the password (JWT) is validated. This makes ncps compatible with tools like `nix copy` that use netrc files for authentication, since netrc produces Basic auth headers.

### Netrc Format

```
machine ncps.example.com password <jwt>
```

Or with an explicit login:

```
machine ncps.example.com login bearer password <jwt>
```

Both formats work. The login value is ignored by ncps.

## Helm Chart Configuration

```yaml
config:
  permissions:
    allowPut: true
  oidc:
    policies:
      - issuer: "https://token.actions.githubusercontent.com"
        audience: "ncps.example.com"
        claims:
          sub:
            - "repo:myorg/*"
```

See <a class="reference-link" href="../Installation/Helm%20Chart.md">Helm Chart</a> for full chart documentation.

## HTTP Response Codes

| Status | Meaning |
| --- | --- |
| 401 | Missing or invalid token (expired, wrong signature, wrong audience) |
| 403 | Valid token but claims don't match any allowed pattern |
| 405 | PUT/DELETE not enabled (`allow-put-verb` / `allow-delete-verb` is false) |

## Troubleshooting

**401 "missing authorization header"**

The request has no `Authorization` header. Ensure your client is sending the token. For `nix copy`, verify the netrc file is in the right location and has the correct machine name.

**401 "token validation failed"**

The JWT couldn't be verified by any configured policy. Common causes:

- The token's audience doesn't match the policy's `audience`
- The token's issuer doesn't match the policy's `issuer`
- The token has expired
- The issuer's JWKS endpoint is unreachable

**403 "claim mismatch"**

The token was verified successfully, but its claims don't match the required patterns. Check the `claims` section of your policy and the actual values in your token. You can decode a JWT at [jwt.io](https://jwt.io) to inspect its claims.

**Startup error: "initializing OIDC policy"**

ncps couldn't reach the issuer's OIDC discovery endpoint at startup. Verify the issuer URL is correct and reachable from the server.

## Related Documentation

- <a class="reference-link" href="../Configuration/Reference.md">Configuration Reference</a>
- <a class="reference-link" href="../Installation/Helm%20Chart.md">Helm Chart</a>
- <a class="reference-link" href="../Deployment/Single%20Instance.md">Single Instance Deployment</a>
- <a class="reference-link" href="../Deployment/High%20Availability.md">High Availability Setup</a>
