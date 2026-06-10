#!/usr/bin/env python3
"""
NCPS Deployment Test Script

Comprehensive testing script for NCPS Kubernetes deployments.
Tests pods, services, database connectivity, storage, and end-to-end functionality.
"""

import argparse
import json
import os
import re
import subprocess
import sys
import time
from dataclasses import dataclass
from typing import Dict, List, Optional

import requests
import yaml

try:
    import psycopg2
    import pymysql
    from kubernetes import client
    from kubernetes import config as k8s_config
except ImportError as e:
    print(f"❌ Missing required dependency: {e}")
    print("\nPlease install required dependencies:")
    print("  pip3 install pyyaml requests psycopg2-binary pymysql kubernetes boto3")
    sys.exit(1)

try:
    import boto3
except ImportError:
    boto3 = None

from http_retry import get_with_retry

HTTP_TIMEOUT = 60


@dataclass
class TestResult:
    """Test result for a single check"""

    name: str
    passed: bool
    message: str
    details: Optional[str] = None


@dataclass
class DeploymentTestResult:
    """Test results for an entire deployment"""

    deployment_name: str
    results: List[TestResult]

    @property
    def passed(self) -> bool:
        return all(r.passed for r in self.results)

    @property
    def failed_count(self) -> int:
        return sum(1 for r in self.results if not r.passed)

    @property
    def passed_count(self) -> int:
        return sum(1 for r in self.results if r.passed)


