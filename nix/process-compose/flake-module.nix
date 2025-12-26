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
                echo "This is secret data!" > /tmp/message.txt
                ${pkgs.minio-client}/bin/mc cp /tmp/message.txt local/test-bucket/message.txt
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
                # Generate presigned URL using mc
                SIGNED_URL=$(${pkgs.minio-client}/bin/mc share download --expire 1h --json tester/test-bucket/message.txt | ${pkgs.jq}/bin/jq -r '.share')

                # Test the signed URL as-is (no Host header manipulation)
                if curl --output /dev/null --silent --fail "$SIGNED_URL"; then
                  echo "✅ Success"
                  # Verify content
                  CONTENT=$(curl --silent "$SIGNED_URL")
                  if [ "$CONTENT" = "This is secret data!" ]; then
                    echo "   Content verified: ✅"
                  else
                    echo "   Content mismatch: ❌"
                  fi
                else
                  echo "❌ Failed"
                  echo "Debug URL: $SIGNED_URL"
                  echo "Testing with verbose output:"
                  curl -v "$SIGNED_URL" 2>&1 | tail -20
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
