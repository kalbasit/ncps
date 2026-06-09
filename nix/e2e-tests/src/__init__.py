"""Unified ncps end-to-end test harness.

A scenario-driven harness that runs a declarative scenario catalog against
either a local ``dev-scripts/run.py`` deployment (``--mode local``) or a
Kind/Helm Kubernetes deployment (``--mode kubernetes``).
"""
