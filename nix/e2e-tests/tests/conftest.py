"""Pytest configuration for the unified e2e harness unit tests.

Puts the harness ``src/`` on ``sys.path`` so tests import the harness modules
(``cli``, ``runner``, ``catalog``) directly, and points ``CONFIG_FILE`` at the
real scenario catalog so catalog tests materialize it via ``nix eval``.
"""

from __future__ import annotations

import os
import sys

_HERE = os.path.dirname(os.path.abspath(__file__))
_SRC = os.path.join(os.path.dirname(_HERE), "src")
if _SRC not in sys.path:
    sys.path.insert(0, _SRC)

# Default the catalog path for tests that load the real config.nix.
os.environ.setdefault(
    "CONFIG_FILE", os.path.join(os.path.dirname(_HERE), "config.nix")
)
