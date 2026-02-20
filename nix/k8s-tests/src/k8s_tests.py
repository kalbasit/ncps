#!/usr/bin/env python3
"""
NCPS Kubernetes Integration Testing CLI

Provides unified interface for Kind cluster integration testing:
- cluster management (create/destroy/info)
- test values generation
- deployment management (install/test/cleanup)
- comprehensive integration testing
"""

import argparse
import base64
import json
import os
import re
import shutil
import subprocess
import sys
import time
import urllib.parse
from dataclasses import dataclass
from datetime import datetime
from typing import Any, Dict, List, Optional

import requests
import yaml

try:
    import boto3
    import psycopg2
    import pymysql
    from kubernetes import client
    from kubernetes import config as k8s_config
except ImportError:
    # Failures here are handled gracefully if specific tests are run
    boto3 = None
    psycopg2 = None
    pymysql = None
    client = None
    k8s_config = None

# Constants
REPO_ROOT = (
    subprocess.check_output(["git", "rev-parse", "--show-toplevel"]).decode().strip()
)
TEST_VALUES_DIR = os.path.join(REPO_ROOT, "charts/ncps/test-values")
CHART_DIR = os.path.join(REPO_ROOT, "charts/ncps")
CLUSTER_NAME = "ncps-kind"


class K8sTestsCLI:
    def __init__(self, verbose: bool = False):
        self.verbose = verbose
        # Path to config is substituted by Nix or can be provided via env
        self.config_file = os.environ.get(
            "CONFIG_FILE", os.path.join(REPO_ROOT, "nix/k8s-tests/config.nix")
        )

    def log(self, msg: str):
        print(msg)

    def error(self, msg: str):
        print(f"❌ Error: {msg}", file=sys.stderr)
        sys.exit(1)

    def run_cmd(
        self,
        cmd: List[str],
        capture_output: bool = False,
        check: bool = True,
        cwd: Optional[str] = None,
        input: Optional[Any] = None,
        stdin: Optional[Any] = None,
        text: bool = True,
    ):
        if self.verbose:
            self.log(f"Running: {' '.join(cmd)}")
        try:
            return subprocess.run(
                cmd,
                capture_output=capture_output,
                text=text,
                check=check,
                cwd=cwd,
                input=input,
                stdin=stdin,
            )
        except subprocess.CalledProcessError as e:
            err_msg = f"Command failed with exit code {e.returncode}: {' '.join(cmd)}"
            if e.stdout:
                err_msg += f"\nSTDOUT: {e.stdout.strip()}"
            if e.stderr:
                err_msg += f"\nSTDERR: {e.stderr.strip()}"
            self.error(err_msg)
        except FileNotFoundError:
            self.error(f"Command not found: {cmd[0]}")

    # --- Cluster Management ---

    def cmd_cluster_create(self):
        self.log("🚀 Initializing NCPS Kubernetes Development Environment...")

        # Pre-flight checks
        for cmd in ["docker", "kind", "kubectl", "helm"]:
            if not shutil.which(cmd):
                self.error(f"'{cmd}' is not installed.")

        # Check docker running
        self.run_cmd(["docker", "info"], capture_output=True)

        # Create Kind Cluster
        clusters = self.run_cmd(
            ["kind", "get", "clusters"], capture_output=True
        ).stdout.splitlines()
        if CLUSTER_NAME not in clusters:
            self.log("📦 Creating Kind cluster...")
            kind_config = f"""
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  dnsSearch: []
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 30000
    hostPort: 30000
    listenAddress: "127.0.0.1"
    protocol: TCP
"""
            self.run_cmd(
                ["kind", "create", "cluster", "--name", CLUSTER_NAME, "--config", "-"],
                input=kind_config,
            )
            self.log("✅ Cluster created.")
        else:
            self.log(f"✅ Cluster '{CLUSTER_NAME}' already exists. Skipping creation.")

        self.run_cmd(["kubectl", "config", "use-context", f"kind-{CLUSTER_NAME}"])

        # Install Infrastructure
        self.log("🏗️  Installing infrastructure components...")

        # Helm repos
        repos = {
            "minio": "https://charts.min.io/",
            "cnpg": "https://cloudnative-pg.io/charts/",
            "mariadb-operator": "https://mariadb-operator.github.io/mariadb-operator/",
            "ot-helm": "https://ot-container-kit.github.io/helm-charts/",
        }
        for name, url in repos.items():
            self.run_cmd(["helm", "repo", "add", name, url, "--force-update"])
        self.run_cmd(["helm", "repo", "update"])

        # MinIO
        self.log("   - Installing MinIO...")
        self.run_cmd(
            [
                "helm",
                "upgrade",
                "--install",
                "minio",
                "minio/minio",
                "--namespace",
                "minio",
                "--create-namespace",
                "--set",
                "resources.requests.memory=256Mi",
                "--set",
                "mode=standalone",
                "--set",
                "rootUser=admin",
                "--set",
                "rootPassword=password123",
                "--set",
                "persistence.enabled=true",
                "--set",
                "persistence.size=5Gi",
                "--wait",
            ]
        )

        # Configure MinIO
        self.log("   ⚙️  Configuring MinIO (bucket and access keys)...")
        mc_cmd = """
            set -e
            echo '--> Waiting for MinIO service...'
            until mc alias set internal http://minio.minio.svc.cluster.local:9000 admin password123; do
                echo '    MinIO not ready yet, retrying in 2s...'
                sleep 2
            done
            mc mb internal/ncps-bucket || true
            mc admin user svcacct add --access-key 'ncps-access-key' --secret-key 'ncps-secret-key' internal admin || true
        """
        self.run_cmd(
            [
                "kubectl",
                "run",
                "minio-configurator",
                "--namespace",
                "minio",
                "--image=minio/mc",
                "--restart=Never",
                "--rm",
                "-i",
                "--command",
                "--",
                "/bin/sh",
                "-c",
                mc_cmd,
            ]
        )

        # Registry
        self.log("   - Installing Container Registry...")
        self.run_cmd(["kubectl", "create", "namespace", "registry"], check=False)
        registry_manifest = """
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: registry-pvc
  namespace: registry
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 5Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: registry
  namespace: registry
spec:
  replicas: 1
  selector:
    matchLabels: { app: registry }
  template:
    metadata:
      labels: { app: registry }
    spec:
      containers:
      - name: registry
        image: registry:2
        ports: [{ containerPort: 5000 }]
        env: [{ name: REGISTRY_STORAGE_DELETE_ENABLED, value: "true" }]
        volumeMounts: [{ name: registry-storage, mountPath: /var/lib/registry }]
      volumes: [{ name: registry-storage, persistentVolumeClaim: { claimName: registry-pvc } }]
---
apiVersion: v1
kind: Service
metadata:
  name: registry
  namespace: registry
spec:
  type: NodePort
  selector: { app: registry }
  ports: [{ port: 5000, targetPort: 5000, nodePort: 30000, protocol: TCP }]
"""
        self.run_cmd(["kubectl", "apply", "-f", "-"], input=registry_manifest)
        self.run_cmd(
            [
                "kubectl",
                "wait",
                "--for=condition=Ready",
                "pod",
                "-l",
                "app=registry",
                "-n",
                "registry",
                "--timeout=180s",
            ]
        )

        # Operators
        self.log("   - Installing Operators...")
        self.run_cmd(
            [
                "helm",
                "upgrade",
                "--install",
                "cnpg",
                "cnpg/cloudnative-pg",
                "--namespace",
                "cnpg-system",
                "--create-namespace",
                "--wait",
            ]
        )
        self.run_cmd(
            [
                "helm",
                "upgrade",
                "--install",
                "mariadb-operator-crds",
                "mariadb-operator/mariadb-operator-crds",
                "--namespace",
                "mariadb-system",
                "--create-namespace",
                "--wait",
            ]
        )
        self.run_cmd(
            [
                "helm",
                "upgrade",
                "--install",
                "mariadb-operator",
                "mariadb-operator/mariadb-operator",
                "--namespace",
                "mariadb-system",
                "--create-namespace",
                "--set",
                "webhook.cert.certManager.enabled=false",
                "--wait",
            ]
        )
        self.run_cmd(
            [
                "helm",
                "upgrade",
                "--install",
                "redis-operator",
                "ot-helm/redis-operator",
                "--namespace",
                "redis-system",
                "--create-namespace",
                "--wait",
            ]
        )

        # Deploy Databases
        self.log("🔥 Deploying database instances...")
        self.run_cmd(["kubectl", "create", "namespace", "data"], check=False)

        pg_manifest = """
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: pg17-ncps
  namespace: data
spec:
  instances: 1
  imageName: ghcr.io/cloudnative-pg/postgresql:17
  storage:
    size: 1Gi
  bootstrap:
    initdb:
      database: ncps
      owner: ncps
"""
        maria_manifest = """
apiVersion: k8s.mariadb.com/v1alpha1
kind: MariaDB
metadata:
  name: mariadb-ncps
  namespace: data
spec:
  rootPasswordSecretKeyRef:
    name: mariadb-root-password
    key: password
    generate: true
  username: ncps
  passwordSecretKeyRef:
    name: mariadb-ncps-password
    key: password
    generate: true
  database: ncps
  storage:
    size: 1Gi
    storageClassName: standard
  replicas: 1
"""
        redis_manifest = """
apiVersion: redis.redis.opstreelabs.in/v1beta2
kind: Redis
metadata:
  name: redis-ncps
  namespace: data
spec:
  kubernetesConfig:
    image: redis:7.0
    imagePullPolicy: IfNotPresent
  storage:
    volumeClaimTemplate:
      spec:
        storageClassName: standard
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 1Gi
"""
        self.run_cmd(["kubectl", "apply", "-f", "-"], input=pg_manifest)
        self.run_cmd(["kubectl", "apply", "-f", "-"], input=maria_manifest)
        self.run_cmd(["kubectl", "apply", "-f", "-"], input=redis_manifest)

        self.log("⏳ Waiting for databases to initialize...")
        self._wait_for_pods("cnpg.io/cluster=pg17-ncps", "data")
        self._wait_for_pods("app.kubernetes.io/instance=mariadb-ncps", "data")
        self._wait_for_pods("app=redis-ncps", "data")

        # Wait for PostgreSQL primary to be fully ready for connections
        # CNPG creates multiple pods (init job, primary instance, etc)
        # We need to ensure the primary instance pod (pg17-ncps-1) exists and is ready
        self.log("   Waiting for PostgreSQL primary instance to be fully ready...")
        for attempt in range(30):
            try:
                # Check if pg17-ncps-1 pod exists and is Ready
                res = self.run_cmd(
                    [
                        "kubectl",
                        "get",
                        "pod",
                        "-n",
                        "data",
                        "pg17-ncps-1",
                        "-o",
                        'jsonpath={.status.conditions[?(@.type=="Ready")].status}',
                    ],
                    capture_output=True,
                    check=False,
                )
                if res.stdout.strip() == "True":
                    # Pod exists and is Ready, now verify PostgreSQL is responding
                    check_result = self.run_cmd(
                        [
                            "kubectl",
                            "exec",
                            "-n",
                            "data",
                            "pg17-ncps-1",
                            "--",
                            "pg_isready",
                            "-U",
                            "postgres",
                        ],
                        capture_output=True,
                        check=False,
                    )
                    if check_result.returncode == 0:
                        self.log("   ✅ PostgreSQL is ready for connections")
                        break
            except Exception:
                pass
            time.sleep(2)
        else:
            self.error("Timed out waiting for PostgreSQL primary instance to be ready")

        # Create per-test databases for isolation
        self.log("🔐 Creating per-test databases for isolation...")

        # Load permutations from Nix config
        permutations = json.loads(
            self.run_cmd(
                ["nix", "eval", "--json", "--file", self.config_file, "permutations"],
                capture_output=True,
            ).stdout
        )

        # PostgreSQL databases
        pgsql_databases = []
        for perm in permutations:
            if perm.get("database", {}).get("type") == "postgresql":
                db_name = f"ncps_{perm['name'].replace('-', '_')}"
                pgsql_databases.append(db_name)

        for db_name in sorted(set(pgsql_databases)):
            self.log(f"   - Creating PostgreSQL database: {db_name}")
            self.run_cmd(
                [
                    "kubectl",
                    "exec",
                    "-n",
                    "data",
                    "pg17-ncps-1",
                    "--",
                    "psql",
                    "-U",
                    "postgres",
                    "-c",
                    f"CREATE DATABASE {db_name};",
                ],
                check=False,
            )  # Ignore error if database already exists
            self.run_cmd(
                [
                    "kubectl",
                    "exec",
                    "-n",
                    "data",
                    "pg17-ncps-1",
                    "--",
                    "psql",
                    "-U",
                    "postgres",
                    "-c",
                    f"GRANT ALL PRIVILEGES ON DATABASE {db_name} TO ncps;",
                ]
            )
            # Set the public schema owner to ncps so it can create tables
            self.run_cmd(
                [
                    "kubectl",
                    "exec",
                    "-n",
                    "data",
                    "pg17-ncps-1",
                    "--",
                    "psql",
                    "-U",
                    "postgres",
                    "-d",
                    db_name,
                    "-c",
                    "ALTER SCHEMA public OWNER TO ncps;",
                ]
            )

        # MariaDB databases
        mariadb_databases = []
        for perm in permutations:
            if perm.get("database", {}).get("type") == "mysql":
                db_name = f"ncps_{perm['name'].replace('-', '_')}"
                mariadb_databases.append(db_name)

        # Wait for MariaDB primary to be fully ready for connections
        # MariaDB operator creates the primary instance pod (mariadb-ncps-0)
        if mariadb_databases:
            self.log("   Waiting for MariaDB primary instance to be fully ready...")
            for attempt in range(30):
                try:
                    # Check if mariadb-ncps-0 pod exists and is Ready
                    res = self.run_cmd(
                        [
                            "kubectl",
                            "get",
                            "pod",
                            "-n",
                            "data",
                            "mariadb-ncps-0",
                            "-o",
                            'jsonpath={.status.conditions[?(@.type=="Ready")].status}',
                        ],
                        capture_output=True,
                        check=False,
                    )
                    if res.stdout.strip() == "True":
                        # Pod exists and is Ready, now verify MariaDB is responding
                        check_result = self.run_cmd(
                            [
                                "kubectl",
                                "exec",
                                "-n",
                                "data",
                                "mariadb-ncps-0",
                                "--",
                                "mariadb-admin",
                                "ping",
                                "-h",
                                "localhost",
                            ],
                            capture_output=True,
                            check=False,
                        )
                        if check_result.returncode == 0:
                            self.log("   ✅ MariaDB is ready for connections")
                            break
                except Exception:
                    pass
                time.sleep(2)
            else:
                self.error("Timed out waiting for MariaDB primary instance to be ready")

        # Get MariaDB root password
        creds = self.get_cluster_creds()
        mariadb_root_password_b64 = self.run_cmd(
            [
                "kubectl",
                "get",
                "secret",
                "-n",
                "data",
                "mariadb-root-password",
                "-o",
                "jsonpath={.data.password}",
            ],
            capture_output=True,
        ).stdout
        mariadb_root_password = base64.b64decode(mariadb_root_password_b64).decode()

        for db_name in sorted(set(mariadb_databases)):
            self.log(f"   - Creating MariaDB database: {db_name}")
            self.run_cmd(
                [
                    "kubectl",
                    "exec",
                    "-n",
                    "data",
                    "mariadb-ncps-0",
                    "--",
                    "mariadb",
                    "-u",
                    "root",
                    f"-p{mariadb_root_password}",
                    "-e",
                    f"CREATE DATABASE IF NOT EXISTS {db_name};",
                ]
            )
            self.run_cmd(
                [
                    "kubectl",
                    "exec",
                    "-n",
                    "data",
                    "mariadb-ncps-0",
                    "--",
                    "mariadb",
                    "-u",
                    "root",
                    f"-p{mariadb_root_password}",
                    "-e",
                    f"GRANT ALL PRIVILEGES ON {db_name}.* TO 'ncps'@'%';",
                ]
            )

        self.log("✅ Cluster created successfully!")
        self.cmd_cluster_info()

    def _wait_for_pods(self, label: str, ns: str):
        # Wait for resources to appear
        for _ in range(30):
            res = self.run_cmd(
                ["kubectl", "get", "pod", "-l", label, "-n", ns, "-o", "name"],
                capture_output=True,
                check=False,
            )
            if res.stdout.strip():
                break
            time.sleep(2)
        else:
            self.error(
                f"Timed out waiting for pods with label '{label}' to appear in namespace '{ns}'"
            )

        # Wait for condition with retries for transient failures (e.g., pod deleted while waiting)
        for _ in range(5):
            try:
                self.run_cmd(
                    [
                        "kubectl",
                        "wait",
                        "--for=condition=Ready",
                        "pod",
                        "-l",
                        label,
                        "-n",
                        ns,
                        "--timeout=180s",
                    ]
                )
                return
            except subprocess.CalledProcessError:
                self.log(f"   ⚠️  'kubectl wait' failed, retrying...")
                time.sleep(5)

        self.error(
            f"Timed out waiting for pods with label '{label}' to be ready in namespace '{ns}'"
        )

    def cmd_cluster_destroy(self):
        self.log(f"🗑️  Destroying Kind cluster '{CLUSTER_NAME}'...")
        self.run_cmd(["kind", "delete", "cluster", "--name", CLUSTER_NAME], check=False)
        self.log("✅ Cluster destroyed successfully.")

    def get_cluster_creds(self) -> Dict[str, Any]:
        creds = {
            "s3": {
                "endpoint": "http://minio.minio.svc.cluster.local:9000",
                "bucket": "ncps-bucket",
                "access_key": "ncps-access-key",
                "secret_key": "ncps-secret-key",
            },
            "postgresql": {
                "host": "pg17-ncps-rw.data.svc.cluster.local",
                "port": 5432,
                "database": "ncps",
                "username": "ncps",
                "password": "",
            },
            "mariadb": {
                "host": "mariadb-ncps.data.svc.cluster.local",
                "port": 3306,
                "database": "ncps",
                "username": "ncps",
                "password": "",
            },
            "redis": {
                "host": "redis-ncps.data.svc.cluster.local",
                "port": 6379,
                "password": "",
            },
        }

        # Fetch dynamic passwords
        try:
            pg_pass_b64 = self.run_cmd(
                [
                    "kubectl",
                    "get",
                    "secret",
                    "-n",
                    "data",
                    "pg17-ncps-app",
                    "-o",
                    "jsonpath={.data.password}",
                ],
                capture_output=True,
            ).stdout
            if pg_pass_b64:
                creds["postgresql"]["password"] = base64.b64decode(pg_pass_b64).decode()

            maria_pass_b64 = self.run_cmd(
                [
                    "kubectl",
                    "get",
                    "secret",
                    "-n",
                    "data",
                    "mariadb-ncps-password",
                    "-o",
                    "jsonpath={.data.password}",
                ],
                capture_output=True,
            ).stdout
            if maria_pass_b64:
                creds["mariadb"]["password"] = base64.b64decode(maria_pass_b64).decode()

            redis_pass_b64 = self.run_cmd(
                [
                    "kubectl",
                    "get",
                    "secret",
                    "-n",
                    "data",
                    "redis-ncps",
                    "-o",
                    "jsonpath={.data.password}",
                ],
                capture_output=True,
                check=False,
            ).stdout
            if redis_pass_b64:
                creds["redis"]["password"] = base64.b64decode(redis_pass_b64).decode()
        except (subprocess.CalledProcessError, base64.binascii.Error, IndexError) as e:
            self.log(
                f"   ⚠️  Could not fetch dynamic credentials, proceeding with defaults. Error: {e}"
            )

        return creds

    def cmd_cluster_info(self, json_output: bool = False):
        creds = self.get_cluster_creds()
        if json_output:
            print(json.dumps(creds, indent=2))
            return

        self.log("\n========================================================")
        self.log("✅ NCPS Kubernetes Development Environment")
        self.log("========================================================")
        self.log(f"Cluster: {CLUSTER_NAME}")
        self.log(f"Context: kind-{CLUSTER_NAME}")
        self.log("\n--- 📦 Container Registry ---")
        self.log("  Location: 127.0.0.1:30000")
        self.log("\n--- 🪣 S3 (MinIO) ---")
        for k, v in creds["s3"].items():
            self.log(f"  {k.capitalize()}: {v}")
        self.log("\n--- 🐘 PostgreSQL 17 ---")
        for k, v in creds["postgresql"].items():
            self.log(f"  {k.capitalize()}: {v}")
        self.log("\n--- 🐬 MariaDB ---")
        for k, v in creds["mariadb"].items():
            self.log(f"  {k.capitalize()}: {v}")
        self.log("\n--- 🔺 Redis ---")
        self.log(f"  Host: {creds['redis']['host']}")
        self.log(f"  Port: {creds['redis']['port']}")
        self.log(f"  Pass: {creds['redis']['password'] or '<none>'}")
        self.log("========================================================\n")

    # --- Generation ---

    def _get_nix_platform(self) -> str:
        """Determine the Nix platform to build for based on host OS and architecture."""
        system = os.uname().sysname.lower()
        machine = os.uname().machine.lower()

        if system == "darwin":
            if machine == "arm64":
                return "aarch64-darwin"
            elif machine == "x86_64":
                return "x86_64-darwin"
        elif system == "linux":
            if machine in ("arm64", "aarch64"):
                return "aarch64-linux"
            elif machine in ("x86_64", "amd64"):
                return "x86_64-linux"

        self.error(f"Unsupported OS/architecture combination: {system}/{machine}")

    def cmd_generate(
        self, push: bool, last: bool, tag: Optional[str], registry: str, repository: str
    ):
        image_tag = tag

        if last:
            state_file = os.path.join(TEST_VALUES_DIR, ".last_image_state.json")

            if not os.path.exists(state_file):
                self.error(
                    "No image state found. Run 'k8s-tests generate --push' first to build and track image."
                )

            with open(state_file, "r") as f:
                state = json.load(f)

            image_tag = state["image_tag"]
            nix_store_path = state.get("nix_store_path")

            self.log(f"⏮️  Reusing last image tag: {image_tag}")

            # If --push is also specified, re-push the image directly from Nix store
            if push:
                if not nix_store_path or not os.path.exists(nix_store_path):
                    self.error(
                        f"Nix store path is invalid/missing: {nix_store_path}\n"
                        f"Run 'k8s-tests generate --push' to build and track a new image."
                    )

                self.log("📤 Re-pushing previously built image to registry...")
                full_image = f"{registry}/{repository}:{image_tag}"
                self.log(f"📤 Pushing to registry: {full_image}")

                # Push directly from docker-archive (like CI does in push-docker-image script)
                self.run_cmd(
                    [
                        "skopeo",
                        "--insecure-policy",
                        "copy",
                        "--dest-tls-verify=false",
                        f"docker-archive:{nix_store_path}",
                        f"docker://{full_image}",
                    ]
                )

                self.log(f"✅ Successfully pushed {full_image}")
        elif push:
            nix_platform = self._get_nix_platform()
            self.log(f"🔨 Building Docker image with Nix for {nix_platform}...")
            build_path = self.run_cmd(
                [
                    "nix",
                    "build",
                    f"{REPO_ROOT}#packages.{nix_platform}.docker",
                    "--print-out-paths",
                    "--no-link",
                ],
                capture_output=True,
            ).stdout.strip()

            self.log("📦 Loading image into Docker...")
            with open(build_path, "rb") as f:
                load_output = self.run_cmd(
                    ["docker", "load"], stdin=f, capture_output=True, text=False
                ).stdout.decode()
            self.log(load_output)

            # Loaded image: 127.0.0.1:30000/ncps:p8xwc56qrjpbfssbfz7vwxs0n028sqav
            match = re.search(r"Loaded image: (.+)", load_output)
            if not match:
                self.error("Could not determine loaded image name.")
            nix_image = match.group(1)
            image_tag = nix_image.split(":")[-1]

            self.log("📤 Pushing image to local registry...")
            full_image = f"{registry}/{repository}:{image_tag}"
            self.run_cmd(
                [
                    "skopeo",
                    "--insecure-policy",
                    "copy",
                    "--dest-tls-verify=false",
                    f"docker-daemon:{nix_image}",
                    f"docker://{full_image}",
                ]
            )
            self.log(f"✅ Image pushed: {full_image}")

            # Save image state for re-pushing later
            state = {
                "image_tag": image_tag,
                "nix_store_path": build_path,  # Path to docker-archive tarball
                "timestamp": datetime.utcnow().isoformat() + "Z",
                "platform": nix_platform,
            }

            state_file = os.path.join(TEST_VALUES_DIR, ".last_image_state.json")
            os.makedirs(TEST_VALUES_DIR, exist_ok=True)
            with open(state_file, "w") as f:
                json.dump(state, f, indent=2)

        if not image_tag:
            self.error("Image tag required.")

        creds = self.get_cluster_creds()

        self.log("📋 Loading test permutations from config...")
        # Call Nix to get values
        args_json = json.dumps(
            {
                "image_registry": registry,
                "image_repository": repository,
                "image_tag": image_tag,
                "cluster": json.dumps(creds),
            }
        )

        nix_eval_cmd = [
            "nix",
            "eval",
            "--json",
            "--file",
            self.config_file,
            "generateValues",
            "--apply",
            f"f: f (builtins.fromJSON ''{args_json}'')",
        ]
        values_json = json.loads(self.run_cmd(nix_eval_cmd, capture_output=True).stdout)

        for name, config in values_json.items():
            self.log(f"  Generating {name}.yaml...")
            with open(os.path.join(TEST_VALUES_DIR, f"{name}.yaml"), "w") as f:
                f.write("# Auto-generated from config.nix\n")
                yaml.dump(config, f, sort_keys=False)

        # Generate setup scripts for existing-secret permutations
        permutations = json.loads(
            self.run_cmd(
                ["nix", "eval", "--json", "--file", self.config_file, "permutations"],
                capture_output=True,
            ).stdout
        )

        # Generate test-config.yaml
        self._generate_test_config(creds, permutations)

        self.log(f"✅ All test files generated in: {TEST_VALUES_DIR}")

    def _create_external_secret(self, name: str, creds: Dict[str, Any], db_type: str):
        """Create external secret for a deployment directly using kubectl."""
        namespace = f"ncps-{name}"

        # Create namespace
        self.run_cmd(
            [
                "kubectl",
                "create",
                "namespace",
                namespace,
                "--dry-run=client",
                "-o",
                "yaml",
            ],
            capture_output=True,
            check=False,
        )
        self.run_cmd(
            ["kubectl", "apply", "-f", "-"],
            input=self.run_cmd(
                [
                    "kubectl",
                    "create",
                    "namespace",
                    namespace,
                    "--dry-run=client",
                    "-o",
                    "yaml",
                ],
                capture_output=True,
            ).stdout,
        )

        # Build database URL with per-test database name
        db_name = f"ncps_{name.replace('-', '_')}"
        db_url = ""
        if db_type == "postgresql":
            p = creds["postgresql"]
            pass_enc = urllib.parse.quote(p["password"])
            db_url = f"postgresql://{p['username']}:{pass_enc}@{p['host']}:{p['port']}/{db_name}?sslmode=disable"
        elif db_type == "mysql":
            m = creds["mariadb"]
            pass_enc = urllib.parse.quote(m["password"])
            db_url = (
                f"mysql://{m['username']}:{pass_enc}@{m['host']}:{m['port']}/{db_name}"
            )

        # Create secret
        secret_yaml = self.run_cmd(
            [
                "kubectl",
                "create",
                "secret",
                "generic",
                "ncps-external-secrets",
                "--namespace",
                namespace,
                "--from-literal=access-key-id=" + creds["s3"]["access_key"],
                "--from-literal=secret-access-key=" + creds["s3"]["secret_key"],
                "--from-literal=database-url=" + db_url,
                "--dry-run=client",
                "-o",
                "yaml",
            ],
            capture_output=True,
        ).stdout

        self.run_cmd(["kubectl", "apply", "-f", "-"], input=secret_yaml)

    def _generate_test_config(self, creds, permutations):
        test_data_hashes = json.loads(
            self.run_cmd(
                [
                    "nix",
                    "eval",
                    "--json",
                    "--file",
                    self.config_file,
                    "testData.narinfo_hashes",
                ],
                capture_output=True,
            ).stdout
        )

        test_config = {
            "cluster": creds,
            "test_data": {"narinfo_hashes": test_data_hashes},
            "deployments": [],
        }

        for perm in permutations:
            test_config["deployments"].append(
                {
                    "name": perm["name"],
                    "namespace": f"ncps-{perm['name']}",
                    "service_name": f"ncps-{perm['name']}",
                    "replicas": perm["replicas"],
                    "mode": "ha" if perm["replicas"] > 1 else "single",
                    "cdc": "cdc" in perm.get("features", []),
                    "migration": {
                        "mode": perm.get("migration", {}).get("mode", "initContainer")
                    },
                    "database": {
                        "type": perm["database"]["type"],
                        "path": perm["database"].get("sqlite", {}).get("path"),
                    },
                    "storage": {
                        "type": perm["storage"]["type"],
                        "path": perm["storage"].get("local", {}).get("path"),
                    },
                }
            )

        with open(os.path.join(TEST_VALUES_DIR, "test-config.yaml"), "w") as f:
            f.write("# NCPS Test Configuration\n# Auto-generated by k8s-tests\n")
            yaml.dump(test_config, f, sort_keys=False)

    # --- Deployment Management ---

    def cmd_install(self, name: Optional[str]):
        if not os.path.exists(TEST_VALUES_DIR):
            self.error("Test values not generated. Run 'k8s-tests generate' first.")

        # Load permutations to check which deployments need external secrets
        permutations = json.loads(
            self.run_cmd(
                ["nix", "eval", "--json", "--file", self.config_file, "permutations"],
                capture_output=True,
            ).stdout
        )
        perm_map = {p["name"]: p for p in permutations}
        creds = self.get_cluster_creds()

        names = (
            [name]
            if name
            else [
                f[:-5]
                for f in os.listdir(TEST_VALUES_DIR)
                if f.endswith(".yaml") and f not in ["test-config.yaml"]
            ]
        )

        for n in sorted(names):
            self.log(f"📦 Installing ncps-{n}...")

            # Create external secret if needed
            if n in perm_map and perm_map[n].get("setupScript"):
                db_type = perm_map[n]["database"]["type"]
                self._create_external_secret(n, creds, db_type)

            values_file = os.path.join(TEST_VALUES_DIR, f"{n}.yaml")
            self.run_cmd(
                [
                    "helm",
                    "upgrade",
                    "--install",
                    f"ncps-{n}",
                    CHART_DIR,
                    "-f",
                    values_file,
                    "--create-namespace",
                    "--namespace",
                    f"ncps-{n}",
                ]
            )

        self.log("✅ All deployments installed")

    def _cleanup_databases(self, perm_names: list[str]):
        """Drop per-test databases after cleanup."""
        if not perm_names:
            return

        creds = self.get_cluster_creds()

        # Load permutations from Nix config
        permutations = json.loads(
            self.run_cmd(
                ["nix", "eval", "--json", "--file", self.config_file, "permutations"],
                capture_output=True,
            ).stdout
        )

        # PostgreSQL cleanup
        pg_databases = []
        for name in perm_names:
            # Check if this permutation uses PostgreSQL
            for perm in permutations:
                if (
                    perm["name"] == name
                    and perm.get("database", {}).get("type") == "postgresql"
                ):
                    db_name = f"ncps_{name.replace('-', '_')}"
                    pg_databases.append(db_name)
                    break

        for db_name in sorted(set(pg_databases)):
            try:
                self.log(f"   - Dropping PostgreSQL database: {db_name}")
                self.run_cmd(
                    [
                        "kubectl",
                        "exec",
                        "-n",
                        "data",
                        "pg17-ncps-1",
                        "--",
                        "psql",
                        "-U",
                        "postgres",
                        "-c",
                        f"DROP DATABASE IF EXISTS {db_name};",
                    ],
                    check=False,
                )
            except Exception as e:
                self.log(f"   ⚠️  Failed to drop PostgreSQL database {db_name}: {e}")

        # MariaDB cleanup
        mariadb_databases = []
        for name in perm_names:
            # Check if this permutation uses MariaDB
            for perm in permutations:
                if (
                    perm["name"] == name
                    and perm.get("database", {}).get("type") == "mysql"
                ):
                    db_name = f"ncps_{name.replace('-', '_')}"
                    mariadb_databases.append(db_name)
                    break

        if mariadb_databases:
            try:
                mariadb_root_password_b64 = self.run_cmd(
                    [
                        "kubectl",
                        "get",
                        "secret",
                        "-n",
                        "data",
                        "mariadb-root-password",
                        "-o",
                        "jsonpath={.data.password}",
                    ],
                    capture_output=True,
                ).stdout
                mariadb_root_password = base64.b64decode(
                    mariadb_root_password_b64
                ).decode()

                for db_name in sorted(set(mariadb_databases)):
                    try:
                        self.log(f"   - Dropping MariaDB database: {db_name}")
                        self.run_cmd(
                            [
                                "kubectl",
                                "exec",
                                "-n",
                                "data",
                                "mariadb-ncps-0",
                                "--",
                                "mariadb",
                                "-u",
                                "root",
                                f"-p{mariadb_root_password}",
                                "-e",
                                f"DROP DATABASE IF EXISTS {db_name};",
                            ],
                            check=False,
                        )
                    except Exception as e:
                        self.log(
                            f"   ⚠️  Failed to drop MariaDB database {db_name}: {e}"
                        )
            except Exception as e:
                self.log(f"   ⚠️  Failed to get MariaDB root password: {e}")

    def cmd_cleanup(self, name: Optional[str]):
        perm_names = []
        if name:
            self.log(f"🧹 Removing {name}...")
            perm_names = [name]
            self.run_cmd(
                ["helm", "uninstall", f"ncps-{name}", "-n", f"ncps-{name}"], check=False
            )
            self.run_cmd(
                ["kubectl", "delete", "namespace", f"ncps-{name}"], check=False
            )
        else:
            self.log("🧹 Cleaning up all test deployments...")
            ns_list = self.run_cmd(
                [
                    "kubectl",
                    "get",
                    "namespaces",
                    "-o",
                    "jsonpath={.items[*].metadata.name}",
                ],
                capture_output=True,
            ).stdout.split()
            for ns in ns_list:
                if ns.startswith("ncps-"):
                    self.log(f"  Removing {ns}...")
                    perm_name = ns.replace("ncps-", "")
                    perm_names.append(perm_name)
                    release = ns  # typically same
                    self.run_cmd(["helm", "uninstall", ns, "-n", ns], check=False)
                    self.run_cmd(["kubectl", "delete", "namespace", ns], check=False)

        # Cleanup databases
        if perm_names:
            self.log("🗑️  Cleaning up per-test databases...")
            self._cleanup_databases(perm_names)

        self.log("✅ Cleanup finished")

    # --- Testing (bridged from test-deployments.py logic) ---

    def cmd_test(self, name: Optional[str]):
        config_path = os.path.join(TEST_VALUES_DIR, "test-config.yaml")
        if not os.path.exists(config_path):
            self.error("test-config.yaml not found. Run 'k8s-tests generate' first.")

        # Import logic from test-deployments.py (re-implementing core here for self-containment)
        # For brevity, I'll use a simplified version or call the existing script if preferred,
        # but the goal is unification.
        # I'll include the NCPSTester class logic here.
        from k8s_tests_tester import (
            NCPSTester,  # We'll extract this to a helper or include it
        )

        tester = NCPSTester(config_path, verbose=self.verbose)
        results = tester.test_all_deployments(deployment_filter=name)
        if not results:
            self.error("No deployments tested")
        success = tester.print_summary(results)
        if not success:
            sys.exit(1)

    def cmd_all(self):
        self.log("🚀 Running complete workflow...")
        self.cmd_cluster_create()
        self.cmd_generate(
            push=True,
            last=False,
            tag=None,
            registry="localhost:30000",
            repository="ncps",
        )
        self.cmd_install(name=None)
        self.cmd_test(name=None)
        self.log("✅ Complete workflow finished.")


