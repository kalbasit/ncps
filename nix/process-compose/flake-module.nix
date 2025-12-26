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
            # 1. The MinIO Server
            minio-server = {
              command = ''
                # Create a temp dir for this session
                DATA_DIR=$(mktemp -d)
                echo "Storing ephemeral data in $DATA_DIR"

                # Run MinIO
                ${pkgs.minio}/bin/minio server $DATA_DIR --console-address ":9001"
              '';
              environment = {
                MINIO_ROOT_USER = "admin";
                MINIO_ROOT_PASSWORD = "password";
              };
              # Availability check (healthcheck)
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

            # 2. The Setup Script (creates bucket)
            create-buckets = {
              command = ''
                set -e
                # 1. Setup alias for the admin user
                ${pkgs.minio-client}/bin/mc alias set local http://127.0.0.1:9000 admin password

                # 2. Create bucket and set public
                ${pkgs.minio-client}/bin/mc mb local/test-bucket || true
                ${pkgs.minio-client}/bin/mc anonymous set public local/test-bucket

                # 3. Create a Service Account (Access Key & Secret)
                # We use 'admin' policy here for full access, but you can restrict it if needed.
                # The '|| true' ignores errors if it already exists (though unrelated in ephemeral mode).
                ${pkgs.minio-client}/bin/mc admin user svcacct add \
                  --access-key "test-access-key" \
                  --secret-key "test-secret-key" \
                  local admin || true

                echo "=================================================="
                echo "✅ MinIO Setup Complete"
                echo "   Endpoint:   http://127.0.0.1:9000"
                echo "   Bucket:     test-bucket"
                echo "   Access Key: test-access-key"
                echo "   Secret Key: test-secret-key"
                echo "=================================================="
              '';
              # Only run this once the server is up
              depends_on.minio-server.condition = "process_healthy";
            };
          };
        };
      };

      # # (Optional) Add to devShell so you have 'minio' and 'mc' CLI tools available manually
      # devShells.default = pkgs.mkShell {
      #   buildInputs = [
      #     pkgs.minio
      #     pkgs.minio-client
      #   ];
      # };
    };
}
