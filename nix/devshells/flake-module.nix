{
  perSystem =
    { config, pkgs, ... }:
    {
      devShells.default = pkgs.mkShell {
        buildInputs = [
          # python environment for dev-scripts/test-deployments.py
          (pkgs.python3.withPackages (
            ps: with ps; [
              boto3
              kubernetes
              psycopg2-binary
              pymysql
              pyyaml
              requests
            ]
          ))

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
            if [ -t 1 ]; then
              echo "🛑 Run 'eval \"\$(enable-s3-tests)\"' to enable S3 tests." >&2
              exit 0
            fi

            echo "✅ S3 tests enabled, don't forget to run 'nix run .#deps' to start MinIO." >&2
            cat <<'EOF'
              export NCPS_TEST_S3_BUCKET="test-bucket"
              export NCPS_TEST_S3_ENDPOINT="http://127.0.0.1:9000"
              export NCPS_TEST_S3_REGION="us-east-1"
              export NCPS_TEST_S3_ACCESS_KEY_ID="test-access-key"
              export NCPS_TEST_S3_SECRET_ACCESS_KEY="test-secret-key"
            EOF
          '')
          (pkgs.writeShellScriptBin "enable-postgres-tests" ''
            if [ -t 1 ]; then
              echo "🛑 Run 'eval \"\$(enable-postgres-tests)\"' to enable PostgreSQL tests." >&2
              exit 0
            fi

            echo "✅ PostgreSQL tests enabled, don't forget to run 'nix run .#deps' to start PostgreSQL." >&2
            cat <<'EOF'
            export NCPS_TEST_POSTGRES_URL="postgresql://test-user:test-password@127.0.0.1:5432/test-db?sslmode=disable"
            EOF
          '')
          (pkgs.writeShellScriptBin "enable-mysql-tests" ''
            if [ -t 1 ]; then
              echo "🛑 Run 'eval \"\$(enable-mysql-tests)\"' to enable MySQL tests." >&2
              exit 0
            fi

            echo "✅ MySQL tests enabled, don't forget to run 'nix run .#deps' to start MySQL." >&2
            cat <<'EOF'
            export NCPS_TEST_MYSQL_URL="mysql://test-user:test-password@127.0.0.1:3306/test-db"
            EOF
          '')
          (pkgs.writeShellScriptBin "enable-redis-tests" ''
            if [ -t 1 ]; then
              echo "🛑 Run 'eval \"\$(enable-redis-tests)\"' to enable Redis tests." >&2
              exit 0
            fi

            echo "✅ Redis tests enabled, don't forget to run 'nix run .#deps' to start Redis." >&2
            cat <<'EOF'
            export NCPS_ENABLE_REDIS_TESTS=1
            EOF
          '')
          (pkgs.writeShellScriptBin "enable-integration-tests" ''
            if [ -t 1 ]; then
              echo "🛑 Run 'eval \"\$(enable-integration-tests)\"' to enable all integration tests." >&2
              exit 0
            fi

            enable-s3-tests
            enable-postgres-tests
            enable-mysql-tests
            enable-redis-tests
          '')
          (pkgs.writeShellScriptBin "disable-integration-tests" ''
            if [ -t 1 ]; then
              echo "🛑 Run 'eval \"\$(disable-integration-tests)\"' to disable all integration tests." >&2
              exit 0
            fi

            vars_to_unset=$(env | grep '^NCPS_TEST_' | cut -d= -f1)
            if [ -n "$vars_to_unset" ]; then
              echo "✅ Integration tests disabled." >&2
              echo unset $vars_to_unset
            else
              echo "✅ No integration test variables to disable." >&2
            fi
          '')

          pkgs.delve
          pkgs.go
          pkgs.golangci-lint
          pkgs.mariadb
          pkgs.minio
          pkgs.minio-client
          pkgs.postgresql
          pkgs.pre-commit
          pkgs.redis
          pkgs.skopeo
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

          # Set NCPS_DB_SCHEMA_DIR to the repo root's db/schema
          # This avoids requiring the ncps package to be built for dev shell
          export NCPS_DB_SCHEMA_DIR="$(git rev-parse --show-toplevel)/db/schema"

          if [[ "$(${pkgs.gnugrep}/bin/grep '^\(go \)[0-9.]*$' go.mod)" != "go ''${_GO_VERSION}" ]]; then
            ${pkgs.gnused}/bin/sed -e "s:^\(go \)[0-9.]*$:\1''${_GO_VERSION}:" -i go.mod
          fi

          if [[ "$(${pkgs.gnugrep}/bin/grep '^\(go \)[0-9.]*$' nix/dbmate-wrapper/src/go.mod)" != "go ''${_GO_VERSION}" ]]; then
            ${pkgs.gnused}/bin/sed -e "s:^\(go \)[0-9.]*$:\1''${_GO_VERSION}:" -i nix/dbmate-wrapper/src/go.mod
          fi

          echo ""
          echo "🧪 Integration test helpers available:"
          echo "  eval \"\$(enable-s3-tests)\"           - Enable S3/MinIO tests"
          echo "  eval \"\$(enable-postgres-tests)\"     - Enable PostgreSQL tests"
          echo "  eval \"\$(enable-mysql-tests)\"        - Enable MySQL tests"
          echo "  eval \"\$(enable-redis-tests)\"        - Enable Redis tests"
          echo "  eval \"\$(enable-integration-tests)\"  - Enable all integration tests"
          echo "  eval \"\$(disable-integration-tests)\" - Disable all integration tests"
          echo ""
          echo "💡 Start dependencies with: nix run .#deps"
          echo ""
        '';
      };
    };
}
