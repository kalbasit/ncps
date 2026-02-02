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
                    # Fetch narinfo
                    resp = requests.get(
                        f"{base_url}/{narinfo_hash}.narinfo", timeout=30
                    )
                    if resp.status_code != 200:
                        return TestResult(
                            "HTTP Endpoints",
                            False,
                            f"Failed to fetch narinfo {narinfo_hash}: HTTP {resp.status_code}",
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

                    # Fetch the NAR file
                    resp = requests.get(f"{base_url}/{nar_url}", timeout=30)
                    if resp.status_code != 200:
                        return TestResult(
                            "HTTP Endpoints",
                            False,
                            f"Failed to fetch NAR {nar_url}: HTTP {resp.status_code}",
                        )

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

            # Copy database to /tmp and query it (avoids permission issues with read-only directories)
            # Using nouchka/sqlite3 image which has sqlite pre-installed
            result = subprocess.run(
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
                    "--profile=restricted",
                    "--",
                    "sh",
                    "-c",
                    f"cp {target_db_path} /tmp/test.db && sqlite3 /tmp/test.db '.tables'",
                ],
                capture_output=True,
                text=True,
                timeout=60,
            )

            if result.returncode != 0:
                return TestResult(
                    "Database",
                    False,
                    f"Failed to access SQLite database at {db_path}",
                    details=f"Return code: {result.returncode}\nstderr: {result.stderr}\nstdout: {result.stdout}",
                )

            tables_output = result.stdout.strip()

            if "nar_files" not in tables_output:
                return TestResult(
                    "Database",
                    False,
                    f"Expected 'nar_files' table not found in SQLite database",
                    details=f"Tables found: '{tables_output}'",
                )

            # Count rows
            result = subprocess.run(
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
                    "--profile=restricted",
                    "--",
                    "sh",
                    "-c",
                    f"cp {target_db_path} /tmp/test.db && sqlite3 /tmp/test.db 'SELECT COUNT(*) FROM nar_files;'",
                ],
                capture_output=True,
                text=True,
                timeout=60,
            )

            if result.returncode != 0:
                return TestResult(
                    "Database",
                    False,
                    f"Failed to count rows in SQLite database",
                    details=f"stderr: {result.stderr}\nstdout: {result.stdout}",
                )

            row_count = result.stdout.strip()

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

            conn = psycopg2.connect(
                host="localhost",
                port=local_port,
                database=pg_config["database"],
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
            cursor.execute(f"SELECT COUNT(*) FROM {target_table}")
            count = cursor.fetchone()[0]

            conn.close()

            entry_type = "chunks" if cdc_enabled else "NAR entries"
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

            conn = pymysql.connect(
                host="localhost",
                port=local_port,
                database=mysql_config["database"],
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
            for subdir in ["config", "nar"]:
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
                    f"find {target_storage_path}/nar -type f 2>/dev/null | wc -l",
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

            file_count = result.stdout.strip()

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
            # Extract host:port from URL (e.g., "http://minio.minio.svc.cluster.local:9000" -> "minio.minio.svc.cluster.local:9000")
            endpoint_without_scheme = endpoint.split("://", 1)[-1]
            host_port = endpoint_without_scheme.split("/", 1)[0]  # Remove any path

            if ":" in host_port:
                host, port = host_port.rsplit(":", 1)
            else:
                port = "443" if use_ssl else "80"
                host = host_port

            # Extract service name from FQDN (e.g., "minio.minio.svc.cluster.local" -> "minio")
            service_name = host.split(".")[0]
            namespace = host.split(".")[1] if "." in host else "minio"

            # Port-forward to MinIO service
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

            # Connect to MinIO via localhost
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
                # Chunks are stored in store/chunks/
                chunk_objects = s3_client.list_objects_v2(Bucket=bucket, Prefix="store/chunks/")
                chunk_count = chunk_objects.get("KeyCount", 0)

                if chunk_count == 0:
                    return TestResult(
                        "Storage",
                        False,
                        "No chunks found in S3 (prefix: store/chunks/)",
                    )

                return TestResult(
                    "Storage",
                    True,
                    f"S3 storage accessible ({chunk_count} chunks found)",
                )
            else:
                # List objects with prefix
                # NARs are stored in store/nar/
                nar_objects = s3_client.list_objects_v2(Bucket=bucket, Prefix="store/nar/")
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

    def _find_free_port(self) -> int:
        """Find a free port for port-forwarding"""
        import socket

        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            s.bind(("", 0))
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


def main():
    parser = argparse.ArgumentParser(
        description="Test NCPS Kubernetes deployments",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )

    parser.add_argument(
        "config",
        help="Path to test-config.yaml",
    )

    parser.add_argument(
        "-d",
        "--deployment",
        help="Test only this deployment (by name)",
    )

    parser.add_argument(
        "-v",
        "--verbose",
        action="store_true",
        help="Verbose output",
    )

    args = parser.parse_args()

    # Check config exists
    if not os.path.exists(args.config):
        print(f"❌ Config file not found: {args.config}")
        sys.exit(1)

    tester = NCPSTester(args.config, verbose=args.verbose)
    results = tester.test_all_deployments(deployment_filter=args.deployment)

    if not results:
        print("❌ No deployments tested")
        sys.exit(1)

    success = tester.print_summary(results)
    sys.exit(0 if success else 1)


if __name__ == "__main__":
    main()
