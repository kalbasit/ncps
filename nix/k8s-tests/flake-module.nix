# NCPS Kubernetes Integration Testing Tool
# Nix package definition for k8s-tests CLI
_: {
  perSystem =
    { pkgs, ... }:
    {
      packages.k8s-tests = pkgs.writeShellApplication {
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
        ];

        # Main script with path substitutions
        text = ''
          # Substitute paths at build time
          export CONFIG_FILE="${./config.nix}"
          export LIB_FILE="${./src/lib.sh}"
          export CLUSTER_SCRIPT="${./src/cluster.sh}"

          # Source and run main script
          ${builtins.readFile ./src/k8s-tests.sh}
        '';
      };
    };
}