# Main entry point
def main():
    parser = argparse.ArgumentParser(description="NCPS Kubernetes Integration Testing")
    parser.set_defaults(func=lambda _: parser.print_help())
    subparsers = parser.add_subparsers(dest="command")

    # Cluster
    cluster_parser = subparsers.add_parser("cluster")
    cluster_sub = cluster_parser.add_subparsers(dest="subcommand")
    cluster_sub.add_parser("create").set_defaults(
        func=lambda cli, _: cli.cmd_cluster_create()
    )
    cluster_sub.add_parser("destroy").set_defaults(
        func=lambda cli, _: cli.cmd_cluster_destroy()
    )
    info_p = cluster_sub.add_parser("info")
    info_p.add_argument("--json", action="store_true")
    info_p.set_defaults(func=lambda cli, args: cli.cmd_cluster_info(args.json))

    # Generate
    gen_p = subparsers.add_parser("generate")
    gen_p.add_argument("--push", action="store_true")
    gen_p.add_argument("--last", action="store_true")
    gen_p.add_argument("tag", nargs="?")
    gen_p.add_argument("registry", nargs="?", default="127.0.0.1:30000")
    gen_p.add_argument("repository", nargs="?", default="ncps")
    gen_p.set_defaults(
        func=lambda cli, args: cli.cmd_generate(
            args.push, args.last, args.tag, args.registry, args.repository
        )
    )

    # Install
    inst_p = subparsers.add_parser("install")
    inst_p.add_argument("name", nargs="?")
    inst_p.set_defaults(func=lambda cli, args: cli.cmd_install(args.name))

    # Test
    test_p = subparsers.add_parser("test")
    test_p.add_argument("name", nargs="?")
    test_p.add_argument("-v", "--verbose", action="store_true")
    test_p.set_defaults(func=lambda cli, args: cli.cmd_test(args.name))

    # Cleanup
    clean_p = subparsers.add_parser("cleanup")
    clean_p.add_argument("name", nargs="?")
    clean_p.set_defaults(func=lambda cli, args: cli.cmd_cleanup(args.name))

    # All
    all_p = subparsers.add_parser("all")
    all_p.set_defaults(func=lambda cli, _: cli.cmd_all())

    args = parser.parse_args()
    cli = K8sTestsCLI(verbose=getattr(args, "verbose", False))
    if hasattr(args, "func"):
        args.func(cli, args)
    else:
        parser.print_help()


if __name__ == "__main__":
    main()
