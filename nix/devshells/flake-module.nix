{
  perSystem =
    { config, pkgs, ... }:
    {
      devShells.default = pkgs.mkShell {
        buildInputs = [
          # Use real dbmate for the wrapper to call
          (pkgs.writeShellScriptBin "dbmate.real" ''
            exec ${pkgs.dbmate}/bin/dbmate "$@"
          '')
          # dbmate-wrapper provides the dbmate command
          (pkgs.writeShellScriptBin "dbmate" ''
            exec ${config.packages.dbmate-wrapper}/bin/dbmate-wrapper "$@"
          '')
          # Helper scripts for enabling integration tests
          (pkgs.writeShellScriptBin "enable-s3-tests" ''
            cat <<'EOF'
            export NCPS_TEST_S3_BUCKET="test-bucket"
            export NCPS_TEST_S3_ENDPOINT="http://127.0.0.1:9000"
            export NCPS_TEST_S3_REGION="us-east-1"
            export NCPS_TEST_S3_ACCESS_KEY_ID="test-access-key"
            export NCPS_TEST_S3_SECRET_ACCESS_KEY="test-secret-key"
            EOF
          '')
          (pkgs.writeShellScriptBin "enable-postgres-tests" ''
            cat <<'EOF'
            export NCPS_TEST_POSTGRES_URL="postgresql://test-user:test-password@127.0.0.1:5432/test-db?sslmode=disable"
            EOF
          '')
          (pkgs.writeShellScriptBin "enable-mysql-tests" ''
            cat <<'EOF'
            export NCPS_TEST_MYSQL_URL="mysql://test-user:test-password@127.0.0.1:3306/test-db"
            EOF
          '')
          (pkgs.writeShellScriptBin "enable-redis-tests" ''
            cat <<'EOF'
            export NCPS_ENABLE_REDIS_TESTS=1
            EOF
          '')
          (pkgs.writeShellScriptBin "enable-all-integration-tests" ''
            cat <<'EOF'
            export NCPS_TEST_S3_BUCKET="test-bucket"
            export NCPS_TEST_S3_ENDPOINT="http://127.0.0.1:9000"
            export NCPS_TEST_S3_REGION="us-east-1"
            export NCPS_TEST_S3_ACCESS_KEY_ID="test-access-key"
            export NCPS_TEST_S3_SECRET_ACCESS_KEY="test-secret-key"
            export NCPS_TEST_POSTGRES_URL="postgresql://test-user:test-password@127.0.0.1:5432/test-db?sslmode=disable"
            export NCPS_TEST_MYSQL_URL="mysql://test-user:test-password@127.0.0.1:3306/test-db"
            export NCPS_ENABLE_REDIS_TESTS=1
            EOF
          '')
          (pkgs.writeShellScriptBin "disable-integration-tests" ''
            cat <<'EOF'
            unset NCPS_TEST_S3_BUCKET
            unset NCPS_TEST_S3_ENDPOINT
            unset NCPS_TEST_S3_REGION
            unset NCPS_TEST_S3_ACCESS_KEY_ID
            unset NCPS_TEST_S3_SECRET_ACCESS_KEY
            unset NCPS_TEST_POSTGRES_URL
            unset NCPS_TEST_MYSQL_URL
            unset NCPS_ENABLE_REDIS_TESTS
            EOF
          '')
          pkgs.delve
          pkgs.go
          pkgs.golangci-lint
          pkgs.minio
          pkgs.minio-client
          pkgs.redis
          pkgs.sqlc
          pkgs.sqlfluff
          pkgs.watchexec
        ];

        _GO_VERSION = "${pkgs.go.version}";
        _DBMATE_VERSION = "${pkgs.dbmate.version}";

        # Disable hardening for fortify otherwize it's not possible to use Delve.
        hardeningDisable = [ "fortify" ];

        shellHook = ''
          ${config.pre-commit.installationScript}

          # Set NCPS_DB_MIGRATIONS_DIR to the repo root's db/migrations
          # This avoids requiring the ncps package to be built for dev shell
          export NCPS_DB_MIGRATIONS_DIR="$(git rev-parse --show-toplevel)/db/migrations"

          if [[ "$(${pkgs.gnugrep}/bin/grep '^\(go \)[0-9.]*$' go.mod)" != "go ''${_GO_VERSION}" ]]; then
            ${pkgs.gnused}/bin/sed -e "s:^\(go \)[0-9.]*$:\1''${_GO_VERSION}:" -i go.mod
          fi

          if [[ "$(${pkgs.gnugrep}/bin/grep '^\(go \)[0-9.]*$' nix/dbmate-wrapper/src/go.mod)" != "go ''${_GO_VERSION}" ]]; then
            ${pkgs.gnused}/bin/sed -e "s:^\(go \)[0-9.]*$:\1''${_GO_VERSION}:" -i nix/dbmate-wrapper/src/go.mod
          fi

          echo ""
          echo "🧪 Integration test helpers available:"
          echo "  eval \"\$(enable-s3-tests)\"          - Enable S3/MinIO tests"
          echo "  eval \"\$(enable-postgres-tests)\"    - Enable PostgreSQL tests"
          echo "  eval \"\$(enable-mysql-tests)\"       - Enable MySQL tests"
          echo "  eval \"\$(enable-redis-tests)\"       - Enable Redis tests"
          echo "  eval \"\$(enable-all-integration-tests)\" - Enable all integration tests"
          echo "  eval \"\$(disable-integration-tests)\" - Disable all integration tests"
          echo ""
          echo "💡 Start dependencies with: nix run .#deps"
          echo ""
        '';
      };
    };
}
