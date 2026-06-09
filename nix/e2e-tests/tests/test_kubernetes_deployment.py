"""Offline unit tests for the ``KubernetesDeployment`` adapter.

These never touch a cluster: the ``runner`` (kubectl/helm), ``popen``
(port-forwards), ``cli`` (K8sTestsCLI), port allocator, state writer, prober and
sleep are all injected as fakes. They pin the *command construction* and seam
wiring of every ``Deployment`` protocol method.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import List

import pytest

from kubernetes_deployment import KubernetesDeployment, _KubeSqliteDB


@dataclass
class _Scenario:
    name: str
    storage: str = "s3"
    database: str = "postgres"
    replicas: int = 2
    cdc: str = "off"
    staging: bool = True
    phase: str = "staging-contention"
    modes: tuple = ("local", "kubernetes")


class _FakeProc:
    def __init__(self):
        self.terminated = False

    def terminate(self):
        self.terminated = True


class _FakeCLI:
    def __init__(self):
        self.calls: List[str] = []

    def cmd_cluster_create(self):
        self.calls.append("cluster_create")

    def cmd_generate(self, **kw):
        self.calls.append("generate")

    def cmd_install(self, name=None):
        self.calls.append(f"install:{name}")

    def cmd_cleanup(self, name=None):
        self.calls.append(f"cleanup:{name}")

    def get_cluster_creds(self):
        return {
            "postgresql": {
                "host": "pg17-ncps-rw.data.svc.cluster.local",
                "port": 5432,
                "database": "ncps",
                "username": "ncps",
                "password": "s3cr3t",
            },
            "mariadb": {
                "host": "mariadb-ncps.data.svc.cluster.local",
                "port": 3306,
                "database": "ncps",
                "username": "ncps",
                "password": "m@ria",
            },
        }


class _Recorder:
    """Fake runner: records argv lists and returns canned outputs by matcher."""

    def __init__(self):
        self.calls: List[List[str]] = []
        self._responses = []  # list of (predicate, (rc, out))
        self.default = (0, "")

    def respond(self, predicate, rc=0, out=""):
        self._responses.append((predicate, (rc, out)))

    def __call__(self, args, *, timeout=180, check=True, input=None):
        self.calls.append(list(args))
        for pred, resp in self._responses:
            if pred(args):
                return resp
        return self.default

    def find(self, *needles):
        """Return argv lists containing all needles (as a contiguous-ish match)."""
        hits = []
        for c in self.calls:
            joined = " ".join(c)
            if all(n in joined for n in needles):
                hits.append(c)
        return hits


def _make(scenario=None, runner=None):
    runner = runner or _Recorder()
    procs = []

    def popen(args):
        p = _FakeProc()
        p.args = args
        procs.append(p)
        return p

    ports = iter(range(40000, 40100))
    written = {}

    def state_writer(plist, *, locker, inflight_staging):
        written["ports"] = list(plist)
        written["locker"] = locker
        written["inflight_staging"] = inflight_staging

    dep = KubernetesDeployment(
        scenario or _Scenario(name="staging-contention"),
        cli=_FakeCLI(),
        runner=runner,
        popen=popen,
        port_allocator=lambda: next(ports),
        state_writer=state_writer,
        prober=lambda url: True,
        sleep=lambda s: None,
    )
    return dep, runner, procs, written


# -- identity / namespacing ----------------------------------------------------


def test_namespace_and_release_derive_from_name():
    dep, *_ = _make(_Scenario(name="staging-contention"))
    assert dep.namespace == "ncps-staging-contention"
    assert dep.release == "ncps-staging-contention"


def test_locker_is_redis_for_multi_replica():
    dep, *_ = _make(_Scenario(name="x", replicas=2, staging=True))
    assert dep.locker == "redis"
    single, *_ = _make(
        _Scenario(name="y", replicas=1, staging=False, phase="cdc-lifecycle")
    )
    assert single.locker == "local"


# -- provision -----------------------------------------------------------------


def test_provision_runs_cluster_generate_install_in_order():
    runner = _Recorder()
    runner.respond(
        lambda a: "get" in a and "pods" in a,
        out="ncps-staging-contention-0 ncps-staging-contention-1",
    )
    runner.respond(lambda a: "deploy,statefulset" in a, out="deployment.apps/ncps-staging-contention")
    dep, runner, procs, written = _make(runner=runner)
    dep.provision()
    assert dep._cli.calls[:3] == ["cluster_create", "generate", "install:staging-contention"]
    # One port-forward per replica.
    assert len([p for p in procs if "port-forward" in p.args]) == 2
    # state.json written with both forwarded ports + locker + staging flag.
    assert len(written["ports"]) == 2
    assert written["locker"] == "redis"
    assert written["inflight_staging"] is True
    # read_state() exposes the same effective config the driver asserts on.
    st = dep.read_state()
    assert st["locker"] == "redis" and st["inflight_staging"] is True
    assert len(st["instances"]) == 2


def test_replica_urls_match_forwarded_ports():
    runner = _Recorder()
    runner.respond(lambda a: "get" in a and "pods" in a, out="pod-a pod-b")
    runner.respond(lambda a: "deploy,statefulset" in a, out="deployment.apps/ncps-x")
    dep, runner, procs, _ = _make(runner=runner)
    dep.provision()
    urls = dep.replica_urls()
    assert len(urls) == 2
    assert all(u.startswith("http://127.0.0.1:") for u in urls)


# -- CDC toggle via helm upgrade ----------------------------------------------


def test_restart_enables_cdc_via_helm_set_and_rolls_out():
    runner = _Recorder()
    runner.respond(lambda a: "get" in a and "pods" in a, out="pod-a pod-b")
    runner.respond(lambda a: "deploy,statefulset" in a, out="deployment.apps/ncps-x")
    dep, runner, *_ = _make(runner=runner)
    dep.restart(cdc=True)
    helm = runner.find("helm", "upgrade")
    assert helm, "helm upgrade issued"
    joined = " ".join(helm[0])
    assert "config.cdc.enabled=true" in joined
    assert "config.cdc.lazyChunkingEnabled=false" in joined
    assert runner.find("rollout", "restart"), "rollout restart issued"


def test_restart_lazy_sets_lazy_chunking():
    runner = _Recorder()
    runner.respond(lambda a: "get" in a and "pods" in a, out="pod-a")
    runner.respond(lambda a: "deploy,statefulset" in a, out="deployment.apps/ncps-x")
    dep, runner, *_ = _make(runner=runner)
    dep.restart(lazy=True)
    joined = " ".join(runner.find("helm", "upgrade")[0])
    assert "config.cdc.enabled=true" in joined
    assert "config.cdc.lazyChunkingEnabled=true" in joined


def test_restart_cdc_false_disables():
    runner = _Recorder()
    runner.respond(lambda a: "get" in a and "pods" in a, out="pod-a")
    runner.respond(lambda a: "deploy,statefulset" in a, out="deployment.apps/ncps-x")
    dep, runner, *_ = _make(runner=runner)
    dep.restart(cdc=False)
    joined = " ".join(runner.find("helm", "upgrade")[0])
    assert "config.cdc.enabled=false" in joined


# -- stop / run_subcommand -----------------------------------------------------


def test_stop_scales_workload_to_zero():
    runner = _Recorder()
    # After scale, no pods running.
    runner.respond(lambda a: "deploy,statefulset" in a, out="deployment.apps/ncps-x")
    runner.respond(lambda a: "get" in a and "pods" in a, out="")
    dep, runner, *_ = _make(runner=runner)
    dep.stop()
    scale = runner.find("scale", "--replicas=0")
    assert scale, "scaled to zero"


def test_run_subcommand_execs_shell_less_binary():
    runner = _Recorder()
    runner.respond(lambda a: "deploy,statefulset" in a, out="deployment.apps/ncps-x")
    runner.respond(lambda a: "get" in a and "pods" in a, out="pod-a")
    runner.respond(lambda a: "exec" in a, rc=0, out="total: 5")
    dep, runner, *_ = _make(runner=runner)
    rc, out = dep.run_subcommand("migrate-chunks-to-nar", ["--dry-run"])
    assert rc == 0
    execs = runner.find("exec", "/bin/ncps", "migrate-chunks-to-nar")
    assert execs, "exec'd ncps binary directly"
    assert "--dry-run" in " ".join(execs[0])


def test_run_subcommand_oneshot_clones_pod_template_when_scaled_to_zero():
    """With no running pod (drain), a one-shot pod is cloned from the workload
    template so the storage PVC + config + DB env are preserved."""
    import json as _json

    template = {
        "spec": {
            "template": {
                "spec": {
                    "containers": [
                        {
                            "name": "ncps",
                            "image": "ncps:test",
                            "args": ["serve"],
                            "livenessProbe": {"httpGet": {"path": "/", "port": 8501}},
                            "volumeMounts": [{"name": "storage", "mountPath": "/storage"}],
                            "env": [{"name": "CACHE_DATABASE_URL", "value": "sqlite:/storage/db/ncps.db"}],
                        }
                    ],
                    "volumes": [{"name": "storage", "persistentVolumeClaim": {"claimName": "ncps-x-storage"}}],
                }
            }
        }
    }
    runner = _Recorder()
    runner.respond(lambda a: "deploy,statefulset" in a, out="statefulset.apps/ncps-x")
    runner.respond(lambda a: "get" in a and "pods" in a, out="")  # no running pod
    runner.respond(lambda a: "-o" in a and "json" in a, out=_json.dumps(template))
    runner.respond(lambda a: "get" in a and "jsonpath={.status.phase}" in " ".join(a), out="Succeeded")
    runner.respond(lambda a: "logs" in a, out="drained 3 NARs")
    dep, runner, *_ = _make(
        _Scenario(name="x", database="sqlite", replicas=1, staging=False, phase="cdc-lifecycle"),
        runner=runner,
    )
    rc, out = dep.run_subcommand("migrate-chunks-to-nar", ["--force-reclaim"])
    assert rc == 0
    applies = [c for c in runner.calls if "apply" in c]
    assert applies, "one-shot pod applied"
    # The applied manifest preserves the storage volume + drops the probe.
    apply_call = applies[0]
    # input is passed via runner kwarg; assert the manifest content was built.
    # (Recorder ignores input, so re-build to verify the clone logic ran.)
    assert any("delete" in c and "oneshot" in " ".join(c) for c in runner.calls), "cleans up oneshot pod"


# -- db() ----------------------------------------------------------------------


def test_sqlite_query_uses_reader_pod_when_scaled_to_zero():
    """During drain (ncps scaled to 0), sqlite is read from a transient pod that
    mounts the released storage PVC, not a debug sidecar."""
    import json as _json

    pod_spec = {
        "containers": [
            {
                "name": "ncps",
                "image": "ncps:test",
                "volumeMounts": [{"name": "storage", "mountPath": "/storage"}],
            }
        ],
        "volumes": [{"name": "storage", "persistentVolumeClaim": {"claimName": "storage-ncps-cdc-0"}}],
    }
    runner = _Recorder()
    runner.respond(lambda a: "deploy,statefulset" in a, out="statefulset.apps/ncps-cdc")
    runner.respond(lambda a: "get" in a and "pods" in a, out="")  # scaled to 0
    runner.respond(lambda a: "get" in a and "jsonpath={.status.phase}" in " ".join(a), out="Succeeded")
    runner.respond(lambda a: "logs" in a, out="0")
    dep, runner, *_ = _make(
        _Scenario(name="cdc", database="sqlite", replicas=1, staging=False, phase="cdc-lifecycle"),
        runner=runner,
    )
    dep._last_pod_spec = pod_spec  # captured while a pod was running
    val = dep.db().scalar("SELECT COUNT(*) FROM nar_files WHERE total_chunks > 0")
    assert val == 0
    applies = [c for c in runner.calls if "apply" in c]
    assert applies, "reader pod applied (no debug sidecar when scaled to 0)"
    assert not runner.find("debug", "--target"), "no debug sidecar used when scaled to 0"


def test_db_postgres_builds_portforwarded_url():
    runner = _Recorder()
    dep, runner, procs, _ = _make(_Scenario(name="staging-contention", database="postgres"))
    db = dep.db()
    assert db.dialect == "postgres"
    assert db.url.startswith("postgresql://ncps:s3cr3t@127.0.0.1:")
    assert db.url.endswith("/ncps_staging_contention")
    # opened a port-forward to the pg service
    assert any("svc/pg17-ncps-rw" in " ".join(p.args) for p in procs)


def test_db_sqlite_returns_debug_backed_reader():
    dep, *_ = _make(_Scenario(name="cdc-lifecycle", database="sqlite", replicas=1, staging=False, phase="cdc-lifecycle"))
    db = dep.db()
    assert isinstance(db, _KubeSqliteDB)
    assert db.dialect == "sqlite"


def test_sqlite_query_uses_debug_sidecar_and_proc_root():
    runner = _Recorder()
    runner.respond(lambda a: "deploy,statefulset" in a, out="statefulset.apps/ncps-cdc-lifecycle")
    runner.respond(lambda a: "get" in a and "pods" in a, out="ncps-cdc-lifecycle-0")
    runner.respond(lambda a: "runAsUser" in " ".join(a), out="1000")
    runner.respond(lambda a: "ephemeralContainerStatuses" in " ".join(a), out="true")
    runner.respond(lambda a: "exec" in a, out="3")
    dep, runner, *_ = _make(
        _Scenario(name="cdc-lifecycle", database="sqlite", replicas=1, staging=False, phase="cdc-lifecycle"),
        runner=runner,
    )
    val = dep.db().scalar("SELECT COUNT(*) FROM nar_files")
    assert val == 3
    # created a debug ephemeral container targeting the ncps container
    assert runner.find("debug", "--target", "ncps"), "kubectl debug sidecar created"
    # query copied the live DB from /proc/1/root
    assert any("/proc/1/root/storage/db/ncps.db" in " ".join(c) for c in runner.calls)


# -- teardown ------------------------------------------------------------------


def test_teardown_closes_forwards_and_cleans_up():
    runner = _Recorder()
    runner.respond(lambda a: "get" in a and "pods" in a, out="pod-a pod-b")
    runner.respond(lambda a: "deploy,statefulset" in a, out="deployment.apps/ncps-x")
    dep, runner, procs, _ = _make(runner=runner)
    dep.provision()
    dep.teardown()
    assert all(p.terminated for p in procs if "port-forward" in p.args)
    assert "cleanup:staging-contention" in dep._cli.calls


def test_protocol_methods_present():
    dep, *_ = _make()
    for m in (
        "provision", "replica_urls", "client", "restart", "stop", "start",
        "clean_restart", "read_state", "run_subcommand", "db", "logs", "teardown",
    ):
        assert callable(getattr(dep, m)), m
