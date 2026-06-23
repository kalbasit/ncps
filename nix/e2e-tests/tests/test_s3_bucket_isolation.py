"""Unit tests for per-scenario S3 bucket isolation in the kubernetes harness.

Regression guard for the e2e-nightly CDC failure: scenarios used to share one
Garage bucket, so residual whole-file NARs from an earlier non-CDC scenario made
later CDC scenarios skip chunking (0 chunks). Each scenario must get its own
bucket, mirroring the existing per-scenario database isolation.

These are fast, pure-python unit tests (no nix eval, no cluster).
"""

from __future__ import annotations

# harness_config is stdlib-only, so this runs in the pytest-only unit net
# (which has neither requests nor pyyaml, so k8s_tests cannot be imported here).
from harness_config import scenario_bucket_name


def test_scenario_bucket_name_is_per_scenario():
    assert scenario_bucket_name("single-s3-postgres-cdc") == "ncps-single-s3-postgres-cdc"
    # Distinct scenarios get distinct buckets (the core isolation property).
    assert scenario_bucket_name("single-s3-postgres") != scenario_bucket_name(
        "single-s3-postgres-cdc"
    )


def test_scenario_bucket_names_are_valid_s3_names():
    # S3 bucket names: lowercase, 3-63 chars, hyphens ok, no underscores.
    for name in (
        "single-s3-postgres-cdc",
        "ha-s3-postgres-cdc",
        "ha-s3-postgres-cdc-lifecycle",
    ):
        bucket = scenario_bucket_name(name)
        assert bucket.islower()
        assert "_" not in bucket
        assert 3 <= len(bucket) <= 63
