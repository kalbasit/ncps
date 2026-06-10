"""Unit tests for the bounded transient-failure retry helper.

The helper is dependency-injected (it calls a ``do_get`` thunk and an injected
``sleep``) so these tests need no ``requests``/network and run under the
pytest-only ``e2e-harness-unit`` check.
"""

from __future__ import annotations

import pytest

import http_retry


class _Resp:
    def __init__(self, code: int) -> None:
        self.status_code = code


def test_returns_first_2xx_without_retrying():
    seq = [_Resp(200), _Resp(500)]
    calls = {"n": 0}

    def do_get():
        r = seq[calls["n"]]
        calls["n"] += 1
        return r

    out = http_retry.get_with_retry(do_get, attempts=5, backoff=0, sleep=lambda _s: None)
    assert out.status_code == 200
    assert calls["n"] == 1, "stops as soon as a non-5xx response arrives"


def test_recovers_after_transient_5xx():
    seq = [_Resp(500), _Resp(503), _Resp(200)]
    calls = {"n": 0}

    def do_get():
        r = seq[calls["n"]]
        calls["n"] += 1
        return r

    out = http_retry.get_with_retry(do_get, attempts=5, backoff=0, sleep=lambda _s: None)
    assert out.status_code == 200
    assert calls["n"] == 3


def test_recovers_after_connection_error():
    calls = {"n": 0}

    def do_get():
        calls["n"] += 1
        if calls["n"] < 3:
            raise ConnectionError("connection refused")
        return _Resp(200)

    out = http_retry.get_with_retry(do_get, attempts=5, backoff=0, sleep=lambda _s: None)
    assert out.status_code == 200
    assert calls["n"] == 3


def test_persistent_5xx_returns_last_response():
    """A reproducible 5xx is NOT masked — the final 5xx response is returned so
    the caller still fails the check."""
    calls = {"n": 0}

    def do_get():
        calls["n"] += 1
        return _Resp(500)

    out = http_retry.get_with_retry(do_get, attempts=3, backoff=0, sleep=lambda _s: None)
    assert out.status_code == 500
    assert calls["n"] == 3, "all attempts used"


def test_persistent_connection_error_raises():
    def do_get():
        raise ConnectionError("down")

    with pytest.raises(ConnectionError):
        http_retry.get_with_retry(do_get, attempts=3, backoff=0, sleep=lambda _s: None)


def test_backoff_is_bounded_and_sleeps_between_attempts():
    slept = []

    def do_get():
        return _Resp(500)

    http_retry.get_with_retry(
        do_get, attempts=4, backoff=1.0, sleep=slept.append
    )
    # One sleep between each of the 4 attempts -> 3 sleeps, each capped at 10s.
    assert len(slept) == 3
    assert all(s <= 10 for s in slept)
