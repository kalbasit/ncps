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
              requests
              pyyaml
              psycopg2
              pymysql
              kubernetes
              boto3
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
