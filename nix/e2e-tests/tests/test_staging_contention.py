"""Unit tests for the staging-contention chunking-window race helpers.

Under eager CDC a cross-pod reader of the uncompressed `.nar` is served
progressively from the holder's chunks (the designed #1289 path), not from
in-flight staging. The chunking-window driver races readers while the eager-CDC
download+chunk is in flight (best-effort gate via :func:`_await_inflight`) and
then asserts byte-correctness plus "chunked by exactly one replica". These tests
pin the in-flight classification and URL-hash helpers without a live cluster.
"""

from __future__ import annotations

from phases import staging_contention as sc


class _ScriptedDB:
    """A fake DBAccess whose query() replays a scripted sequence of rows."""

    def __init__(self, sequence):
        # sequence: list of return-values for successive query() calls.
        self._seq = list(sequence)
        self.calls = 0

    def query(self, sql, params=()):  # noqa: D401 - test stub
        self.calls += 1
        if self._seq:
            return self._seq.pop(0)
        # Repeat the last value once exhausted.
        return []


# -- _inflight_state -----------------------------------------------------------


def test_inflight_state_absent_when_no_row():
    db = _ScriptedDB([[]])
    assert sc._inflight_state(db, "h") == "absent"


def test_inflight_state_inflight_when_total_chunks_zero():
    db = _ScriptedDB([[(0,)]])
    assert sc._inflight_state(db, "h") == "inflight"


def test_inflight_state_done_when_total_chunks_positive():
    db = _ScriptedDB([[(3810,)]])
    assert sc._inflight_state(db, "h") == "done"


# -- _await_inflight -----------------------------------------------------------


def test_await_inflight_races_on_absent_download_phase():
    # No chunk row yet = download in progress: race now to catch the long
    # download phase, do not wait for the brief post-download chunk phase.
    db = _ScriptedDB([[]])
    assert sc._await_inflight(db, "h") == "inflight"


def test_await_inflight_races_on_actively_chunking():
    db = _ScriptedDB([[(0,)]])
    assert sc._await_inflight(db, "h") == "inflight"


def test_await_inflight_returns_missed_when_already_chunked():
    db = _ScriptedDB([[(3810,)]])
    assert sc._await_inflight(db, "h") == "missed"


# -- _hash_from_nar_url --------------------------------------------------------


def test_hash_from_nar_url_uncompressed():
    assert sc._hash_from_nar_url("nar/deadbeef.nar") == "deadbeef"


def test_hash_from_nar_url_compressed():
    assert sc._hash_from_nar_url("nar/deadbeef.nar.xz") == "deadbeef"


def test_hash_from_nar_url_leading_slash():
    assert sc._hash_from_nar_url("/nar/deadbeef.nar") == "deadbeef"
