# Unified ncps e2e test harness.
#
# One scenario-driven harness that runs a declarative scenario catalog against
# either a local `dev-scripts/run.py` deployment (`--mode local`) or a
# Kind/Helm Kubernetes deployment (`--mode kubernetes`). It replaces the former
# `nix/k8s-tests` CLI and the standalone `dev-scripts/test-*-e2e.py` drivers.
#
# The scenario catalog is the existing `config.nix` (materialized as JSON via
# `nix eval`), so there is a single source of truth shared by both modes.
_: {
  perSystem =
    { pkgs, ... }:
    let
      # The scenario catalog (single source of truth for both modes).
      configFile = ./config.nix;

      e2e = pkgs.writeShellApplication {
        name = "e2e";
        runtimeInputs = with pkgs; [
          # Kubernetes-mode tooling.
          kubectl
          kubernetes-helm
          kind
          skopeo
          docker
          # Shared.
          git
          nix # `nix eval` (catalog) and `nix run .#deps` (local-mode backends)
          (pkgs.python3.withPackages (
            ps: with ps; [
              boto3
              brotli
              kubernetes
              psycopg2
              pymysql
              pyyaml
              requests
              zstandard
            ]
          ))
        ];
        text = ''
          export CONFIG_FILE="${configFile}"
          export PYTHONPATH="${./src}:''${PYTHONPATH:-}"
          exec python3 ${./src/cli.py} "$@"
        '';
      };
      # Fast, offline unit tests for the harness CLI / runner / catalog logic.
      # Excludes the `catalog` marker (those materialize config.nix via `nix
      # eval`) and never touches the network or a cluster, so it is cheap enough
      # to run in `nix flake check` even though the harness scenarios stay out.
      pytestPython = pkgs.python3.withPackages (ps: with ps; [ pytest ]);
    in
    {
      packages.e2e = e2e;

      apps.e2e = {
        type = "app";
        program = "${e2e}/bin/e2e";
      };

      checks.e2e-harness-unit = pkgs.runCommandLocal "e2e-harness-unit-tests" { } ''
        cp -r ${./src} src
        cp -r ${./tests} tests
        cp ${./pytest.ini} pytest.ini
        export PYTHONDONTWRITEBYTECODE=1
        ${pytestPython}/bin/python -m pytest tests -m "not catalog" -q
        touch $out
      '';
    };
}
