{ inputs, ... }:
{
  imports = [
    inputs.process-compose-flake.flakeModule
  ];
  perSystem =
    { pkgs, ... }:
    {
      # This creates the 'nix run' command
      process-compose.deps = {
        settings = {
          processes = {
            minio-server = {
              command = ''
                DATA_DIR=$(mktemp -d)
                echo "Storing ephemeral data in $DATA_DIR"
                ${pkgs.minio}/bin/minio server $DATA_DIR --console-address ":9001"
              '';
              environment = {
                MINIO_ROOT_USER = "admin";
                MINIO_ROOT_PASSWORD = "password";
                MINIO_REGION = "us-east-1";
              };
              readiness_probe = {
                http_get = {
                  host = "127.0.0.1";
                  port = 9000;
                  path = "/minio/health/live";
                };
                initial_delay_seconds = 2;
                period_seconds = 5;
              };
            };
            create-buckets = {
              command =
                let
                  initMinio = pkgs.writeShellApplication {
                    name = "init-minio";
                    runtimeInputs = [
                      pkgs.minio-client
                      pkgs.curl
                      pkgs.jq
                    ];
                    text = builtins.readFile ./init-minio.sh;
                  };
                in
                "${initMinio}/bin/init-minio";
              depends_on.minio-server.condition = "process_healthy";
            };
          };
        };
      };
    };
}
