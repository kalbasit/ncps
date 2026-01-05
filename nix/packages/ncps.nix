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

          # Start MinIO before running tests to enable S3 integration tests
          minioPreCheck = ''
            echo "🚀 Starting MinIO for S3 integration tests..."

            # Create temporary directories for MinIO data and config
            export MINIO_DATA_DIR=$(mktemp -d)
            export HOME=$(mktemp -d)

            # Configure MinIO credentials (must be set before starting MinIO)
            export MINIO_ROOT_USER=admin
            export MINIO_ROOT_PASSWORD=password
            export MINIO_REGION=us-east-1

            # Generate random free ports using python
            # We bind to port 0, get the assigned port, and close the socket immediately.
            # In a Nix sandbox, the race condition risk (port being stolen between check and use) is negligible.
            export MINIO_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')
            export CONSOLE_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')

            # Export S3 test environment variables
            export NCPS_TEST_S3_BUCKET="test-bucket"
            export NCPS_TEST_S3_ENDPOINT="http://127.0.0.1:$MINIO_PORT"
            export NCPS_TEST_S3_REGION="us-east-1"
            export NCPS_TEST_S3_ACCESS_KEY_ID="test-access-key"
            export NCPS_TEST_S3_SECRET_ACCESS_KEY="test-secret-key"

            # Start MinIO server in background
            minio server "$MINIO_DATA_DIR" \
              --address "127.0.0.1:$MINIO_PORT" \
              --console-address "127.0.0.1:$CONSOLE_PORT" &
            export MINIO_PID=$!

            # Wait for MinIO to be ready
            echo "⏳ Waiting for MinIO to be ready..."
            for i in {1..30}; do
              if curl -sf "$NCPS_TEST_S3_ENDPOINT/minio/health/live"; then
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
            mc alias set local "$NCPS_TEST_S3_ENDPOINT" "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"

            # Create test bucket
            mc mb "local/$NCPS_TEST_S3_BUCKET" || true

            # Create service account for tests
            mc admin user svcacct add \
              --access-key "$NCPS_TEST_S3_ACCESS_KEY_ID" \
              --secret-key "$NCPS_TEST_S3_SECRET_ACCESS_KEY" \
              local admin || true

            echo "✅ MinIO configured for S3 integration tests"
          '';

          # Stop MinIO after tests complete
          minioPostCheck = ''
            echo "🛑 Stopping MinIO..."
            if [ -n "$MINIO_PID" ]; then
              kill $MINIO_PID 2>/dev/null || true
              # Wait for MinIO to fully shut down
              for i in {1..30}; do
                if ! kill -0 $MINIO_PID 2>/dev/null; then
                  break
                fi
                sleep 0.5
              done

              # If it's still alive, force kill it
              if kill -0 $MINIO_PID 2>/dev/null; then
                echo "MinIO did not shut down gracefully, force killing..."
                kill -9 $MINIO_PID 2>/dev/null || true
                sleep 1 # Give a moment for the OS to clean up after SIGKILL
              fi
            fi
            sleep 1
            rm -rf "$MINIO_DATA_DIR" 2>/dev/null || true
            echo "✅ MinIO stopped and cleaned up"
          '';

          # Start PostgreSQL before running tests to enable PostgreSQL integration tests
          postgresPreCheck = ''
            echo "🚀 Starting PostgreSQL for integration tests..."

            # Create temporary directory for PostgreSQL data
            export POSTGRES_DATA_DIR=$(mktemp -d)

            # Generate random free port using python
            export POSTGRES_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')

            # Export PostgreSQL test environment variable
            export NCPS_TEST_POSTGRES_URL="postgresql://test-user:test-password@127.0.0.1:$POSTGRES_PORT/test-db?sslmode=disable"

            # Initialize PostgreSQL database
            initdb -D "$POSTGRES_DATA_DIR" -U postgres --no-locale --encoding=UTF8

            # Configure PostgreSQL
            echo "host all all 127.0.0.1/32 trust" >> "$POSTGRES_DATA_DIR/pg_hba.conf"
            echo "listen_addresses = '127.0.0.1'" >> "$POSTGRES_DATA_DIR/postgresql.conf"
            echo "port = $POSTGRES_PORT" >> "$POSTGRES_DATA_DIR/postgresql.conf"
            echo "unix_socket_directories = '$POSTGRES_DATA_DIR'" >> "$POSTGRES_DATA_DIR/postgresql.conf"

            # Start PostgreSQL server in background
            postgres -D "$POSTGRES_DATA_DIR" -k "$POSTGRES_DATA_DIR" &
            export POSTGRES_PID=$!

            # Wait for PostgreSQL to be ready
            echo "⏳ Waiting for PostgreSQL to be ready..."
            for i in {1..30}; do
              if pg_isready -h 127.0.0.1 -p "$POSTGRES_PORT" -U postgres >/dev/null 2>&1; then
                echo "✅ PostgreSQL is ready"
                break
              fi
              if [ $i -eq 30 ]; then
                echo "❌ PostgreSQL failed to start"
                kill $POSTGRES_PID 2>/dev/null || true
                exit 1
              fi
              sleep 1
            done

            # Create test user and database
            psql -h 127.0.0.1 -p "$POSTGRES_PORT" -U postgres -d postgres -c "CREATE USER \"test-user\" WITH PASSWORD 'test-password';"
            psql -h 127.0.0.1 -p "$POSTGRES_PORT" -U postgres -d postgres -c "CREATE DATABASE \"test-db\" OWNER \"test-user\";"

            echo "✅ PostgreSQL configured for integration tests"
          '';

          # Stop PostgreSQL after tests complete
          postgresPostCheck = ''
            echo "🛑 Stopping PostgreSQL..."
            if [ -n "$POSTGRES_PID" ]; then
              kill $POSTGRES_PID 2>/dev/null || true
              # Wait for PostgreSQL to fully shut down
              for i in {1..30}; do
                if ! kill -0 $POSTGRES_PID 2>/dev/null; then
                  break
                fi
                sleep 0.5
              done

              # If it's still alive, force kill it
              if kill -0 $POSTGRES_PID 2>/dev/null; then
                echo "PostgreSQL did not shut down gracefully, force killing..."
                kill -9 $POSTGRES_PID 2>/dev/null || true
                sleep 1 # Give a moment for the OS to clean up after SIGKILL
              fi
            fi
            sleep 1
            rm -rf "$POSTGRES_DATA_DIR" 2>/dev/null || true
            echo "✅ PostgreSQL stopped and cleaned up"
          '';

          # Start MySQL/MariaDB before running tests to enable MySQL integration tests
          mysqlPreCheck = ''
            echo "🚀 Starting MariaDB for integration tests..."

            # Create temporary directory for MariaDB data
            export MYSQL_DATA_DIR=$(mktemp -d)

            # Generate random free port using python
            export MYSQL_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')

            # Export MySQL test environment variable
            export NCPS_TEST_MYSQL_URL="mysql://test-user:test-password@127.0.0.1:$MYSQL_PORT/test-db"

            # Initialize MariaDB database
            mariadb-install-db --datadir="$MYSQL_DATA_DIR" --auth-root-authentication-method=normal

            # Start MariaDB server in background
            mariadbd \
              --datadir="$MYSQL_DATA_DIR" \
              --bind-address=127.0.0.1 \
              --port="$MYSQL_PORT" \
              --socket="$MYSQL_DATA_DIR/mysql.sock" \
              --skip-networking=0 &
            export MYSQL_PID=$!

            # Wait for MariaDB to be ready
            echo "⏳ Waiting for MariaDB to be ready..."
            for i in {1..30}; do
              if mariadb-admin -h 127.0.0.1 -P "$MYSQL_PORT" --protocol=TCP ping >/dev/null 2>&1; then
                echo "✅ MariaDB is ready"
                break
              fi
              if [ $i -eq 30 ]; then
                echo "❌ MariaDB failed to start"
                kill $MYSQL_PID 2>/dev/null || true
                exit 1
              fi
              sleep 1
            done

            # Create test user and database
            mariadb -h 127.0.0.1 -P "$MYSQL_PORT" --protocol=TCP -u root <<EOF
            CREATE DATABASE IF NOT EXISTS \`test-db\`;
            CREATE USER IF NOT EXISTS 'test-user'@'localhost' IDENTIFIED BY 'test-password';
            CREATE USER IF NOT EXISTS 'test-user'@'127.0.0.1' IDENTIFIED BY 'test-password';
            GRANT ALL PRIVILEGES ON \`test-db\`.* TO 'test-user'@'localhost';
            GRANT ALL PRIVILEGES ON \`test-db\`.* TO 'test-user'@'127.0.0.1';
            FLUSH PRIVILEGES;
            EOF

            echo "✅ MariaDB configured for integration tests"
          '';

          # Stop MySQL/MariaDB after tests complete
          mysqlPostCheck = ''
            echo "🛑 Stopping MariaDB..."
            if [ -n "$MYSQL_PID" ]; then
              kill $MYSQL_PID 2>/dev/null || true
              # Wait for MariaDB to fully shut down
              for i in {1..30}; do
                if ! kill -0 $MYSQL_PID 2>/dev/null; then
                  break
                fi
                sleep 0.5
              done

              # If it's still alive, force kill it
              if kill -0 $MYSQL_PID 2>/dev/null; then
                echo "MariaDB did not shut down gracefully, force killing..."
                kill -9 $MYSQL_PID 2>/dev/null || true
                sleep 1 # Give a moment for the OS to clean up after SIGKILL
              fi
            fi
            # Give it an extra moment to release file handles
            sleep 1
            rm -rf "$MYSQL_DATA_DIR" 2>/dev/null || true
            echo "✅ MariaDB stopped and cleaned up"
          '';

          # Start Redis before running tests to enable Redis integration tests
          redisPreCheck = ''
            echo "🚀 Starting Redis for integration tests..."

            # Create temporary directory for Redis data
            export REDIS_DATA_DIR=$(mktemp -d)

            # Generate random free port using python
            export REDIS_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')

            # Export Redis test environment variables
            export NCPS_ENABLE_REDIS_TESTS=1
            export NCPS_TEST_REDIS_ADDRS="127.0.0.1:$REDIS_PORT"

            # Start Redis server in background
            redis-server \
              --dir "$REDIS_DATA_DIR" \
              --bind 127.0.0.1 \
              --port "$REDIS_PORT" \
              --save "" \
              --appendonly no &
            export REDIS_PID=$!

            # Wait for Redis to be ready
            echo "⏳ Waiting for Redis to be ready..."
            for i in {1..30}; do
              if redis-cli -h 127.0.0.1 -p "$REDIS_PORT" ping >/dev/null 2>&1; then
                echo "✅ Redis is ready on port $REDIS_PORT"
                break
              fi
              if [ $i -eq 30 ]; then
                echo "❌ Redis failed to start"
                kill $REDIS_PID 2>/dev/null || true
                exit 1
              fi
              sleep 1
            done

            echo "✅ Redis configured for integration tests"
          '';

          # Stop Redis after tests complete
          redisPostCheck = ''
            echo "🛑 Stopping Redis..."
            if [ -n "$REDIS_PID" ]; then
              kill $REDIS_PID 2>/dev/null || true
              # Wait for Redis to fully shut down
              for i in {1..30}; do
                if ! kill -0 $REDIS_PID 2>/dev/null; then
                  break
                fi
                sleep 0.5
              done

              # If it's still alive, force kill it
              if kill -0 $REDIS_PID 2>/dev/null; then
                echo "Redis did not shut down gracefully, force killing..."
                kill -9 $REDIS_PID 2>/dev/null || true
                sleep 1 # Give a moment for the OS to clean up after SIGKILL
              fi
            fi
            sleep 1
            rm -rf "$REDIS_DATA_DIR" 2>/dev/null || true
            echo "✅ Redis stopped and cleaned up"
          '';
        in
        pkgs.buildGoModule {
          name = "ncps-${shortRev}";

          src = lib.fileset.toSource {
            fileset = lib.fileset.unions [
              ../../cmd
              ../../db/migrations
              ../../db/schema
              ../../go.mod
              ../../go.sum
              ../../main.go
              ../../pkg
              ../../testdata
              ../../testhelper
            ];
            root = ../..;
          };

          vendorHash = "sha256-P+S4+isD+MyxbvE96x8KkBvZOztmTxVYcJ1amu3UEaA=";

          ldflags = [
            "-X github.com/kalbasit/ncps/cmd.Version=${version}"
          ];

          doCheck = true;
          checkFlags = [ "-race" ];

          nativeBuildInputs = [
            pkgs.curl # used for checking MinIO health check
            pkgs.dbmate # used for testing
            pkgs.mariadb # MySQL/MariaDB for integration tests
            pkgs.minio # S3-compatible storage for integration tests
            pkgs.minio-client # mc CLI for MinIO setup
            pkgs.postgresql # PostgreSQL for integration tests
            pkgs.python3 # used for generating the ports
            pkgs.redis # Redis for distributed locking integration tests
          ];

          # pre and post checks
          preCheck = ''
            # Set up cleanup trap to ensure background processes are killed even if tests fail
            cleanup() {
              ${redisPostCheck}
              ${mysqlPostCheck}
              ${postgresPostCheck}
              ${minioPostCheck}
            }
            trap cleanup EXIT

            ${minioPreCheck}
            ${postgresPreCheck}
            ${mysqlPreCheck}
            ${redisPreCheck}
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
