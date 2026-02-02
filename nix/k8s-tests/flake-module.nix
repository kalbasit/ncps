# NCPS Kubernetes Integration Testing Tool
# Nix package definition for k8s-tests CLI
_: {
  perSystem =
    { pkgs, ... }:
    {
      packages.k8s-tests =
        let
          test-deployments-bin = pkgs.writers.writePython3Bin "test-deployments" {
            libraries = with pkgs.python3Packages; [
              boto3
              kubernetes
              psycopg2-binary
              pymysql
              pyyaml
              requests
            ];
            doCheck = false;
          } (builtins.readFile ./src/test-deployments.py);
        in
        pkgs.writeShellApplication {
          name = "k8s-tests";

          # Runtime dependencies
          runtimeInputs = with pkgs; [
            jq # JSON parsing and manipulation
            yq-go # YAML processing
            kubectl # Kubernetes CLI
            kubernetes-helm # Helm CLI (v3)
            kind # Kind CLI
            skopeo # Image operations
            git # Repository operations
            docker # Docker CLI (for image loading)
            test-deployments-bin
          ];

          # Main script with path substitutions
          text = ''
            # Substitute paths at build time
            export CONFIG_FILE="${./config.nix}"

            export CLUSTER_SCRIPT="${./src/cluster.sh}"

            export TEST_DEPLOYMENTS_EXE="${test-deployments-bin}/bin/test-deployments"

            # Source and run main script
            ${builtins.readFile ./src/k8s-tests.sh}
          '';
        };
    };
}
