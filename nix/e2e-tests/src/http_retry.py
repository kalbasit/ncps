"""Bounded retry for transient HTTP failures during kubernetes validation.

Dependency-injected (no ``requests`` import) so it stays unit-testable under the
pytest-only harness check: the caller passes a ``do_get`` thunk that performs the
request and returns a response object exposing ``status_code``.

Rationale: right after a deploy ncps can be up (``/healthz`` passes) yet a
narinfo/NAR fetch transiently 5xxes during warm-up or seeding. A single no-retry
GET turns that transient blip into a whole-scenario failure (the observed
``single-local-mariadb`` HTTP 500). Retrying connection errors and 5xx absorbs
the blip; a persistent failure still surfaces (the final 5xx response is
returned, or the last connection error is raised).
"""

from __future__ import annotations

import time


def get_with_retry(do_get, *, attempts: int = 5, backoff: float = 1.0, sleep=time.sleep):
    """Call ``do_get`` until it returns a non-5xx response or attempts run out.

    Retries on a raised exception (connection-level failure) or a >= 500 status.
    Returns the first response with ``status_code < 500``; if every attempt was a
    5xx, returns the last response so the caller can still fail the check; if
    every attempt raised, re-raises the last exception. Backoff is exponential
    and capped at 10s between attempts.
    """
    last_exc: BaseException | None = None
    resp = None
    for i in range(attempts):
        try:
            resp = do_get()
        except Exception as e:  # noqa: BLE001 — any connection-level error is retryable
            last_exc = e
            resp = None
        else:
            if resp.status_code < 500:
                return resp
        if i < attempts - 1:
            sleep(min(backoff * (2**i), 10))
    if resp is not None:
        return resp
    raise last_exc  # type: ignore[misc]  # only reached when every attempt raised
