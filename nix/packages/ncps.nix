{ self, ... }:
{
  perSystem =
    {
      lib,
      pkgs,
      ...
    }:
    {
      packages.ncps =
        let
          shortRev = self.shortRev or self.dirtyShortRev;
          rev = self.rev or self.dirtyRev;
          tag = builtins.getEnv "RELEASE_VERSION";

          version = if tag != "" then tag else rev;
        in
        pkgs.buildGoModule {
          name = "ncps-${shortRev}";

          src = lib.fileset.toSource {
            fileset = lib.fileset.unions [
              ../../cmd
              ../../db/migrations
              ../../go.mod
              ../../go.sum
              ../../main.go
              ../../pkg
              ../../testdata
              ../../testhelper
            ];
            root = ../..;
          };

          ldflags = [
            "-X github.com/kalbasit/ncps/cmd.Version=${version}"
          ];

          vendorHash = "sha256-hSC2/KWZLk2KtN31tam0ZmYp/eUtPpc6DAeJExvaeGw=";

          doCheck = true;
          checkFlags = [ "-race" ];

          nativeBuildInputs = [
            pkgs.dbmate # used for testing
            pkgs.minio # S3-compatible storage for integration tests
            pkgs.minio-client # mc CLI for MinIO setup
          ];

          # Start MinIO before running tests to enable S3 integration tests
          preCheck = ''
            echo "🚀 Starting MinIO for S3 integration tests..."

            # Create temporary directories for MinIO data and config
            export MINIO_DATA_DIR=$(mktemp -d)
            export HOME=$(mktemp -d)

            # Configure MinIO credentials (must be set before starting MinIO)
            export MINIO_ROOT_USER=admin
            export MINIO_ROOT_PASSWORD=password
            export MINIO_REGION=us-east-1

            # Start MinIO server in background
            ${pkgs.minio}/bin/minio server "$MINIO_DATA_DIR" \
              --address "127.0.0.1:9000" \
              --console-address "127.0.0.1:9001" > /dev/null 2>&1 &
            export MINIO_PID=$!

            # Wait for MinIO to be ready
            echo "⏳ Waiting for MinIO to be ready..."
            for i in {1..30}; do
              if ${pkgs.curl}/bin/curl -sf http://127.0.0.1:9000/minio/health/live > /dev/null 2>&1; then
                echo "✅ MinIO is ready"
                break
              fi
              if [ $i -eq 30 ]; then
                echo "❌ MinIO failed to start"
                kill $MINIO_PID 2>/dev/null || true
                exit 1
              fi
              sleep 1
            done

            # Setup admin alias
            ${pkgs.minio-client}/bin/mc alias set local http://127.0.0.1:9000 admin password > /dev/null

            # Create test bucket
            ${pkgs.minio-client}/bin/mc mb local/test-bucket > /dev/null 2>&1 || true

            # Create service account for tests
            ${pkgs.minio-client}/bin/mc admin user svcacct add \
              --access-key "test-access-key" \
              --secret-key "test-secret-key" \
              local admin > /dev/null 2>&1 || true

            # Export S3 test environment variables
            export NCPS_TEST_S3_BUCKET="test-bucket"
            export NCPS_TEST_S3_ENDPOINT="http://127.0.0.1:9000"
            export NCPS_TEST_S3_REGION="us-east-1"
            export NCPS_TEST_S3_ACCESS_KEY_ID="test-access-key"
            export NCPS_TEST_S3_SECRET_ACCESS_KEY="test-secret-key"

            echo "✅ MinIO configured for S3 integration tests"
          '';

          # Stop MinIO after tests complete
          postCheck = ''
            echo "🛑 Stopping MinIO..."
            kill $MINIO_PID 2>/dev/null || true
            rm -rf "$MINIO_DATA_DIR"
            echo "✅ MinIO stopped and cleaned up"
          '';

          postInstall = ''
            mkdir -p $out/share/ncps
            cp -r db $out/share/ncps/db
          '';

          meta = {
            description = "Nix binary cache proxy service";
            homepage = "https://github.com/kalbasit/ncps";
            license = lib.licenses.mit;
            mainProgram = "ncps";
            maintainers = [ lib.maintainers.kalbasit ];
          };
        };

    };
}