class NCPSTester:
    """Main test orchestrator for NCPS deployments"""

    def __init__(self, config_path: str, verbose: bool = False):
        self.verbose = verbose
        self.config = self._load_config(config_path)
        self.k8s_core_v1 = None
        self.k8s_apps_v1 = None
        self._init_kubernetes()

    def _load_config(self, path: str) -> dict:
        """Load test configuration from YAML file"""
        with open(path, "r") as f:
            return yaml.safe_load(f)

    def _init_kubernetes(self):
        """Initialize Kubernetes client"""
        try:
            k8s_config.load_kube_config()
            self.k8s_core_v1 = client.CoreV1Api()
            self.k8s_apps_v1 = client.AppsV1Api()
            self.k8s_batch_v1 = client.BatchV1Api()
        except Exception as e:
            print(f"❌ Failed to initialize Kubernetes client: {e}")
            sys.exit(1)

    def log(self, msg: str, verbose_only: bool = False):
        """Log message"""
        if not verbose_only or self.verbose:
            print(msg)

    def test_all_deployments(
        self, deployment_filter: Optional[str] = None
    ) -> Dict[str, DeploymentTestResult]:
        """Test all deployments or a specific one"""
        results = {}
        deployments = self.config["deployments"]

        if deployment_filter:
            deployments = [d for d in deployments if d["name"] == deployment_filter]
            if not deployments:
                print(f"❌ Deployment '{deployment_filter}' not found in configuration")
                return results

        for deployment_config in deployments:
            name = deployment_config["name"]
            print(f"\n{'=' * 80}")
            print(f"Testing: {name}")
            print(f"{'=' * 80}\n")

            result = self.test_deployment(deployment_config)
            results[name] = result

            # Print summary
            if result.passed:
                print(f"\n✅ {name}: All tests passed ({result.passed_count} checks)")
            else:
                print(
                    f"\n❌ {name}: {result.failed_count} failed, {result.passed_count} passed"
                )

        return results

    def test_deployment(self, deployment_config: dict) -> DeploymentTestResult:
        """Test a single deployment"""
        results = []
        name = deployment_config["name"]
        namespace = deployment_config["namespace"]

        # 0. Wait for migration job (if mode=job)
        print("🔄 Checking migration job...")
        migration_result = self._wait_for_migration_job(deployment_config)
        results.append(migration_result)
        self._print_test_result(migration_result)
        if not migration_result.passed:
            # If migration failed, skip remaining tests
            print("   ⚠️  Skipping remaining tests (migration failed)")
            return DeploymentTestResult(name, results)

        # 1. Check pods are running
        print("🔍 Checking pods...")
        pod_result = self._test_pods(deployment_config)
        results.append(pod_result)
        self._print_test_result(pod_result)
        if not pod_result.passed:
            # If pods aren't running, skip remaining tests
            print("   ⚠️  Skipping remaining tests (pods not ready)")
            return DeploymentTestResult(name, results)

        # 2. Test HTTP endpoints
        print("🌐 Testing HTTP endpoints...")
        http_result = self._test_http_endpoints(deployment_config)
        results.append(http_result)
        self._print_test_result(http_result)

        # 3. Test database
        print("🗄️  Testing database...")
        db_result = self._test_database(deployment_config)
        results.append(db_result)
        self._print_test_result(db_result)

        # 4. Test storage
        print("💾 Testing storage...")
        storage_result = self._test_storage(deployment_config)
        results.append(storage_result)
        self._print_test_result(storage_result)

        # 5. CDC lifecycle (only for permutations with the cdc-lifecycle marker)
        if deployment_config.get("cdc_lifecycle"):
            print("🔁 Testing CDC lifecycle (non-CDC->CDC->drain->non-CDC)...")
            lifecycle_result = self._test_cdc_lifecycle(deployment_config)
            results.append(lifecycle_result)
            self._print_test_result(lifecycle_result)

        # 6. Multi-replica topology assertions (cross-replica presence,
        #    storage-lag / no phantom HEAD). Not gated on cdc-lifecycle:
        #    _test_cdc_topology self-skips single-replica / no-test-data
        #    deployments, so every multi-replica deployment exercises these
        #    phantom/cross-replica checks.
        print("🌐 Testing multi-replica topology...")
        topology_result = self._test_cdc_topology(deployment_config)
        results.append(topology_result)
        self._print_test_result(topology_result)

        return DeploymentTestResult(name, results)

    def _print_test_result(self, result: TestResult):
        """Print a single test result"""
        if result.passed:
            print(f"   ✅ {result.name}: {result.message}")
        else:
            print(f"   ❌ {result.name}: {result.message}")
            if result.details:
                for line in result.details.split("\n"):
                    print(f"      {line}")

        if self.verbose and result.passed:
            # In verbose mode, show details even for passing tests
            if result.details:
                for line in result.details.split("\n"):
                    print(f"      {line}")

    def _test_pods(self, deployment_config: dict) -> TestResult:
        """Test that pods are running"""
        namespace = deployment_config["namespace"]
        expected_replicas = deployment_config["replicas"]
        mode = deployment_config["mode"]

        try:
            # Wait for pods to be created (up to 120 seconds)
            max_wait = 120
            wait_interval = 5
            elapsed = 0

            while elapsed < max_wait:
                pods = self.k8s_core_v1.list_namespaced_pod(
                    namespace=namespace, label_selector="app.kubernetes.io/name=ncps"
                )

                if len(pods.items) >= expected_replicas:
                    break

                if self.verbose:
                    print(
                        f"      Waiting for pods... ({len(pods.items)}/{expected_replicas} created)"
                    )
                time.sleep(wait_interval)
                elapsed += wait_interval
            else:
                return TestResult(
                    "Pods",
                    False,
                    f"Only {len(pods.items)}/{expected_replicas} pods created after {max_wait}s",
                )

            # Wait for all pods to be Running (up to 180 seconds)
            max_wait = 180
            elapsed = 0

            while elapsed < max_wait:
                pods = self.k8s_core_v1.list_namespaced_pod(
                    namespace=namespace, label_selector="app.kubernetes.io/name=ncps"
                )

                running_pods = [p for p in pods.items if p.status.phase == "Running"]

                if len(running_pods) == expected_replicas:
                    # Verify containers are ready
                    all_ready = all(
                        all(cs.ready for cs in pod.status.container_statuses or [])
                        for pod in running_pods
                    )

                    if all_ready:
                        return TestResult(
                            "Pods",
                            True,
                            f"{expected_replicas}/{expected_replicas} pods running and ready",
                        )

                if self.verbose:
                    print(
                        f"      Waiting for pods to be ready... ({len(running_pods)}/{expected_replicas} running)"
                    )
                time.sleep(wait_interval)
                elapsed += wait_interval

            # Diagnose failures
            failed_pods = [p for p in pods.items if p.status.phase != "Running"]
            error_details = []

            for pod in failed_pods:
                pod_name = pod.metadata.name
                phase = pod.status.phase
                error_details.append(f"  - {pod_name}: {phase}")

                # Check container statuses
                if pod.status.container_statuses:
                    for cs in pod.status.container_statuses:
                        if cs.state.waiting:
                            error_details.append(
                                f"    Container {cs.name}: Waiting - {cs.state.waiting.reason}"
                            )
                        elif cs.state.terminated:
                            error_details.append(
                                f"    Container {cs.name}: Terminated - {cs.state.terminated.reason}"
                            )

            return TestResult(
                "Pods",
                False,
                f"Pods not ready after {max_wait}s",
                details="\n".join(error_details),
            )

        except Exception as e:
            return TestResult("Pods", False, f"Error checking pods: {e}")

    def _wait_for_migration_job(self, deployment_config: dict) -> TestResult:
        """Wait for migration job to complete (if using job mode)"""
        namespace = deployment_config["namespace"]
        migration_mode = deployment_config.get("migration", {}).get("mode")

        # Only wait for jobs in "job" mode (not "initContainer" or "argocd")
        if migration_mode != "job":
            return TestResult(
                "Migration Job",
                True,
                f"Migration mode is '{migration_mode}', no job to wait for",
            )

        # Job name follows pattern: {deployment-name}-migration (from _helpers.tpl line 5)
        # Deployment name format: ncps-{permutation-name}
        job_name = f"{deployment_config['service_name']}-migration"

        self.log(
            f"   Waiting for migration job '{job_name}' to complete...",
            verbose_only=True,
        )

        try:
            max_wait = 120  # 2 minutes for migration to complete
            wait_interval = 5
            elapsed = 0
            job_not_found_count = 0

            while elapsed < max_wait:
                try:
                    job = self.k8s_batch_v1.read_namespaced_job(
                        name=job_name, namespace=namespace
                    )

                    # Check if job succeeded
                    if job.status.succeeded and job.status.succeeded >= 1:
                        return TestResult(
                            "Migration Job",
                            True,
                            "Migration job completed successfully",
                        )

                    # Check if job failed
                    if job.status.failed and job.status.failed > 0:
                        # Fetch pod logs for diagnostics
                        error_details = self._get_migration_job_logs(
                            namespace, job_name
                        )
                        return TestResult(
                            "Migration Job",
                            False,
                            f"Migration job failed after {job.status.failed} attempts",
                            details=error_details,
                        )

                    # Job is still running
                    if self.verbose:
                        active = job.status.active or 0
                        print(
                            f"      Migration job running... ({active} active pods, {elapsed}s elapsed)"
                        )

                except client.exceptions.ApiException as e:
                    if e.status == 404:
                        job_not_found_count += 1

                        # If job not found after initial wait, check if it already completed and was cleaned up
                        if (
                            job_not_found_count > 2
                        ):  # After 10 seconds of not finding it
                            # Check events to see if job completed in the past
                            if self._check_migration_job_completed_previously(
                                namespace, job_name
                            ):
                                return TestResult(
                                    "Migration Job",
                                    True,
                                    "Migration job already completed (cleaned up by Kubernetes)",
                                )

                        # Job not found yet (Helm hook may still be creating it)
                        if self.verbose:
                            print(
                                f"      Migration job not found yet, waiting... ({elapsed}s elapsed)"
                            )
                    else:
                        raise

                time.sleep(wait_interval)
                elapsed += wait_interval

            # Timeout - provide diagnostic info
            error_details = self._get_migration_job_diagnostics(namespace, job_name)
            return TestResult(
                "Migration Job",
                False,
                f"Migration job did not complete within {max_wait}s",
                details=error_details,
            )

        except Exception as e:
            return TestResult(
                "Migration Job", False, f"Error checking migration job: {e}"
            )

    def _check_migration_job_completed_previously(
        self, namespace: str, job_name: str
    ) -> bool:
        """Check if migration job completed in the past by examining events"""
        try:
            events = self.k8s_core_v1.list_namespaced_event(
                namespace=namespace,
                field_selector=f"involvedObject.name={job_name}",
            )

            # Look for completion events
            for event in events.items:
                if event.reason == "Completed" and "completed" in event.message.lower():
                    return True

            return False
        except Exception:
            return False

    def _get_migration_job_logs(self, namespace: str, job_name: str) -> str:
        """Fetch logs from migration job pods for diagnostics"""
        try:
            # Find pods created by this job
            pods = self.k8s_core_v1.list_namespaced_pod(
                namespace=namespace, label_selector=f"job-name={job_name}"
            )

            if not pods.items:
                return "No pods found for migration job"

            # Get logs from the most recent pod
            pod = pods.items[-1]  # Last pod (most recent attempt)
            pod_name = pod.metadata.name

            try:
                logs = self.k8s_core_v1.read_namespaced_pod_log(
                    name=pod_name, namespace=namespace, container="migration"
                )
                return f"Migration pod logs ({pod_name}):\n{logs}"
            except Exception as e:
                return f"Failed to fetch logs from pod {pod_name}: {e}"

        except Exception as e:
            return f"Error fetching migration job logs: {e}"

    def _get_migration_job_diagnostics(self, namespace: str, job_name: str) -> str:
        """Get diagnostic information about migration job"""
        details = []

        try:
            # Try to get job status
            try:
                job = self.k8s_batch_v1.read_namespaced_job(
                    name=job_name, namespace=namespace
                )
                details.append("Job status:")
                details.append(f"  Active: {job.status.active or 0}")
                details.append(f"  Succeeded: {job.status.succeeded or 0}")
                details.append(f"  Failed: {job.status.failed or 0}")

                # Get conditions
                if job.status.conditions:
                    details.append("  Conditions:")
                    for cond in job.status.conditions:
                        details.append(
                            f"    - {cond.type}: {cond.status} ({cond.reason})"
                        )
            except client.exceptions.ApiException as e:
                if e.status == 404:
                    details.append(
                        "Job not found - may not have been created by Helm hook"
                    )
                else:
                    details.append(f"Failed to get job status: {e}")

            # Get recent events
            try:
                events = self.k8s_core_v1.list_namespaced_event(
                    namespace=namespace,
                    field_selector=f"involvedObject.name={job_name}",
                )
                if events.items:
                    details.append("\nRecent events:")
                    for event in sorted(
                        events.items, key=lambda e: e.last_timestamp or e.event_time
                    )[-5:]:
                        details.append(f"  - {event.reason}: {event.message}")
            except Exception:
                pass

            # Get pod logs
            pod_logs = self._get_migration_job_logs(namespace, job_name)
            if pod_logs and "No pods found" not in pod_logs:
                details.append(f"\n{pod_logs}")

        except Exception as e:
            details.append(f"Error gathering diagnostics: {e}")

        return "\n".join(details)

    def _test_http_endpoints(self, deployment_config: dict) -> TestResult:
        """Test HTTP endpoints via port-forward"""
        namespace = deployment_config["namespace"]
        service_name = deployment_config.get("service_name", deployment_config["name"])

        # Start port-forward
        port_forward = None
        try:
            local_port = self._find_free_port()
            port_forward = subprocess.Popen(
                [
                    "kubectl",
                    "port-forward",
                    f"svc/{service_name}",
                    f"{local_port}:8501",
                    "-n",
                    namespace,
                ],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )

            # Wait for port-forward to be ready
            time.sleep(3)

            base_url = f"http://localhost:{local_port}"

            # Test healthz endpoint
            try:
                resp = requests.get(f"{base_url}/healthz", timeout=10)
                if resp.status_code != 200:
                    return TestResult(
                        "HTTP Endpoints",
                        False,
                        f"Healthz check failed: HTTP {resp.status_code}",
                    )
            except Exception as e:
                return TestResult(
                    "HTTP Endpoints", False, f"Failed to connect to service: {e}"
                )

            # Test narinfo fetching
            test_hashes = self.config.get("test_data", {}).get("narinfo_hashes", [])
            if not test_hashes:
                return TestResult(
                    "HTTP Endpoints",
                    True,
                    "Healthz passed (no test data configured)",
                )

            # Test first 3 hashes
            for narinfo_hash in test_hashes[:3]:
                try:
                    # Fetch narinfo. Retry transient post-deploy 5xx/connection
                    # errors: ncps can be up (healthz ok) yet briefly 5xx a
                    # narinfo during warm-up/seeding.
                    resp = get_with_retry(
                        lambda h=narinfo_hash: requests.get(
                            f"{base_url}/{h}.narinfo", timeout=HTTP_TIMEOUT
                        )
                    )
                    if resp.status_code != 200:
                        return TestResult(
                            "HTTP Endpoints",
                            False,
                            f"Failed to fetch narinfo {narinfo_hash}: HTTP {resp.status_code}",
                        )
                    if self.verbose:
                        print(
                            f"      ✓ Fetched {narinfo_hash}.narinfo ({len(resp.text)} bytes)"
                        )

                    # Parse URL from narinfo
                    narinfo_text = resp.text
                    url_match = re.search(r"^URL:\s*(.+)$", narinfo_text, re.MULTILINE)
                    if not url_match:
                        return TestResult(
                            "HTTP Endpoints",
                            False,
                            f"No URL found in narinfo {narinfo_hash}",
                        )

                    nar_url = url_match.group(1).strip()

                    # Fetch the NAR file (same transient-error tolerance).
                    if self.verbose:
                        print(f"      ✓ Fetching {nar_url}")
                    resp = get_with_retry(
                        lambda u=nar_url: requests.get(f"{base_url}/{u}", timeout=HTTP_TIMEOUT)
                    )
                    if resp.status_code != 200:
                        return TestResult(
                            "HTTP Endpoints",
                            False,
                            f"Failed to fetch NAR {nar_url}: HTTP {resp.status_code}",
                        )
                    if self.verbose:
                        print(f"      ✓ Fetched {nar_url} ({len(resp.content)} bytes)")

                    if len(resp.content) == 0:
                        return TestResult(
                            "HTTP Endpoints",
                            False,
                            f"NAR {nar_url} returned empty content",
                        )

                    if self.verbose:
                        print(
                            f"      ✓ Fetched {narinfo_hash}.narinfo and {nar_url} ({len(resp.content)} bytes)"
                        )

                except Exception as e:
                    return TestResult(
                        "HTTP Endpoints",
                        False,
                        f"Error testing narinfo {narinfo_hash}: {e}",
                    )

            return TestResult(
                "HTTP Endpoints",
                True,
                f"Healthz and narinfo endpoints working (tested {min(3, len(test_hashes))} hashes)",
            )

        finally:
            if port_forward:
                port_forward.terminate()
                port_forward.wait(timeout=5)

    def _test_database(self, deployment_config: dict) -> TestResult:
        """Test database connectivity and data"""
        db_type = deployment_config["database"]["type"]
        namespace = deployment_config["namespace"]

        if db_type == "sqlite":
            return self._test_sqlite_database(deployment_config)
        elif db_type == "postgresql":
            return self._test_postgresql_database(deployment_config)
        elif db_type == "mysql":
            return self._test_mysql_database(deployment_config)
        else:
            return TestResult("Database", False, f"Unknown database type: {db_type}")

    def _test_sqlite_database(self, deployment_config: dict) -> TestResult:
        """Test SQLite database"""
        namespace = deployment_config["namespace"]
        db_path = deployment_config["database"]["path"]

        try:
            # Get first pod
            pods = self.k8s_core_v1.list_namespaced_pod(
                namespace=namespace,
                label_selector="app.kubernetes.io/name=ncps",
                limit=1,
            )

            if not pods.items:
                return TestResult("Database", False, "No pods found")

            pod_name = pods.items[0].metadata.name

            # When using debug with --target, access target's filesystem via /proc/1/root
            target_db_path = f"/proc/1/root{db_path}"

            # Snapshot the live DB with SQLite's online backup, which yields a
            # WAL-consistent copy. The previous approach copied ncps.db,
            # ncps.db-wal and ncps.db-shm as three separate files; a checkpoint
            # landing between the copies (main file copied pre-checkpoint without
            # the table, WAL then truncated) produced a snapshot missing freshly
            # written tables — a flaky "nar_files not found" even though the live
            # DB clearly has it (the HTTP narinfo probe, which reads nar_files,
            # passes). `.backup` coordinates with WAL and concurrent writers.
            # nouchka/sqlite3 ships sqlite3; no --profile=restricted because it
            # sets runAsNonRoot, which the kubelet rejects for that root image
            # (the debug container never starts and the probe times out).
            def _sqlite(query_arg: str) -> subprocess.CompletedProcess:
                return subprocess.run(
                    [
                        "kubectl",
                        "debug",
                        "-n",
                        namespace,
                        pod_name,
                        "--target=ncps",
                        "--image=nouchka/sqlite3:latest",
                        "-it=false",
                        "--quiet",
                        "--",
                        "sh",
                        "-c",
                        f"sqlite3 {target_db_path} \".backup '/tmp/test.db'\" "
                        f"&& sqlite3 /tmp/test.db {query_arg}",
                    ],
                    capture_output=True,
                    text=True,
                    timeout=60,
                )

            # Retry the table check: a snapshot taken in the brief window before
            # a migration/write commits can miss the table. The data is present
            # once serving has begun, so a few attempts converge.
            last = None
            tables_output = ""
            for _attempt in range(5):
                last = _sqlite("'.tables'")
                if last.returncode == 0 and "nar_files" in last.stdout:
                    tables_output = last.stdout.strip()
                    break
                time.sleep(3)
            else:
                if last is not None and last.returncode != 0:
                    return TestResult(
                        "Database",
                        False,
                        f"Failed to access SQLite database at {db_path}",
                        details=f"Return code: {last.returncode}\nstderr: {last.stderr}\nstdout: {last.stdout}",
                    )
                return TestResult(
                    "Database",
                    False,
                    "Expected 'nar_files' table not found in SQLite database",
                    details=f"Tables found: '{(last.stdout if last else '').strip()}'",
                )

            # Count rows
            result = _sqlite("'SELECT COUNT(*) FROM nar_files;'")

            if result.returncode != 0:
                return TestResult(
                    "Database",
                    False,
                    f"Failed to count rows in SQLite database",
                    details=f"stderr: {result.stderr}\nstdout: {result.stdout}",
                )

            row_count = int(result.stdout.strip())

            if row_count == 0:
                return TestResult(
                    "Database",
                    False,
                    f"SQLite database is empty (0 NAR entries)",
                    details=f"stdout: {result.stdout}",
                )

            return TestResult(
                "Database",
                True,
                f"SQLite database accessible ({row_count} NAR entries)",
            )

        except subprocess.TimeoutExpired:
            return TestResult(
                "Database", False, "Timeout while testing SQLite database"
            )
        except Exception as e:
            return TestResult("Database", False, f"Error testing SQLite: {e}")

    def _test_postgresql_database(self, deployment_config: dict) -> TestResult:
        """Test PostgreSQL database via port-forward"""
        pg_config = self.config["cluster"]["postgresql"]

        # Port-forward to PostgreSQL service
        port_forward = None
        try:
            local_port = self._find_free_port()

            # Extract service name and namespace from FQDN
            # e.g., "pg17-ncps-rw.data.svc.cluster.local" -> service="pg17-ncps-rw", namespace="data"
            host_parts = pg_config["host"].split(".")
            service_name = host_parts[0]
            namespace = host_parts[1] if len(host_parts) > 1 else "data"

            # Port-forward to the PostgreSQL service
            port_forward = subprocess.Popen(
                [
                    "kubectl",
                    "port-forward",
                    f"svc/{service_name}",
                    f"{local_port}:{pg_config['port']}",
                    "-n",
                    namespace,
                ],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )

            # Wait for port-forward to be ready
            time.sleep(3)

            # Use per-test database name (e.g., ncps_single_s3_postgres)
            db_name = f"ncps_{deployment_config['name'].replace('-', '_')}"

            conn = psycopg2.connect(
                host="localhost",
                port=local_port,
                database=db_name,
                user=pg_config["username"],
                password=pg_config["password"],
            )

            cursor = conn.cursor()

            # Check tables
            cursor.execute(
                "SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'"
            )
            tables = [row[0] for row in cursor.fetchall()]

            cdc_enabled = deployment_config.get("cdc", False)
            target_table = "chunks" if cdc_enabled else "nar_files"

            if target_table not in tables:
                conn.close()
                return TestResult(
                    "Database",
                    False,
                    f"Expected table '{target_table}' not found",
                    details=f"Found tables: {tables}",
                )

            # Count rows
            # For CDC deployments, background downloads happen asynchronously,
            # so we need to retry with exponential backoff to wait for chunks to be created
            max_retries = 10 if cdc_enabled else 1
            retry_delay = 1  # Start with 1 second
            count = 0

            for attempt in range(max_retries):
                cursor.execute(f"SELECT COUNT(*) FROM {target_table}")
                count = cursor.fetchone()[0]

                if count > 0:
                    break

                if attempt < max_retries - 1:
                    if cdc_enabled:
                        self.log(
                            f"   ⏳ Waiting for background downloads (attempt {attempt + 1}/{max_retries}, {count} chunks so far)...",
                            verbose_only=True,
                        )
                    time.sleep(retry_delay)
                    retry_delay = min(
                        retry_delay * 1.5, 10
                    )  # Exponential backoff, max 10s

            conn.close()

            entry_type = "chunks" if cdc_enabled else "NAR entries"

            if count == 0:
                conn.close()
                return TestResult(
                    "Database",
                    False,
                    f"PostgreSQL database is empty (0 {entry_type})",
                )

            return TestResult(
                "Database",
                True,
                f"PostgreSQL database accessible ({count} {entry_type})",
            )

        except Exception as e:
            return TestResult("Database", False, f"Error connecting to PostgreSQL: {e}")
        finally:
            if port_forward:
                port_forward.terminate()
                port_forward.wait(timeout=5)

    def _test_mysql_database(self, deployment_config: dict) -> TestResult:
        """Test MySQL/MariaDB database via port-forward"""
        mysql_config = self.config["cluster"]["mariadb"]

        # Port-forward to MariaDB service
        port_forward = None
        try:
            local_port = self._find_free_port()

            # Extract service name and namespace from FQDN
            # e.g., "mariadb-ncps.data.svc.cluster.local" -> service="mariadb-ncps", namespace="data"
            host_parts = mysql_config["host"].split(".")
            service_name = host_parts[0]
            namespace = host_parts[1] if len(host_parts) > 1 else "data"

            # Port-forward to the MariaDB service
            port_forward = subprocess.Popen(
                [
                    "kubectl",
                    "port-forward",
                    f"svc/{service_name}",
                    f"{local_port}:{mysql_config['port']}",
                    "-n",
                    namespace,
                ],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )

            # Wait for port-forward to be ready
            time.sleep(3)

            # Use per-test database name (e.g., ncps_single_s3_mariadb)
            db_name = f"ncps_{deployment_config['name'].replace('-', '_')}"

            conn = pymysql.connect(
                host="localhost",
                port=local_port,
                database=db_name,
                user=mysql_config["username"],
                password=mysql_config["password"],
            )

            cursor = conn.cursor()

            # Check tables
            cursor.execute("SHOW TABLES")
            tables = [row[0] for row in cursor.fetchall()]

            cdc_enabled = deployment_config.get("cdc", False)
            target_table = "chunks" if cdc_enabled else "nar_files"

            if target_table not in tables:
                conn.close()
                return TestResult(
                    "Database",
                    False,
                    f"Expected table '{target_table}' not found",
                    details=f"Found tables: {tables}",
                )

            # Count rows
            cursor.execute(f"SELECT COUNT(*) FROM {target_table}")
            count = cursor.fetchone()[0]

            conn.close()

            entry_type = "chunks" if cdc_enabled else "NAR entries"

            if count == 0:
                conn.close()
                return TestResult(
                    "Database",
                    False,
                    f"MySQL database is empty (0 {entry_type})",
                )

            return TestResult(
                "Database", True, f"MySQL database accessible ({count} {entry_type})"
            )

        except Exception as e:
            return TestResult("Database", False, f"Error connecting to MySQL: {e}")
        finally:
            if port_forward:
                port_forward.terminate()
                port_forward.wait(timeout=5)

    def _test_storage(self, deployment_config: dict) -> TestResult:
        """Test storage (local or S3)"""
        storage_type = deployment_config["storage"]["type"]

        if storage_type == "local":
            return self._test_local_storage(deployment_config)
        elif storage_type == "s3":
            return self._test_s3_storage(deployment_config)
        else:
            return TestResult("Storage", False, f"Unknown storage type: {storage_type}")

    def _test_local_storage(self, deployment_config: dict) -> TestResult:
        """Test local filesystem storage"""
        namespace = deployment_config["namespace"]
        storage_path = deployment_config["storage"]["path"]

        try:
            # Get first pod
            pods = self.k8s_core_v1.list_namespaced_pod(
                namespace=namespace,
                label_selector="app.kubernetes.io/name=ncps",
                limit=1,
            )

            if not pods.items:
                return TestResult("Storage", False, "No pods found")

            pod_name = pods.items[0].metadata.name

            # When using debug with --target, access target's filesystem via /proc/1/root
            target_storage_path = f"/proc/1/root{storage_path}"

            # Check storage directory structure using debug container
            for subdir in ["store/nar", "store/narinfo"]:
                result = subprocess.run(
                    [
                        "kubectl",
                        "debug",
                        "-n",
                        namespace,
                        pod_name,
                        "--target=ncps",
                        "--image=busybox:latest",
                        "-it=false",
                        "--quiet",
                        "--",
                        "ls",
                        "-la",
                        f"{target_storage_path}/{subdir}",
                    ],
                    capture_output=True,
                    text=True,
                    timeout=30,
                )

                if result.returncode != 0:
                    return TestResult(
                        "Storage",
                        False,
                        f"Storage subdirectory {subdir} not found at {storage_path}",
                        details=f"stderr: {result.stderr}\nstdout: {result.stdout}",
                    )

            # Count NAR files
            result = subprocess.run(
                [
                    "kubectl",
                    "debug",
                    "-n",
                    namespace,
                    pod_name,
                    "--target=ncps",
                    "--image=busybox:latest",
                    "-it=false",
                    "--quiet",
                    "--",
                    "sh",
                    "-c",
                    f"find {target_storage_path}/store/nar -type f 2>/dev/null | wc -l",
                ],
                capture_output=True,
                text=True,
                timeout=30,
            )

            if result.returncode != 0:
                return TestResult(
                    "Storage",
                    False,
                    f"Failed to count files in {storage_path}/nar",
                    details=f"stderr: {result.stderr}",
                )

            file_count = int(result.stdout.strip())

            if file_count == 0:
                return TestResult(
                    "Storage",
                    False,
                    f"Local storage is empty (0 NAR files in {storage_path})",
                )

            return TestResult(
                "Storage",
                True,
                f"Local storage accessible ({file_count} NAR files in {storage_path})",
            )

        except subprocess.TimeoutExpired:
            return TestResult("Storage", False, "Timeout while testing local storage")
        except Exception as e:
            return TestResult("Storage", False, f"Error testing local storage: {e}")

    def _test_s3_storage(self, deployment_config: dict) -> TestResult:
        """Test S3 storage via port-forward"""
        if boto3 is None:
            return TestResult(
                "Storage",
                False,
                "boto3 not installed (pip3 install boto3)",
            )

        port_forward = None
        try:
            s3_config = self.config["cluster"]["s3"]
            local_port = self._find_free_port()

            # Parse endpoint to extract service name and port
            endpoint = s3_config["endpoint"]
            use_ssl = endpoint.startswith("https://")
            # Extract host:port from URL (e.g., "http://garage.garage.svc.cluster.local:3900" -> "garage.garage.svc.cluster.local:3900")
            endpoint_without_scheme = endpoint.split("://", 1)[-1]
            host_port = endpoint_without_scheme.split("/", 1)[0]  # Remove any path

            if ":" in host_port:
                host, port = host_port.rsplit(":", 1)
            else:
                port = "443" if use_ssl else "80"
                host = host_port

            # Extract service name from FQDN (e.g., "garage.garage.svc.cluster.local" -> "garage")
            service_name = host.split(".")[0]
            namespace = host.split(".")[1] if "." in host else "garage"

            # Port-forward to the in-cluster S3 service
            port_forward = subprocess.Popen(
                [
                    "kubectl",
                    "port-forward",
                    f"svc/{service_name}",
                    f"{local_port}:{port}",
                    "-n",
                    namespace,
                ],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )

            # Wait for port-forward to be ready
            time.sleep(3)

            # Connect to the in-cluster S3 service via the localhost port-forward
            s3_client = boto3.client(
                "s3",
                endpoint_url=f"http://localhost:{local_port}",
                aws_access_key_id=s3_config["access_key"],
                aws_secret_access_key=s3_config["secret_key"],
                region_name="us-east-1",
                use_ssl=False,  # Local port-forward is always unencrypted
            )

            bucket = s3_config["bucket"]
            cdc_enabled = deployment_config.get("cdc", False)

            if cdc_enabled:
                # List chunks
                # Chunks are stored in store/chunk/ (singular; see pkg/storage/chunk/s3.go)
                # For CDC deployments, background downloads happen asynchronously,
                # so we need to retry with exponential backoff to wait for chunks to be created
                max_retries = 10
                retry_delay = 1  # Start with 1 second
                chunk_count = 0

                for attempt in range(max_retries):
                    chunk_objects = s3_client.list_objects_v2(
                        Bucket=bucket,
                        Prefix="store/chunk/",  # singular: see pkg/storage/chunk/s3.go
                    )
                    chunk_count = chunk_objects.get("KeyCount", 0)

                    if chunk_count > 0:
                        break

                    if attempt < max_retries - 1:
                        self.log(
                            f"   ⏳ Waiting for background downloads to S3 (attempt {attempt + 1}/{max_retries}, {chunk_count} chunks so far)...",
                            verbose_only=True,
                        )
                        time.sleep(retry_delay)
                        retry_delay = min(
                            retry_delay * 1.5, 10
                        )  # Exponential backoff, max 10s

                if chunk_count == 0:
                    return TestResult(
                        "Storage",
                        False,
                        "No chunks found in S3 (prefix: store/chunk/)",
                    )

                return TestResult(
                    "Storage",
                    True,
                    f"S3 storage accessible ({chunk_count} chunks found)",
                )
            else:
                # List objects with prefix
                # NARs are stored in store/nar/
                nar_objects = s3_client.list_objects_v2(
                    Bucket=bucket, Prefix="store/nar/"
                )
                nar_count = nar_objects.get("KeyCount", 0)

                if nar_count == 0:
                    return TestResult(
                        "Storage",
                        False,
                        "No NAR objects found in S3 (prefix: store/nar/)",
                    )

                config_objects = s3_client.list_objects_v2(
                    Bucket=bucket, Prefix="config/"
                )
                config_count = config_objects.get("KeyCount", 0)

                return TestResult(
                    "Storage",
                    True,
                    f"S3 storage accessible ({nar_count} NAR objects, {config_count} config objects)",
                )

        except Exception as e:
            return TestResult("Storage", False, f"Error accessing S3: {e}")
        finally:
            if port_forward:
                port_forward.terminate()
                port_forward.wait(timeout=5)

    # ------------------------------------------------------------------
    # CDC lifecycle (gated on the "cdc-lifecycle" marker feature)
    # ------------------------------------------------------------------

    def _pg_fetchone(self, deployment_config: dict, sql: str):
        """Port-forward to the cluster PostgreSQL and run a single query.

        Mirrors _test_postgresql_database's connection setup. Returns the
        first row tuple, or None.
        """
        pg_config = self.config["cluster"]["postgresql"]
        host_parts = pg_config["host"].split(".")
        service_name = host_parts[0]
        namespace = host_parts[1] if len(host_parts) > 1 else "data"

        port_forward = None
        try:
            local_port = self._find_free_port()
            port_forward = subprocess.Popen(
                [
                    "kubectl",
                    "port-forward",
                    f"svc/{service_name}",
                    f"{local_port}:{pg_config['port']}",
                    "-n",
                    namespace,
                ],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            time.sleep(3)
            db_name = f"ncps_{deployment_config['name'].replace('-', '_')}"
            conn = psycopg2.connect(
                host="localhost",
                port=local_port,
                database=db_name,
                user=pg_config["username"],
                password=pg_config["password"],
            )
            try:
                cursor = conn.cursor()
                cursor.execute(sql)
                return cursor.fetchone()
            finally:
                conn.close()
        finally:
            if port_forward:
                port_forward.terminate()
                port_forward.wait(timeout=5)

    def _disable_cdc_in_configmap(self, deployment_config: dict):
        """Flip CDC off the way `helm upgrade --set config.cdc.enabled=false`
        would: drop the `cache.cdc` block from the rendered config.yaml in the
        deployment's ConfigMap. The change takes effect on the next pod boot.
        """
        name = deployment_config["name"]
        namespace = deployment_config["namespace"]
        cm_name = f"ncps-{name}"
        cm = self.k8s_core_v1.read_namespaced_config_map(cm_name, namespace)
        if not cm.data or "config.yaml" not in cm.data:
            raise RuntimeError(f"ConfigMap {cm_name} does not contain config.yaml")
        rendered = yaml.safe_load(cm.data["config.yaml"])
        if "cache" in rendered and "cdc" in rendered["cache"]:
            del rendered["cache"]["cdc"]
        cm.data["config.yaml"] = yaml.safe_dump(rendered, sort_keys=False)
        self.k8s_core_v1.replace_namespaced_config_map(cm_name, namespace, cm)

    def _rollout_restart_and_wait(self, deployment_config: dict) -> TestResult:
        """Restart the ncps Deployment and wait for pods to be ready again."""
        name = deployment_config["name"]
        namespace = deployment_config["namespace"]
        subprocess.run(
            [
                "kubectl",
                "rollout",
                "restart",
                f"deployment/ncps-{name}",
                "-n",
                namespace,
            ],
            check=True,
            capture_output=True,
        )
        subprocess.run(
            [
                "kubectl",
                "rollout",
                "status",
                f"deployment/ncps-{name}",
                "-n",
                namespace,
                "--timeout=180s",
            ],
            check=True,
            capture_output=True,
        )
        # Reuse the pod-readiness gate so subsequent checks see ready pods.
        return self._test_pods(deployment_config)

    def _drain_chunks_via_exec(self, deployment_config: dict) -> str:
        """Run `ncps migrate-chunks-to-nar` inside a running pod, reusing the
        pod's own --config (so DB URL, S3 bucket/endpoint/prefix and the S3
        credential env vars all match the serving deployment exactly).
        Returns combined stdout+stderr.
        """
        namespace = deployment_config["namespace"]
        pods = self.k8s_core_v1.list_namespaced_pod(
            namespace=namespace, label_selector="app.kubernetes.io/name=ncps"
        )
        running = [p for p in pods.items if p.status.phase == "Running"]
        if not running:
            raise RuntimeError("no running ncps pod to exec migrate-chunks-to-nar")
        pod_name = running[0].metadata.name
        result = subprocess.run(
            [
                "kubectl",
                "exec",
                pod_name,
                "-n",
                namespace,
                "--",
                "/bin/ncps",
                "--config",
                "/etc/ncps/config.yaml",
                "migrate-chunks-to-nar",
                "--force-reclaim",
            ],
            capture_output=True,
            text=True,
            timeout=600,
        )
        out = result.stdout + result.stderr
        if result.returncode != 0:
            raise RuntimeError(
                f"migrate-chunks-to-nar failed in pod {pod_name} "
                f"(exit {result.returncode}):\n{out}"
            )
        return out

    def _chunked_narinfo_hash(self, deployment_config: dict) -> Optional[str]:
        """Return a narinfo hash whose NAR is stored as chunks (total_chunks > 0),
        or None if none can be determined. Used so the drain-mode serve check
        exercises an actually-chunked NAR rather than an arbitrary test hash.
        Postgres only (the lifecycle permutation is postgres-backed).
        """
        if deployment_config.get("database", {}).get("type") != "postgresql":
            return None
        row = self._pg_fetchone(
            deployment_config,
            "SELECT n.hash FROM narinfos n "
            "JOIN narinfo_nar_files l ON l.narinfo_id = n.id "
            "JOIN nar_files f ON f.id = l.nar_file_id "
            "WHERE f.total_chunks > 0 LIMIT 1",
        )
        return row[0] if row else None

    def _serve_check(
        self, deployment_config: dict, narinfo_hash: Optional[str] = None
    ) -> bool:
        """Fetch a narinfo + its NAR through the Service; return True if both
        come back 200 with a non-empty NAR body. When narinfo_hash is given it
        is used directly (e.g. a proven-chunked hash for drain-mode checks);
        otherwise the first configured test hash is used.
        """
        namespace = deployment_config["namespace"]
        service_name = deployment_config.get("service_name", deployment_config["name"])
        if narinfo_hash is None:
            test_hashes = self.config.get("test_data", {}).get("narinfo_hashes", [])
            if not test_hashes:
                return True
            narinfo_hash = test_hashes[0]
        port_forward = None
        try:
            local_port = self._find_free_port()
            port_forward = subprocess.Popen(
                [
                    "kubectl",
                    "port-forward",
                    f"svc/{service_name}",
                    f"{local_port}:8501",
                    "-n",
                    namespace,
                ],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            time.sleep(3)
            base_url = f"http://localhost:{local_port}"
            resp = requests.get(
                f"{base_url}/{narinfo_hash}.narinfo", timeout=HTTP_TIMEOUT
            )
            if resp.status_code != 200:
                return False
            url_match = re.search(r"^URL:\s*(.+)$", resp.text, re.MULTILINE)
            if not url_match:
                return False
            nar = requests.get(
                f"{base_url}/{url_match.group(1).strip()}", timeout=HTTP_TIMEOUT
            )
            return nar.status_code == 200 and len(nar.content) > 0
        finally:
            # Transport/setup errors (port-forward, requests) propagate to the
            # caller so the TestResult points at the real infrastructure error
            # rather than a misleading "did not serve" semantic failure.
            if port_forward:
                port_forward.terminate()
                port_forward.wait(timeout=5)

    def _test_cdc_lifecycle(self, deployment_config: dict) -> TestResult:
        """Drive the non-CDC -> CDC -> drain -> non-CDC lifecycle on the
        cluster and assert DB + serving invariants at each phase.

        Phases:
          A. CDC active: chunks present + cdc_enabled config key present.
          B. Drain: disable CDC in the ConfigMap + restart; chunked NARs
             still serve (drain mode active).
          C. Drain complete: migrate-chunks-to-nar leaves zero chunked NARs.
          D. Auto-completion: a final restart clears the cdc_* config keys
             (initCDCDrainMode).

        Only PostgreSQL permutations are supported (the standard lifecycle
        permutation is ha-s3-postgres-cdc-lifecycle).
        """
        if deployment_config.get("database", {}).get("type") != "postgresql":
            return TestResult(
                "CDC Lifecycle",
                True,
                "skipped (lifecycle test implemented for PostgreSQL only)",
            )

        details = []
        try:
            # Phase A — CDC chunking active.
            chunks = self._pg_fetchone(deployment_config, "SELECT COUNT(*) FROM chunks")
            if not chunks or chunks[0] == 0:
                return TestResult(
                    "CDC Lifecycle", False, "Phase A: no chunks present; CDC not active"
                )
            cdc_present = self._pg_fetchone(
                deployment_config, "SELECT COUNT(*) FROM config WHERE key = 'cdc_enabled'"
            )
            if not cdc_present or cdc_present[0] == 0:
                return TestResult(
                    "CDC Lifecycle",
                    False,
                    "Phase A: cdc_enabled config key missing while CDC active",
                )
            # Capture a narinfo whose NAR is actually chunked, so Phase B's
            # drain-mode serve check exercises chunked serving (not an arbitrary
            # hash that might be a whole file).
            chunked_hash = self._chunked_narinfo_hash(deployment_config)
            details.append(
                f"Phase A: {chunks[0]} chunks, cdc_enabled present"
                + (f", chunked narinfo {chunked_hash}" if chunked_hash else "")
            )

            # Phase B — disable CDC, restart, expect drain-mode serving.
            self._disable_cdc_in_configmap(deployment_config)
            restart = self._rollout_restart_and_wait(deployment_config)
            if not restart.passed:
                return TestResult(
                    "CDC Lifecycle",
                    False,
                    "Phase B: pods not ready after CDC-disable restart",
                    details=restart.message,
                )
            if not self._serve_check(deployment_config, chunked_hash):
                return TestResult(
                    "CDC Lifecycle",
                    False,
                    "Phase B: chunked NAR did not serve in drain mode",
                )
            details.append("Phase B: CDC disabled, drain mode serving from chunks")

            # Phase C — drain the chunks back to whole files.
            out = self._drain_chunks_via_exec(deployment_config)
            self.log(out, verbose_only=True)
            chunked = self._pg_fetchone(
                deployment_config,
                "SELECT COUNT(*) FROM nar_files WHERE total_chunks > 0",
            )
            if chunked is None or chunked[0] != 0:
                return TestResult(
                    "CDC Lifecycle",
                    False,
                    f"Phase C: {chunked[0] if chunked else '?'} chunked NARs remain after migrate-chunks-to-nar",
                )
            details.append("Phase C: migrate-chunks-to-nar drained all chunked NARs")

            # Phase D — restart; initCDCDrainMode clears the stored CDC config.
            restart = self._rollout_restart_and_wait(deployment_config)
            if not restart.passed:
                return TestResult(
                    "CDC Lifecycle",
                    False,
                    "Phase D: pods not ready after drain-completion restart",
                    details=restart.message,
                )
            cdc_after = self._pg_fetchone(
                deployment_config, "SELECT COUNT(*) FROM config WHERE key = 'cdc_enabled'"
            )
            if cdc_after is None or cdc_after[0] != 0:
                return TestResult(
                    "CDC Lifecycle",
                    False,
                    "Phase D: cdc_enabled not cleared by initCDCDrainMode after drain",
                )
            details.append("Phase D: initCDCDrainMode cleared stored CDC config")

            return TestResult(
                "CDC Lifecycle",
                True,
                "non-CDC -> CDC -> drain -> non-CDC lifecycle completed",
                details="\n".join(details),
            )
        except Exception as e:
            return TestResult(
                "CDC Lifecycle",
                False,
                f"Lifecycle error: {e}",
                details="\n".join(details),
            )

    def _pod_presence_ok(self, namespace: str, pod: str, narinfo_hash: str) -> Optional[bool]:
        """Port-forward to a single pod and check HEAD/GET agreement for the NAR
        referenced by `narinfo_hash`. Returns:
          - True  : narinfo + NAR served, and HEAD status == GET status with
                    a non-empty 200 body (no phantom presence).
          - False : a phantom was observed (HEAD 200 but GET not-200/empty, or
                    HEAD/GET disagree) — the bug class this guards against.
          - None  : the narinfo/NAR is not present on this replica (consistent
                    absence is handled by the caller's cross-replica check).
        Retries with bounded backoff to tolerate shared-storage propagation lag.
        """
        port_forward = None
        try:
            local_port = self._find_free_port()
            port_forward = subprocess.Popen(
                [
                    "kubectl",
                    "port-forward",
                    f"pod/{pod}",
                    f"{local_port}:8501",
                    "-n",
                    namespace,
                ],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            time.sleep(3)
            base = f"http://localhost:{local_port}"
            resp = requests.get(
                f"{base}/{narinfo_hash}.narinfo", timeout=HTTP_TIMEOUT
            )
            if resp.status_code == 404:
                return None
            if resp.status_code != 200:
                return False
            m = re.search(r"^URL:\s*(.+)$", resp.text, re.MULTILINE)
            if not m:
                return False
            nar = m.group(1).strip()
            # Bounded retry for storage-lag tolerance.
            last = False
            for attempt in range(5):
                head = requests.head(f"{base}/{nar}", timeout=HTTP_TIMEOUT)
                get = requests.get(f"{base}/{nar}", timeout=HTTP_TIMEOUT)
                if head.status_code == 200 and (
                    get.status_code != 200 or len(get.content) == 0
                ):
                    # Phantom: HEAD claims present but bytes are absent.
                    return False
                if head.status_code == get.status_code == 200 and len(get.content) > 0:
                    return True
                if head.status_code == 404 and get.status_code == 404:
                    last = None
                time.sleep(2 * (attempt + 1))
            return last
        finally:
            # Let transport/setup errors propagate so the caller reports the
            # real infrastructure failure instead of a false "phantom HEAD".
            if port_forward:
                port_forward.terminate()
                port_forward.wait(timeout=5)

    def _test_cdc_topology(self, deployment_config: dict) -> TestResult:
        """Multi-replica topology assertions (run for every multi-replica
        deployment; self-skips single-replica / no-test-data):

          6.2 Multi-replica shared-DB presence — every replica agrees on the
              presence of the same NAR (no replica is missing what the shared
              DB/storage says exists).
          6.3 Storage-lag tolerance / no phantom HEAD — on each replica, a NAR's
              HEAD never returns 200 while its bytes are absent (HEAD agrees with
              GET), verified with bounded-backoff retries.

        Requires >1 replica; skips otherwise.
        """
        namespace = deployment_config["namespace"]
        replicas = deployment_config.get("replicas", 1)
        if replicas < 2:
            return TestResult(
                "CDC Topology", True, "skipped (single replica)"
            )
        test_hashes = self.config.get("test_data", {}).get("narinfo_hashes", [])
        if not test_hashes:
            return TestResult(
                "CDC Topology", True, "skipped (no test data configured)"
            )

        narinfo_hash = test_hashes[0]
        try:
            pods = self.k8s_core_v1.list_namespaced_pod(
                namespace=namespace, label_selector="app.kubernetes.io/name=ncps"
            )
            running = [p.metadata.name for p in pods.items if p.status.phase == "Running"]
            if len(running) < 2:
                return TestResult(
                    "CDC Topology",
                    False,
                    f"expected >= 2 running pods, found {len(running)}",
                )

            results = {pod: self._pod_presence_ok(namespace, pod, narinfo_hash) for pod in running}
            details = "\n".join(f"{pod}: {state}" for pod, state in results.items())

            # 6.3: a phantom (False) on any replica is a hard failure.
            if any(state is False for state in results.values()):
                return TestResult(
                    "CDC Topology",
                    False,
                    "phantom presence (HEAD 200 with absent bytes) or HEAD/GET disagreement on a replica",
                    details=details,
                )
            # 6.2: replicas must agree. A mix means a replica cannot see what
            # the shared DB/storage holds.
            states = set(results.values())
            if len(states) > 1:
                return TestResult(
                    "CDC Topology",
                    False,
                    "cross-replica presence disagreement (a replica is missing a shared NAR)",
                    details=details,
                )
            # The test hash was already fetched via _test_http_endpoints, so it
            # must be present on every replica. "absent everywhere" means no
            # replica can serve it anymore — a real failure, not a pass.
            if states == {None}:
                return TestResult(
                    "CDC Topology",
                    False,
                    f"NAR {narinfo_hash} absent on all {len(running)} replicas "
                    "(was served earlier via the Service)",
                    details=details,
                )
            return TestResult(
                "CDC Topology",
                True,
                f"cross-replica presence consistent across {len(running)} replicas; no phantom HEAD",
                details=details,
            )
        except Exception as e:
            return TestResult("CDC Topology", False, f"Topology error: {e}")

    def _find_free_port(self) -> int:
        """Find a free port for port-forwarding"""
        import socket

        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            # Bind only to the loopback interface to avoid exposing a listening socket on all interfaces
            s.bind(("127.0.0.1", 0))
            s.listen(1)
            port = s.getsockname()[1]
        return port

    def print_summary(self, all_results: Dict[str, DeploymentTestResult]):
        """Print final summary"""
        print(f"\n\n{'=' * 80}")
        print("TEST SUMMARY")
        print(f"{'=' * 80}\n")

        total_deployments = len(all_results)
        passed_deployments = sum(1 for r in all_results.values() if r.passed)
        failed_deployments = total_deployments - passed_deployments

        for name, result in all_results.items():
            status = "✅ PASS" if result.passed else "❌ FAIL"
            print(
                f"{status} {name} ({result.passed_count}/{len(result.results)} checks)"
            )

            # Always show all check results in summary
            for test_result in result.results:
                status_icon = "✅" if test_result.passed else "❌"
                print(f"     {status_icon} {test_result.name}: {test_result.message}")
                if not test_result.passed and test_result.details:
                    for line in test_result.details.split("\n"):
                        print(f"        {line}")

        print(f"\n{'=' * 80}")
        print(f"Total: {passed_deployments}/{total_deployments} deployments passed")
        print(f"{'=' * 80}\n")

        return failed_deployments == 0


# NCPSTester class and main functionality remain above
