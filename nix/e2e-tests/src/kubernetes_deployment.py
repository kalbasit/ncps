"""``KubernetesDeployment`` — the kubernetes substrate for the phase drivers.

Implements the same :class:`deployment.Deployment` protocol that
``LocalDeployment`` provides, so the feature phases (``serve``,
``cdc-lifecycle``, ``staging-contention``) run **unchanged** on a Kind cluster.
It delegates cluster/Helm lifecycle to the proven :class:`k8s_tests.K8sTestsCLI`
and reaches each ncps replica through a per-pod ``kubectl port-forward``.

Seam mapping (see openspec change ``e2e-kubernetes-deployment-adapter``):

* ``provision`` -> ``cmd_cluster_create`` + ``cmd_generate`` + ``cmd_install``.
* ``replica_urls`` -> one ``kubectl port-forward pod/<name>`` per replica, and a
  ``var/ncps/state.json`` written in run.py's shape so ``seed_cache`` (which
  reads that file) builds through the cluster ncps.
* ``restart``/``start``/``clean_restart`` -> ``helm upgrade`` toggling
  ``config.cdc.enabled`` / ``lazyChunkingEnabled`` + ``kubectl rollout restart``.
* ``stop`` -> scale the workload to zero (drain prep).
* ``run_subcommand`` -> ``kubectl exec`` the **shell-less** ncps binary directly
  (``/bin/ncps <subcmd>``) — the image has no shell, so the binary is the entry.
* ``db`` -> postgres/mysql via an adapter-owned port-forward; sqlite via a
  ``kubectl debug`` ephemeral container that shares the ncps container's PID
  namespace and reads the live DB file at ``/proc/1/root`` (the image is
  shell-less, so the file cannot be read from the ncps container itself).
* ``logs`` -> ``kubectl logs`` of replica *i*.

Every seam is injectable (``runner``, ``popen``, ``port_allocator``,
``state_writer``, ``prober``, ``sleep``, ``cli``) so the command construction is
unit-testable offline with fakes, without a real cluster.
"""

from __future__ import annotations

import json
import os
import socket
import subprocess
import time
import urllib.request
from typing import Callable, List, Optional, Tuple

from client import Client
from db import DBAccess
from harness_config import REPO_ROOT, STATE_FILE, G, R, log

# Chart + generated per-scenario values live under the repo (mirrors the
# constants in k8s_tests, derived here to avoid importing that module — and its
# heavy deps — on the offline unit-test path).
CHART_DIR = os.path.join(REPO_ROOT, "charts/ncps")
TEST_VALUES_DIR = os.path.join(REPO_ROOT, "charts/ncps/test-values")

# The ncps container listens here (chart ExposedPorts / values httpPort).
NCPS_CONTAINER_PORT = 8501
# Chart container name and instance-selector label (charts/ncps/_helpers.tpl).
NCPS_CONTAINER = "ncps"
INSTANCE_LABEL = "app.kubernetes.io/instance"
# Image used for the sqlite debug sidecar; has the sqlite3 + coreutils binaries.
SQLITE_DEBUG_IMAGE = "nouchka/sqlite3:latest"
DATA_NAMESPACE = "data"


def _default_runner(
    args: List[str], *, timeout: int = 180, check: bool = True, input: Optional[str] = None
) -> Tuple[int, str]:
    """Run a command, returning ``(returncode, combined_output)``."""
    r = subprocess.run(
        args, capture_output=True, text=True, timeout=timeout, input=input
    )
    out = (r.stdout or "") + (r.stderr or "")
    if check and r.returncode != 0:
        raise RuntimeError(f"command failed ({r.returncode}): {' '.join(args)}\n{out}")
    return r.returncode, out


def _alloc_port() -> int:
    """Reserve an ephemeral local port for a port-forward."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _write_state_file(ports: List[int], *, locker: str, inflight_staging: bool) -> None:
    """Write run.py's ``state.json`` shape so ``seed_cache`` finds the caches."""
    os.makedirs(os.path.dirname(STATE_FILE), exist_ok=True)
    payload = {
        "locker": locker,
        "inflight_staging": inflight_staging,
        "instances": [{"port": p} for p in ports],
    }
    with open(STATE_FILE, "w", encoding="utf-8") as fp:
        json.dump(payload, fp)


