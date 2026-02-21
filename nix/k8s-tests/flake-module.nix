# NCPS Kubernetes Integration Testing Tool
# Nix package definition for k8s-tests CLI
_: {
  perSystem =
    { pkgs, ... }:
    {
      packages.k8s-tests = pkgs.writeShellApplication {
        name = "k8s-tests";
        runtimeInputs = with pkgs; [
          kubectl
          kubernetes-helm
          kind
          skopeo
          git
          docker
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
          export CONFIG_FILE="${./config.nix}"
          export PYTHONPATH="${./src}:''${PYTHONPATH:-}"
          exec python3 ${./src/k8s_tests.py} "$@"
        '';
      };
    };
}
