## Why

`nix copy --to https://<host>/upload <closure>` still aborts with *"cannot add 'X' to the binary cache because the reference 'Y' does not exist"* even after the purge fix (#1321) and even with a single replica. Root cause: the NAR **HEAD** handler reports a NAR as present based solely on the `nar_file` **DB record** (`GetNarFileSize`), while every other path determines existence from **actual bytes**. When a `nar_file` row exists without backing bytes (a phantom), nix's pre-upload `HEAD /upload/nar/<h>.nar.zst` returns `200`, so nix **skips uploading the NAR** and writes only the narinfo (nix uploads NAR-first, narinfo-last). That leaves a phantom; a dependent path's reference-verification `GET` then `404`s and Lix aborts the whole copy. This directly contradicts the existing `nar-cache-miss-recovery` rule that "the mere existence of a `nar_file` row SHALL NOT by itself make a NAR servable" — which today is only enforced on the GET path, not HEAD.

## What Changes

- The NAR `HEAD` existence/size probe (`getNar(false)` in `pkg/server`) MUST confirm the NAR is actually **servable** (whole-file or chunks present, or chunking in progress) before returning `200` — it MUST NOT return `200` from the `nar_file` record's size alone.
- A record-without-bytes NAR MUST `HEAD` `404` on `/upload` (so nix re-uploads it), and on the substituter path MUST behave consistently with `GET` (fall through to the normal recovery/serve path rather than a bare DB-size `200`).
- The check MUST honor the tri-state stat: an **ambiguous** storage error MUST NOT be turned into a false `200` nor a false `404`.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `nar-cache-miss-recovery`: add a requirement that the NAR **HEAD/existence probe** reflects actual servability (not a bare `nar_file` row), consistent with `GetNar`. Existing GET-path requirements are unchanged.

## Impact

- **Code**: `pkg/server/server.go` (`getNar(false)` HEAD branch) and a `pkg/cache` existence check reused from the GET/phantom-guard path (`statNarInStore`/`HasNarInChunks`, tri-state aware). `pkg/server`/`pkg/cache` tests. No schema, migration, route, or write-path change.
- **Behavior**: only NAR `HEAD` changes; `GET` and narinfo paths are untouched. Unblocks `nix copy --to .../upload`.
- **I/O / latency / memory**: `HEAD` gains one cheap storage `stat` (and a chunk check when applicable) instead of a DB-only lookup — a small, bounded cost on an infrequent verb; it never reads NAR bytes. No change to the hot GET/serve path. Net effect is *less* work overall: it eliminates the failed-copy retries and the phantom churn.

## Non-goals

- Write-path seed prevention (`storeInDatabase`/`PutNarInfo` creating a `nar_file` record without durable bytes) — a separate follow-up. The HEAD fix alone breaks the failure chain because nix re-uploads the NAR once HEAD reports it absent.
- Cross-replica / shared-storage consistency and replica count — confirmed irrelevant (the bug reproduces at one replica).
- Cleanup of existing phantom `nar_file` rows (operational; they self-correct as NARs are re-uploaded or recovered from upstream).