def _http_ok(url: str) -> bool:
    try:
        with urllib.request.urlopen(url, timeout=2):
            return True
    except Exception:
        return False


class _KubeSqliteDB:
    """``DBAccess``-shaped sqlite reader backed by a ``kubectl debug`` sidecar.

    The ncps image is shell-less, so the sqlite file is read from a debug
    ephemeral container that shares the ncps container's PID namespace and sees
    its rootfs at ``/proc/1/root``. Each query copies the live DB (+ ``-wal`` /
    ``-shm``) into the sidecar's writable ``/tmp`` so WAL writes are visible,
    then runs ``sqlite3``.
    """

    dialect = "sqlite"

    def __init__(self, deployment: "KubernetesDeployment", db_path: str):
        self._d = deployment
        self._db_path = db_path

    def query(self, sql: str, params=()):  # noqa: ANN001 — matches DBAccess
        return self._d._sqlite_query(self._db_path, sql, tuple(params))

    def scalar(self, sql: str, params=()):  # noqa: ANN001 — matches DBAccess
        rows = self.query(sql, params)
        return rows[0][0] if rows else None


class KubernetesDeployment:
    """Run a scenario's phases on Kind via the shared ``Deployment`` protocol."""

    def __init__(
        self,
        scenario,
        *,
        cli=None,
        runner: Optional[Callable] = None,
        popen: Optional[Callable] = None,
        port_allocator: Optional[Callable[[], int]] = None,
        state_writer: Optional[Callable] = None,
        prober: Optional[Callable[[str], bool]] = None,
        sleep: Optional[Callable[[float], None]] = None,
        config_file: Optional[str] = None,
    ):
        self.scenario = scenario
        self.name = scenario.name
        self.namespace = f"ncps-{scenario.name}"
        self.release = f"ncps-{scenario.name}"
        self.replicas = max(1, scenario.replicas)
        self.database = scenario.database  # "sqlite" | "postgres" | "mysql"
        self.storage = scenario.storage
        # Multi-replica or staging-enabled scenarios use the redis locker.
        self.locker = "redis" if (scenario.staging or self.replicas > 1) else "local"
        # Mirror run.py: staging is only effective with a distributed locker.
        self.inflight_staging = bool(scenario.staging) and self.locker == "redis"
        self._cdc = scenario.cdc in ("eager", "lazy")
        self._lazy = scenario.cdc == "lazy"
        self._config_file = config_file or os.environ.get(
            "CONFIG_FILE",
            os.path.join(os.path.dirname(os.path.dirname(__file__)), "config.nix"),
        )

        # Injectable seams (defaults are the real implementations).
        self._runner = runner or _default_runner
        self._popen = popen or self._default_popen
        self._alloc = port_allocator or _alloc_port
        self._state_writer = state_writer or _write_state_file
        self._prober = prober or _http_ok
        self._sleep = sleep or time.sleep
        if cli is not None:
            self._cli = cli
        else:
            from k8s_tests import K8sTestsCLI

            self._cli = K8sTestsCLI(verbose=False)

        self._forwards: List[Tuple[object, int]] = []  # (proc, local_port)
        self._db_forward: Optional[Tuple[object, int]] = None
        self._sqlite_sidecar: Optional[Tuple[str, str]] = None  # (pod, container)
        # Last fully-resolved running pod spec — the one-shot drain pod is cloned
        # from this (a StatefulSet's PVC lives in volumeClaimTemplates, not the
        # template's `volumes`, so the bare template would miss the storage PVC).
        self._last_pod_spec: Optional[dict] = None

    # -- low-level helpers -----------------------------------------------------

    @staticmethod
    def _default_popen(args: List[str]):
        return subprocess.Popen(
            args, stdout=subprocess.PIPE, stderr=subprocess.PIPE
        )

    def _kubectl(self, *args: str, **kw) -> Tuple[int, str]:
        return self._runner(["kubectl", "-n", self.namespace, *args], **kw)

    def _pod_names(self) -> List[str]:
        rc, out = self._kubectl(
            "get",
            "pods",
            "-l",
            f"{INSTANCE_LABEL}={self.release}",
            "--field-selector=status.phase=Running",
            "-o",
            "jsonpath={.items[*].metadata.name}",
        )
        return sorted(n for n in out.split() if n)

    def _workload(self) -> str:
        """Return the controller kind/name (deployment or statefulset)."""
        rc, out = self._kubectl(
            "get",
            "deploy,statefulset",
            "-l",
            f"{INSTANCE_LABEL}={self.release}",
            "-o",
            "name",
        )
        names = [n for n in out.split() if n]
        if not names:
            raise RuntimeError(f"no deployment/statefulset for release {self.release}")
        return names[0]

    def _wait_ready(self, timeout: int = 240) -> None:
        workload = self._workload()
        self._kubectl(
            "rollout", "status", workload, "--timeout", f"{timeout}s", timeout=timeout + 30
        )

    # -- Deployment protocol ---------------------------------------------------

    def provision(self) -> None:
        log(f"kubernetes: provisioning {self.name} on Kind", G)
        self._cli.cmd_cluster_create()
        self._cli.cmd_generate(
            push=True, last=False, tag=None, registry="localhost:30000", repository="ncps"
        )
        self._cli.cmd_install(name=self.name)
        self._wait_ready()
        self._open_forwards()

    def _open_forwards(self) -> None:
        self._close_forwards()
        # A rollout/restart replaces the pod, so any sqlite debug sidecar from
        # the previous pod instance is gone — force it to be recreated lazily.
        self._sqlite_sidecar = None
        pods = self._pod_names()
        if not pods:
            raise RuntimeError(f"kubernetes: no running ncps pods for {self.release}")
        for pod in pods[: self.replicas]:
            local = self._alloc()
            proc = self._popen(
                [
                    "kubectl",
                    "-n",
                    self.namespace,
                    "port-forward",
                    f"pod/{pod}",
                    f"{local}:{NCPS_CONTAINER_PORT}",
                ]
            )
            self._forwards.append((proc, local))
        # Wait until every forward answers /nix-cache-info.
        deadline = time.time() + 60
        pending = {p for _, p in self._forwards}
        while pending and time.time() < deadline:
            for port in list(pending):
                if self._prober(f"http://127.0.0.1:{port}/nix-cache-info"):
                    pending.discard(port)
            if pending:
                self._sleep(0.5)
        if pending:
            raise RuntimeError(f"kubernetes: port-forwards not ready: {sorted(pending)}")
        # Capture the resolved pod spec (with real PVC volumes) for the one-shot
        # drain pod later, while a pod still exists.
        rc, raw = self._kubectl("get", "pod", pods[0], "-o", "json", check=False)
        try:
            self._last_pod_spec = json.loads(raw)["spec"]
        except (ValueError, KeyError):
            self._last_pod_spec = None
        # seed_cache reads state.json for the cache URLs.
        self._state_writer(
            [p for _, p in self._forwards],
            locker=self.locker,
            inflight_staging=self.inflight_staging,
        )

    def _close_forwards(self) -> None:
        for proc, _ in self._forwards:
            try:
                proc.terminate()
            except Exception:
                pass
        self._forwards = []

    def replica_urls(self) -> List[str]:
        return [f"http://127.0.0.1:{p}" for _, p in self._forwards]

    def client(self, replica: int = 0) -> Client:
        return Client(self.replica_urls()[replica])

    def read_state(self) -> dict:
        return {
            "locker": self.locker,
            "inflight_staging": self.inflight_staging,
            "instances": [{"port": p} for _, p in self._forwards],
        }

    def _helm_upgrade(self, *, cdc: bool, lazy: bool) -> None:
        values_file = os.path.join(TEST_VALUES_DIR, f"{self.name}.yaml")
        self._runner(
            [
                "helm",
                "upgrade",
                "--install",
                self.release,
                CHART_DIR,
                "-f",
                values_file,
                "--namespace",
                self.namespace,
                "--set",
                f"config.cdc.enabled={'true' if (cdc or lazy) else 'false'}",
                "--set",
                f"config.cdc.lazyChunkingEnabled={'true' if lazy else 'false'}",
                "--wait",
            ],
            timeout=300,
        )

    def _rollout_restart(self) -> None:
        self._kubectl("rollout", "restart", self._workload())
        self._wait_ready()

    def restart(self, *, cdc: bool = False, lazy: bool = False) -> None:
        self._cdc, self._lazy = cdc or lazy, lazy
        self._helm_upgrade(cdc=cdc, lazy=lazy)
        self._rollout_restart()
        self._open_forwards()

    def start(self, *, cdc: bool = False, lazy: bool = False) -> None:
        self._cdc, self._lazy = cdc or lazy, lazy
        self._kubectl("scale", self._workload(), f"--replicas={self.replicas}")
        self._helm_upgrade(cdc=cdc, lazy=lazy)
        self._wait_ready()
        self._open_forwards()

    def clean_restart(self, *, cdc: bool = False, lazy: bool = False) -> None:
        """Wipe cache state so a NAR is uncached again, then restart."""
        self._close_forwards()
        # Reinstall wipes the PVC-backed local/sqlite state; for s3+pg the
        # cli owns bucket/db cleanup between windows.
        self._cli.cmd_cleanup(self.name)
        self._cli.cmd_install(name=self.name)
        self._cdc, self._lazy = cdc or lazy, lazy
        self._helm_upgrade(cdc=cdc, lazy=lazy)
        self._wait_ready()
        self._open_forwards()

    def stop(self) -> None:
        self._close_forwards()
        self._sqlite_sidecar = None  # pod is going away
        self._kubectl("scale", self._workload(), "--replicas=0")
        # Wait for pods to disappear.
        deadline = time.time() + 120
        while time.time() < deadline:
            if not self._pod_names():
                return
            self._sleep(1)

    def run_subcommand(self, subcmd: str, extra=None, timeout: int = 600) -> Tuple[int, str]:
        # The image is shell-less: exec the binary directly. A stopped (scaled
        # to 0) workload has no pod, so run the subcommand as a one-shot pod
        # cloned from the workload's pod template — preserving the storage PVC,
        # config and DB env that migrate-chunks-to-nar / fsck need.
        args = ["/bin/ncps", "--config", "/etc/ncps/config.yaml", subcmd]
        if extra:
            args += list(extra)
        pods = self._pod_names()
        if pods:
            rc, out = self._kubectl(
                "exec", pods[0], "-c", NCPS_CONTAINER, "--", *args,
                check=False, timeout=timeout,
            )
            return rc, out
        return self._run_oneshot(subcmd, args, timeout)

    def _run_oneshot(self, subcmd: str, args: List[str], timeout: int) -> Tuple[int, str]:
        """Run ``ncps <subcmd>`` in a one-shot Pod cloned from the last pod."""
        phase, logs = self._run_pod_from_spec(
            f"ncps-{subcmd.replace('_', '-')}-oneshot",
            command=args,
            timeout=timeout,
        )
        return (0 if phase == "Succeeded" else 1), logs

    def _run_pod_from_spec(
        self, pod_name: str, *, command: List[str], image: Optional[str] = None,
        timeout: int = 600,
    ) -> Tuple[str, str]:
        """Run a one-shot Pod cloned from the last running pod's resolved spec.

        Cloning the resolved *pod* spec (not the workload template) preserves a
        StatefulSet's per-pod storage PVC, so migrate/fsck and the stopped-window
        sqlite reader reach the same data. Returns ``(phase, logs)``.
        """
        if self._last_pod_spec is not None:
            spec = json.loads(json.dumps(self._last_pod_spec))  # deep copy
        else:
            rc, raw = self._kubectl(
                "get", self._workload(), "-o", "json", check=False
            )
            spec = json.loads(raw)["spec"]["template"]["spec"]
        for f in ("nodeName", "initContainers", "ephemeralContainers"):
            spec.pop(f, None)
        ncps = next(
            (c for c in spec.get("containers", []) if c.get("name") == NCPS_CONTAINER),
            spec["containers"][0],
        )
        ncps = dict(ncps)
        ncps["command"] = command
        ncps.pop("args", None)
        if image:
            ncps["image"] = image
        for probe in ("livenessProbe", "readinessProbe", "startupProbe"):
            ncps.pop(probe, None)
        spec["containers"] = [ncps]
        spec["restartPolicy"] = "Never"
        manifest = json.dumps(
            {
                "apiVersion": "v1",
                "kind": "Pod",
                "metadata": {"name": pod_name, "namespace": self.namespace},
                "spec": spec,
            }
        )
        # Replace any stale pod from a prior phase, then run to completion.
        self._kubectl("delete", "pod", pod_name, "--ignore-not-found", check=False)
        self._kubectl("apply", "-f", "-", input=manifest, check=False)
        self._kubectl(
            "wait", f"pod/{pod_name}",
            "--for=jsonpath={.status.phase}=Succeeded",
            f"--timeout={timeout}s", check=False, timeout=timeout + 30,
        )
        _, phase = self._kubectl(
            "get", "pod", pod_name, "-o", "jsonpath={.status.phase}", check=False
        )
        _, logs = self._kubectl("logs", pod_name, "--tail=-1", check=False)
        self._kubectl("delete", "pod", pod_name, "--ignore-not-found", check=False)
        return phase.strip(), logs

    def db(self) -> DBAccess:
        if self.database == "sqlite":
            return _KubeSqliteDB(self, self._sqlite_db_path())
        return DBAccess(self.database, self._db_url())

    def _sqlite_db_path(self) -> str:
        # config.nix sets sqlite.path = "/storage/db/ncps.db".
        return "/storage/db/ncps.db"

    def _db_url(self) -> str:
        from urllib.parse import quote

        creds = self._cli.get_cluster_creds()
        key = "postgresql" if self.database == "postgres" else "mariadb"
        c = creds[key]
        host = c["host"].split(".")[0]
        svc_ns = c["host"].split(".")[1] if "." in c["host"] else DATA_NAMESPACE
        if self._db_forward is None:
            local = self._alloc()
            proc = self._popen(
                [
                    "kubectl",
                    "-n",
                    svc_ns,
                    "port-forward",
                    f"svc/{host}",
                    f"{local}:{c['port']}",
                ]
            )
            self._db_forward = (proc, local)
            self._sleep(3)
        local = self._db_forward[1]
        db_name = f"ncps_{self.name.replace('-', '_')}"
        user = quote(c["username"])
        pw = quote(c["password"])
        scheme = "postgresql" if self.database == "postgres" else "mysql"
        return f"{scheme}://{user}:{pw}@127.0.0.1:{local}/{db_name}"

    def _ensure_sqlite_sidecar(self) -> str:
        """Create (once) a debug ephemeral container sharing ncps's PID ns."""
        if self._sqlite_sidecar:
            return self._sqlite_sidecar
        pods = self._pod_names()
        if not pods:
            raise RuntimeError("kubernetes: no ncps pod for sqlite debug sidecar")
        pod = pods[0]
        name = "sqlite-probe"
        # The sidecar must run as the same uid that owns the DB file, i.e. the
        # ncps container's effective uid. When the chart sets no securityContext
        # the ncps image runs as root (uid 0) and the file is root-owned, so the
        # sidecar must also be root; only a chart that pins runAsUser needs the
        # `--custom` securityContext override (which also satisfies a non-root
        # PSS namespace). Passing the override inline is not portable across
        # kubectl versions — it must be a file path.
        rc, uid = self._kubectl(
            "get", "pod", pod, "-o",
            "jsonpath={.spec.containers[0].securityContext.runAsUser}",
            check=False,
        )
        run_as = uid.strip()
        # `-c <name>` pins the ephemeral container name (else kubectl generates a
        # random one); the trailing `-- sleep infinity` keeps it alive so we can
        # exec queries into it.
        debug_cmd = [
            "debug", pod, "-c", name, "--image", SQLITE_DEBUG_IMAGE,
            "--target", NCPS_CONTAINER, "-q",
        ]
        custom_file = None
        if run_as:
            overrides = {"securityContext": {"runAsUser": int(run_as)}}
            import tempfile

            fd, custom_file = tempfile.mkstemp(suffix=".json")
            with os.fdopen(fd, "w") as fp:
                json.dump(overrides, fp)
            debug_cmd += ["--custom", custom_file]
        debug_cmd += ["--", "sleep", "infinity"]
        rc, out = self._kubectl(*debug_cmd, check=False, timeout=120)
        log(f"kubernetes: sqlite debug sidecar (runAsUser={run_as or 'image-default'}): rc={rc}", G)
        if custom_file:
            try:
                os.unlink(custom_file)
            except OSError:
                pass
        # Wait for the ephemeral container to be running.
        deadline = time.time() + 90
        while time.time() < deadline:
            rc, phase = self._kubectl(
                "get", "pod", pod, "-o",
                f'jsonpath={{.status.ephemeralContainerStatuses[?(@.name=="{name}")].state.running}}',
                check=False,
            )
            if phase.strip():
                break
            self._sleep(1)
        else:
            _, statuses = self._kubectl(
                "get", "pod", pod, "-o",
                "jsonpath={.status.ephemeralContainerStatuses}", check=False,
            )
            raise RuntimeError(f"kubernetes: sqlite debug sidecar not running: {statuses}")
        self._sqlite_sidecar = (pod, name)
        return self._sqlite_sidecar

    def _sqlite_query(self, db_path: str, sql: str, params: tuple):
        # Inline params as quoted literals (cdc-lifecycle uses string keys).
        final = sql
        for p in params:
            final = final.replace("?", "'" + str(p).replace("'", "''") + "'", 1)
        query = final.replace(chr(34), chr(39))
        if self._pod_names():
            rc, out = self._sqlite_via_sidecar(db_path, query)
        else:
            # Drain window: ncps is scaled to 0, so there is no pod to attach a
            # debug sidecar to. Read the now-released storage PVC directly from a
            # transient pod that mounts it (RWO is free while ncps is down).
            rc, out = self._sqlite_via_reader_pod(db_path, query)
        if rc != 0 or "Error:" in out or "Parse error" in out:
            raise RuntimeError(
                f"kubernetes: sqlite query failed (rc={rc}) on {db_path}: {out.strip()}\n"
                f"  SQL: {final}"
            )
        rows = []
        for line in out.splitlines():
            line = line.strip()
            if not line:
                continue
            rows.append(tuple(line.split("|")))
        # Coerce single numeric column to int for scalar() callers.
        coerced = []
        for r in rows:
            coerced.append(tuple(int(x) if x.lstrip("-").isdigit() else x for x in r))
        return coerced

    def _sqlite_via_sidecar(self, db_path: str, query: str) -> Tuple[int, str]:
        pod, sidecar = self._ensure_sqlite_sidecar()
        # Copy the live DB (+ wal/shm) so WAL writes are visible, then query.
        # The cp MUST succeed (a permission/owner mismatch here is the failure to
        # surface, not swallow — otherwise sqlite3 silently opens an empty DB and
        # every "no such table" error masquerades as a real row).
        script = (
            "set -e; "
            f"cp /proc/1/root{db_path} /tmp/q.db; "
            f"cp /proc/1/root{db_path}-wal /tmp/q.db-wal 2>/dev/null || true; "
            f"cp /proc/1/root{db_path}-shm /tmp/q.db-shm 2>/dev/null || true; "
            f'sqlite3 /tmp/q.db "{query}"'
        )
        return self._kubectl(
            "exec", pod, "-c", sidecar, "--", "sh", "-c", script, check=False
        )

    def _sqlite_via_reader_pod(self, db_path: str, query: str) -> Tuple[int, str]:
        # The PVC is mounted at /storage in the cloned pod, so read it directly.
        phase, logs = self._run_pod_from_spec(
            "ncps-sqlite-reader",
            image=SQLITE_DEBUG_IMAGE,
            command=["sh", "-c", f'sqlite3 {db_path} "{query}"'],
            timeout=120,
        )
        return (0 if phase == "Succeeded" else 1), logs

    def logs(self, replica: int = 0) -> str:
        pods = self._pod_names()
        if replica >= len(pods):
            return ""
        rc, out = self._kubectl(
            "logs", pods[replica], "-c", NCPS_CONTAINER, "--tail=-1", check=False
        )
        return out

    def teardown(self) -> None:
        self._close_forwards()
        if self._db_forward is not None:
            try:
                self._db_forward[0].terminate()
            except Exception:
                pass
            self._db_forward = None
        try:
            self._cli.cmd_cleanup(self.name)
        except Exception as e:  # noqa: BLE001 — cleanup is best-effort
            log(f"kubernetes: cleanup error for {self.name}: {e}", R)
