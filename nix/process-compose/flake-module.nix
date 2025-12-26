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
              command = ''
                set -e
                # 1. Setup Admin Alias (Strictly use 127.0.0.1)
                ${pkgs.minio-client}/bin/mc alias set local http://127.0.0.1:9000 admin password

                # 2. Setup Resources
                ${pkgs.minio-client}/bin/mc mb local/test-bucket || true
                ${pkgs.minio-client}/bin/mc admin user svcacct add \
                  --access-key "test-access-key" \
                  --secret-key "test-secret-key" \
                  local admin || true

                echo "This is secret data!" > message.txt
                ${pkgs.minio-client}/bin/mc cp message.txt local/test-bucket/message.txt

                echo "---------------------------------------------------"
                echo "🔍 VERIFICATION CHECKS:"

                # Check A: Keys (mc handles signing internally)
                echo -n "1. Access Keys... "
                ${pkgs.minio-client}/bin/mc alias set tester http://127.0.0.1:9000 test-access-key test-secret-key > /dev/null
                if ${pkgs.minio-client}/bin/mc ls tester/test-bucket/message.txt > /dev/null; then
                  echo "✅ Success"
                else
                  echo "❌ Failed"
                fi

                # Check B: Public Access (Anonymous)
                echo -n "2. Public Access (Expect 403)... "
                HTTP_CODE=$(curl -o /dev/null --silent --head --write-out '%{http_code}\n' "http://127.0.0.1:9000/test-bucket/message.txt")
                if [ "$HTTP_CODE" -eq "403" ]; then
                   echo "✅ Success"
                else
                   echo "❌ Failed (Got $HTTP_CODE)"
                fi

                # Check C: Signed URL
                echo -n "3. Signed Access... "

                RAW_OUTPUT=$(${pkgs.minio-client}/bin/mc share download --expire 1h tester/test-bucket/message.txt)
                SIGNED_URL=$(echo "$RAW_OUTPUT" | grep "Share: " | sed 's/Share: //')

                # FIX: Force the Host header to match the signature
                if curl --header "Host: 127.0.0.1:9000" --output /dev/null --silent --head --fail "$SIGNED_URL"; then
                  echo "✅ Success"
                else
                  echo "❌ Failed"
                  echo "Debug URL: $SIGNED_URL"
                fi
                echo "---------------------------------------------------"

                sleep infinity
              '';
              depends_on.minio-server.condition = "process_healthy";
            };
          };
        };
      };
    };
}
