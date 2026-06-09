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
    in
    {
      packages.e2e = e2e;

      apps.e2e = {
        type = "app";
        program = "${e2e}/bin/e2e";
      };
    };
}
