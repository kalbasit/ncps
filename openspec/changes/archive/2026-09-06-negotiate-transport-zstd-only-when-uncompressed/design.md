## Context

See proposal.md — Why. The mechanics that shape the approach:

`upstream.Cache.GetNar(ctx, narURL, mutators...)` (`pkg/cache/upstream/cache.go`)
already receives the `nar.URL`, so `narURL.Compression` is in scope at the exact
point the `Accept-Encoding` mutator is built. There is a single production call
site, `getNarFromUpstream` in `pkg/cache/cache.go`, which passes the download URL
— the opaque/preferred URL when one exists, otherwise the NAR URL.

The value being branched on is only trustworthy because of the change archived as
`narinfo-compression-authoritative`: before it, an Attic-shaped narinfo resolved
to `none` even though its content was zstd, so gating on
`narURL.Compression == none` would have mis-fired precisely on the shape that
motivates this change. That ordering is a hard dependency, not a coincidence.

The response side (`Content-Encoding: zstd` → transparent decompress) is
deliberately untouched, so this change is confined to what ncps *asks for*.

## Goals / Non-Goals

**Goals:**

- Stop ncps from soliciting a transfer encoding it cannot safely reconcile.
- Keep the negotiation exactly where it pays: uncompressed NARs.

**Non-Goals:**

- See proposal.md — Non-goals. At the design level, additionally: no new
  configuration knob. The correct behaviour is derivable from the narinfo, so
  making it opt-in would only preserve a foot-gun.

## Decisions

**1. Gate on the resolved compression at the mutator, not at the call site.**

The rule ("only negotiate for uncompressed content") is a property of the
upstream fetch, not of any particular caller, so it belongs in `GetNar` beside
the matching decompression logic where the two can be read together.

*Alternative rejected:* have `getNarFromUpstream` pass a mutator conditionally.
That scatters the invariant across packages and would silently lapse the moment a
second caller appears.

**2. Leave the transparent `Content-Encoding: zstd` strip unconditional.**

Decompressing a declared transfer encoding is correct regardless of whether we
solicited it — refusing to would break a conforming-but-eager upstream and turn a
working fetch into a corrupt one. This change removes the case where ncps invited
the encoding; it does not try to police upstreams that send it unbidden.

*Alternative rejected:* strip only when we negotiated. That would make ncps
mishandle a legitimately transfer-encoded response and buys nothing, since we no
longer ask.

**3. Make the existing test's fake upstream honour `Accept-Encoding`.**

The skipped transport-level subtest's server sets `Content-Encoding: zstd`
unconditionally. That models an upstream that lies regardless of the request,
which no real proxy does — Caddy, nginx and CDNs all compress only when the
client advertises. Left as-is the subtest cannot pass under any correct
implementation, because it does not react to what ncps asks for.

Making the fixture realistic is what lets the subtest be un-skipped, and it
narrows what the test proves to the case this change actually fixes. The residual
case — an upstream sending an unsolicited `Content-Encoding: zstd` over a raw body
— is a protocol violation that remains unhandled by design (Decision 2), and is
no longer represented by a test.

## Risks / Trade-offs

- **An upstream that usefully double-compressed is no longer asked to.** →
  Negligible: zstd over zstd/xz yields ~0% and costs CPU on both ends. The change
  is a net win on the wire budget in every realistic case.
- **A future caller could pass a `nar.URL` whose `Compression` is not the
  resolved value**, re-opening the gap silently. → The requirement is expressed in
  terms of the *resolved* compression, and the unit test asserts the header per
  compression type at the `GetNar` boundary, so a regression fails there rather
  than surfacing as a corrupt NAR.
- **Removing the skipped subtest's unrealistic form loses coverage of the
  lying-upstream case.** → Accepted deliberately: it was never a passing test, the
  behaviour is unsupported per Decision 2, and keeping a permanently-red case has
  already proven to cost more (a stale skip pointing at a closed issue) than it
  documented.

## Migration Plan

No database migration, no configuration change, no persisted-state change. The
change alters one outbound request header. Rollback is a plain revert; nothing
written under the new behaviour is unreadable by the previous version, since the
bytes stored are the upstream's own compressed NAR either way.
