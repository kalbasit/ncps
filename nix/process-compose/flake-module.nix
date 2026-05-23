{ inputs, ... }:
{
  imports = [
    inputs.process-compose-flake.flakeModule
  ];
  perSystem =
    { pkgs, ... }:
    let
      mariaEnvironment = {
        # Official PostgreSQL supported environment variables.
        MYSQL_TCP_PORT = "3306";

        # Custom PostgreSQL supported environment variables.
        MYSQL_HOST = "127.0.0.1";
        MYSQL_USER = "root";
        MYSQL_DEV_DB = "dev-db";
        MYSQL_DEV_USER = "dev-user";
        MYSQL_DEV_PASSWORD = "dev-password";
        MYSQL_TEST_DB = "test-db";
        MYSQL_TEST_USER = "test-user";
        MYSQL_TEST_PASSWORD = "test-password";
        MYSQL_MIGRATION_DB = "migration-db";
        MYSQL_MIGRATION_USER = "migration-user";
        MYSQL_MIGRATION_PASSWORD = "migration-password";
      };

      garageEnvironment = {
        # Backend-neutral env vars consumed by Go tests (see testhelper/s3.go)
        # and the dev shell `enable-s3-tests` helper.
        NCPS_TEST_S3_ENDPOINT = "http://127.0.0.1:9000";
        NCPS_TEST_S3_PORT = "9000";
        NCPS_TEST_S3_REGION = "us-east-1";
        NCPS_TEST_S3_ACCESS_KEY_ID = "GK1234567890abcdef12345678";
        NCPS_TEST_S3_BUCKET = "test-bucket";
        NCPS_TEST_S3_SECRET_ACCESS_KEY = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";

        # Garage-internal settings. Storage paths (GARAGE_DATA_DIR / META_DIR /
        # CONFIG_FILE) are derived per-UID inside start-garage.sh and
        # init-garage.sh to avoid collisions between users sharing a host.
        GARAGE_RPC_PORT = "3901";
        GARAGE_ADMIN_PORT = "3903";
        # Fixed dev secrets — Garage requires these but they're not exposed to tests.
        # 64-char hex RPC secret and an arbitrary admin token.
        GARAGE_RPC_SECRET = "0000000000000000000000000000000000000000000000000000000000000000";
        GARAGE_ADMIN_TOKEN = "ncps-dev-admin-token";
      };

      postgresEnvironment = {
        # Official PostgreSQL supported environment variables.
        PGHOST = "127.0.0.1";
        PGPORT = "5432";
        PGUSER = "postgres";
        PGDATABASE = "postgres";

        # Custom PostgreSQL supported environment variables.
        PG_DEV_DB = "dev-db";
        PG_DEV_USER = "dev-user";
        PG_DEV_PASSWORD = "dev-password";
        PG_TEST_DB = "test-db";
        PG_TEST_USER = "test-user";
        PG_TEST_PASSWORD = "test-password";
        PG_MIGRATION_DB = "migration-db";
        PG_MIGRATION_USER = "migration-user";
        PG_MIGRATION_PASSWORD = "migration-password";
      };

      redisEnvironment = {
        REDIS_HOST = "127.0.0.1";
        REDIS_PORT = "6379";
      };
    in
    {
      # This creates the 'nix run' command
      process-compose.deps = {
        settings = {
          processes = {
            garage-server = {
              command =
                let
                  startGarage = pkgs.writeShellApplication {
                    name = "start-garage";
                    runtimeInputs = [ pkgs.garage ];
                    text = builtins.readFile ./start-garage.sh;
                  };
                in
                ''
                  # Ensure the data + metadata dirs exist before garage starts.
                  mkdir -p "$GARAGE_DATA_DIR" "$GARAGE_META_DIR"
                  exec ${startGarage}/bin/start-garage
                '';
              environment = garageEnvironment;
              readiness_probe = {
                # Garage exposes its health probe via the admin API.
                http_get = {
                  host = "127.0.0.1";
                  port = 3903;
                  path = "/health";
                };
                initial_delay_seconds = 2;
                period_seconds = 5;
              };
            };
            garage-init = {
              command =
                let
                  initGarage = pkgs.writeShellApplication {
                    name = "init-garage";
                    runtimeInputs = [
                      pkgs.garage
                      pkgs.awscli2
                      pkgs.curl
                      pkgs.jq
                      pkgs.gawk
                    ];
                    text = builtins.readFile ./init-garage.sh;
                  };
                in
                ''
                  # Per-UID ready marker (avoids collisions when multiple users
                  # run process-compose on the same host).
                  READY_MARKER="''${TMPDIR:-/tmp}/ncps-garage-$(id -u).ready"

                  # Remove stale marker file from previous runs, and clean up on
                  # shutdown so a stale marker doesn't survive into the next run.
                  rm -f "$READY_MARKER"
                  trap 'rm -f "$READY_MARKER"' EXIT INT TERM

                  ${initGarage}/bin/init-garage

                  # Create ready marker file for process-compose health check
                  touch "$READY_MARKER"

                  sleep infinity
                '';
              environment = garageEnvironment;
              depends_on.garage-server.condition = "process_healthy";
              readiness_probe = {
                exec.command = ''test -f "''${TMPDIR:-/tmp}/ncps-garage-$(id -u).ready"'';
                initial_delay_seconds = 3;
                period_seconds = 1;
              };
            };
            postgres-server = {
              command =
                let
                  startPostgres = pkgs.writeShellApplication {
                    name = "start-postgres";
                    runtimeInputs = [ pkgs.postgresql ];
                    text = builtins.readFile ./start-postgres.sh;
                  };
                in
                "${startPostgres}/bin/start-postgres";
              environment = postgresEnvironment;
              readiness_probe = {
                exec = {
                  command = "${pkgs.postgresql}/bin/pg_isready -h 127.0.0.1 -p 5432";
                };
                initial_delay_seconds = 2;
                period_seconds = 5;
              };
            };
            postgres-init = {
              command =
                let
                  initPostgres = pkgs.writeShellApplication {
                    name = "init-postgres";
                    runtimeInputs = [ pkgs.postgresql ];
                    text = builtins.readFile ./init-postgres.sh;
                  };
                in
                ''
                  # Remove stale marker file from previous runs
                  rm -f /tmp/ncps-postgres-ready

                  ${initPostgres}/bin/init-postgres ${./postgres-dblink-create-drop-functions.sql}

                  # Create ready marker file for process-compose health check
                  touch /tmp/ncps-postgres-ready

                  sleep infinity
                '';
              environment = postgresEnvironment;
              depends_on.postgres-server.condition = "process_healthy";
              readiness_probe = {
                exec.command = "test -f /tmp/ncps-postgres-ready";
                initial_delay_seconds = 3;
                period_seconds = 1;
              };
            };
            mariadb-server = {
              command =
                let
                  startMysql = pkgs.writeShellApplication {
                    name = "start-mysql";
                    runtimeInputs = [ pkgs.mariadb ];
                    text = builtins.readFile ./start-mysql.sh;
                  };
                in
                "${startMysql}/bin/start-mysql";
              environment = mariaEnvironment;
              readiness_probe = {
                exec = {
                  command = "${pkgs.mariadb}/bin/mariadb-admin -h 127.0.0.1 -P 3306 --protocol=TCP ping";
                };
                initial_delay_seconds = 3;
                period_seconds = 5;
              };
            };
            mariadb-init = {
              command =
                let
                  initMySQL = pkgs.writeShellApplication {
                    name = "init-mysql";
                    runtimeInputs = [ pkgs.mariadb ];
                    text = builtins.readFile ./init-mysql.sh;
                  };
                in
                ''
                  # Remove stale marker file from previous runs
                  rm -f /tmp/ncps-mysql-ready

                  ${initMySQL}/bin/init-mysql

                  # Create ready marker file for process-compose health check
                  touch /tmp/ncps-mysql-ready

                  sleep infinity
                '';
              environment = mariaEnvironment;
              depends_on.mariadb-server.condition = "process_healthy";
              readiness_probe = {
                exec.command = "test -f /tmp/ncps-mysql-ready";
                initial_delay_seconds = 3;
                period_seconds = 1;
              };
            };
            redis-server = {
              command =
                let
                  startRedis = pkgs.writeShellApplication {
                    name = "start-redis";
                    runtimeInputs = [ pkgs.redis ];
                    text = builtins.readFile ./start-redis.sh;
                  };
                in
                "${startRedis}/bin/start-redis";
              environment = redisEnvironment;
              readiness_probe = {
                exec.command = "${pkgs.redis}/bin/redis-cli -h 127.0.0.1 -p 6379 ping";
                initial_delay_seconds = 1;
                period_seconds = 2;
              };
            };
          };
        };
      };
    };
}
