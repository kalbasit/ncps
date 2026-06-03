## 1. Cache layer: purge sentinel never escapes as a non-404 error (TDD)

- [x] 1.1 Write a failing test in `pkg/cache` proving the bug: a narinfo present in the DB with its NAR absent from storage and no download job in flight, where the narinfo IS available upstream, must resolve to a served narinfo — and `GetNarInfo` must never return `errNarInfoPurged`.
- [x] 1.2 Write a failing test: same backing-less narinfo where the narinfo is NOT available upstream must resolve to `storage.ErrNotFound`, never `errNarInfoPurged` and never a generic error.
- [x] 1.3 Guard that only the purge sentinel is converted: `GetNarInfo`'s mapping is scoped to `errors.Is(err, ErrNarInfoPurged)` exclusively. Note: a literal "transient upstream error" cannot be observed at this layer because the upstream package already collapses upstream HTTP failures to `storage.ErrNotFound`; the narrow `errors.Is` guard structurally preserves any genuine non-sentinel error.
- [x] 1.4 In `GetNarInfo` (`pkg/cache/cache.go`, post-`prePullNarInfo` re-read), convert an `errors.Is(err, ErrNarInfoPurged)` result into `storage.ErrNotFound` before returning; leave all other errors unchanged.
- [x] 1.5 Confirm the stage-1 fallthrough is unchanged and routes a first-lookup purge into the upstream-pull path — covered by `TestGetNarInfo_Stage1PurgeThenUpstreamUnavailableResolvesToNotFound`.
- [x] 1.6 Run `task test` (race detector) for `pkg/cache` and confirm the purge-serving tests pass.

## 2. Server layer: handler maps the purge sentinel to 404 (TDD)

- [x] 2.1 Write a failing test in `pkg/server` (`TestNarInfoErrorStatus`) proving the narinfo `GET` error mapping returns `HTTP 404` for the purge sentinel (and `storage.ErrNotFound`), writes nothing on context cancellation, and `HTTP 500` only for unknown errors — without leaking the sentinel message.
- [x] 2.2 Export the sentinel (`errNarInfoPurged` → `cache.ErrNarInfoPurged`) and extract the handler's error→status decision into `narInfoErrorStatus`, mapping `cache.ErrNarInfoPurged` to `HTTP 404`; the 404 branch writes only `http.StatusText`, never the error message.
- [x] 2.3 Run `task test` for `pkg/server` and confirm `TestNarInfoErrorStatus` passes.

## 3. Verification and cleanup

- [x] 3.1 Audited all `ErrNarInfoPurged` producers/consumers: only `GetNarInfo`'s post-pull re-read returned it to a caller (now mapped); all other internal callers check `err == nil` only, and `GetNar`/`GetNarFileSize` never touch the narinfo helpers. No other 500 path.
- [x] 3.2 Ran `task fmt` (clean), `task lint` (0 issues), and `task test` (all packages pass with `-race`).
- [x] 3.3 Verified the `narinfo-purge-serving` scenarios: purge sentinel never escapes `GetNarInfo` (maps to `ErrNotFound`/404), handler maps a leaked sentinel to 404 without leaking the message, and unknown errors still yield 500.
