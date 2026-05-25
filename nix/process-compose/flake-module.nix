{ inputs, ... }:
{
  imports = [
    inputs.process-compose-flake.flakeModule
  ];
  perSystem =
    { pkgs, ... }:
    let
      # -----------------------------------------------------------------------
      # Fixed environments — used by nix run .#deps (interactive dev tool).
      # All port values are hard-coded; nothing is read from the caller's env.
      # -----------------------------------------------------------------------
      mariaEnvironment = {
        # Official MariaDB supported environment variables.
        MYSQL_TCP_PORT = "3306";

        # Custom variables consumed by start-mysql.sh / init-mysql.sh.
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

      # -----------------------------------------------------------------------
      # Dynamic environments — used by nix run .#test-deps.
      # Port values are env var placeholders ("\${VAR:-default}").
      # process-compose expands them at launch time when
      # settings.disable_env_expansion = false.
      # -----------------------------------------------------------------------
      testDepsMariaEnvironment = {
        MYSQL_TCP_PORT = "\${MYSQL_TCP_PORT:-3306}";
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

      testDepsGarageEnvironment = {
        NCPS_TEST_S3_ENDPOINT = "http://127.0.0.1:\${NCPS_TEST_S3_PORT:-9000}";
        NCPS_TEST_S3_PORT = "\${NCPS_TEST_S3_PORT:-9000}";
        NCPS_TEST_S3_REGION = "us-east-1";
        NCPS_TEST_S3_ACCESS_KEY_ID = "GK1234567890abcdef12345678";
        NCPS_TEST_S3_BUCKET = "test-bucket";
        NCPS_TEST_S3_SECRET_ACCESS_KEY = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
        GARAGE_RPC_PORT = "\${GARAGE_RPC_PORT:-3901}";
        GARAGE_ADMIN_PORT = "\${GARAGE_ADMIN_PORT:-3903}";
        GARAGE_RPC_SECRET = "0000000000000000000000000000000000000000000000000000000000000000";
        GARAGE_ADMIN_TOKEN = "ncps-dev-admin-token";
      };

      testDepsPostgresEnvironment = {
        PGHOST = "127.0.0.1";
        PGPORT = "\${PGPORT:-5432}";
        PGUSER = "postgres";
        PGDATABASE = "postgres";
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

      testDepsRedisEnvironment = {
        REDIS_HOST = "127.0.0.1";
        REDIS_PORT = "\${REDIS_PORT:-6379}";
      };

      # -----------------------------------------------------------------------
      # Shared derivations — compiled once, referenced by both deps and test-deps.
      # -----------------------------------------------------------------------
      startGarageApp = pkgs.writeShellApplication {
        name = "start-garage";
        runtimeInputs = [ pkgs.garage ];
        text = builtins.readFile ./start-garage.sh;
      };
      initGarageApp = pkgs.writeShellApplication {
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
      startPostgresApp = pkgs.writeShellApplication {
        name = "start-postgres";
        runtimeInputs = [ pkgs.postgresql ];
        text = builtins.readFile ./start-postgres.sh;
      };
      initPostgresApp = pkgs.writeShellApplication {
        name = "init-postgres";
        runtimeInputs = [ pkgs.postgresql ];
        text = builtins.readFile ./init-postgres.sh;
      };
      startMysqlApp = pkgs.writeShellApplication {
        name = "start-mysql";
        runtimeInputs = [ pkgs.mariadb ];
        text = builtins.readFile ./start-mysql.sh;
      };
      initMysqlApp = pkgs.writeShellApplication {
        name = "init-mysql";
        runtimeInputs = [ pkgs.mariadb ];
        text = builtins.readFile ./init-mysql.sh;
      };
      startRedisApp = pkgs.writeShellApplication {
        name = "start-redis";
        runtimeInputs = [ pkgs.redis ];
        text = builtins.readFile ./start-redis.sh;
      };
    in
    {
      # nix run .#deps — fixed ports, for interactive development.
      process-compose.deps = {
        settings = {
          processes = {
            garage-server = {
              command = "exec ${startGarageApp}/bin/start-garage";
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
              command = ''
                # Per-UID ready marker (avoids collisions when multiple users
                # run process-compose on the same host).
                READY_MARKER="''${TMPDIR:-/tmp}/ncps-garage-$(id -u).ready"

                # Remove stale marker file from previous runs, and clean up on
                # shutdown so a stale marker doesn't survive into the next run.
                rm -f "$READY_MARKER"
                trap 'rm -f "$READY_MARKER"' EXIT INT TERM

                ${initGarageApp}/bin/init-garage

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
              command = "${startPostgresApp}/bin/start-postgres";
              environment = postgresEnvironment;
              readiness_probe = {
                exec.command = "${pkgs.postgresql}/bin/pg_isready -h 127.0.0.1 -p 5432";
                initial_delay_seconds = 2;
                period_seconds = 5;
              };
            };
            postgres-init = {
              command = ''
                # Remove stale marker file from previous runs
                rm -f /tmp/ncps-postgres-ready

                ${initPostgresApp}/bin/init-postgres ${./postgres-dblink-create-drop-functions.sql}

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
              command = "${startMysqlApp}/bin/start-mysql";
              environment = mariaEnvironment;
              readiness_probe = {
                exec.command = "${pkgs.mariadb}/bin/mariadb-admin -h 127.0.0.1 -P 3306 --protocol=TCP ping";
                initial_delay_seconds = 3;
                period_seconds = 5;
              };
            };
            mariadb-init = {
              command = ''
                # Remove stale marker file from previous runs
                rm -f /tmp/ncps-mysql-ready

                ${initMysqlApp}/bin/init-mysql

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
              command = "${startRedisApp}/bin/start-redis";
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

      # nix run .#test-deps — ports taken from the caller's environment.
      # disable_env_expansion = false lets process-compose expand ${PORT_VAR}
      # placeholders at launch time.  Used exclusively by task test:auto /
      # dev-scripts/test-auto.sh, which allocates random free ports and exports
      # them before invoking this profile.
      process-compose.test-deps = {
        settings = {
          disable_env_expansion = false;
          processes = {
            garage-server = {
              command = "exec ${startGarageApp}/bin/start-garage";
              environment = testDepsGarageEnvironment;
              readiness_probe = {
                # http_get.port cannot reference an env var; use exec + curl instead.
                exec.command = "curl -sf http://127.0.0.1:\${GARAGE_ADMIN_PORT:-3903}/health";
                initial_delay_seconds = 2;
                period_seconds = 5;
              };
            };
            garage-init = {
              command = ''
                READY_MARKER="''${TMPDIR:-/tmp}/ncps-garage-$(id -u).ready"
                rm -f "$READY_MARKER"
                trap 'rm -f "$READY_MARKER"' EXIT INT TERM
                ${initGarageApp}/bin/init-garage
                touch "$READY_MARKER"
                sleep infinity
              '';
              environment = testDepsGarageEnvironment;
              depends_on.garage-server.condition = "process_healthy";
              readiness_probe = {
                exec.command = ''test -f "''${TMPDIR:-/tmp}/ncps-garage-$(id -u).ready"'';
                initial_delay_seconds = 3;
                period_seconds = 1;
              };
            };
            postgres-server = {
              command = "${startPostgresApp}/bin/start-postgres";
              environment = testDepsPostgresEnvironment;
              readiness_probe = {
                exec.command = "${pkgs.postgresql}/bin/pg_isready -h 127.0.0.1 -p \${PGPORT:-5432}";
                initial_delay_seconds = 2;
                period_seconds = 5;
              };
            };
            postgres-init = {
              command = ''
                READY_MARKER="/tmp/ncps-postgres-''${TEST_PC_PORT:-0}.ready"
                rm -f "$READY_MARKER"
                ${initPostgresApp}/bin/init-postgres ${./postgres-dblink-create-drop-functions.sql}
                touch "$READY_MARKER"
                sleep infinity
              '';
              environment = testDepsPostgresEnvironment // {
                TEST_PC_PORT = "\${TEST_PC_PORT:-0}";
              };
              depends_on.postgres-server.condition = "process_healthy";
              readiness_probe = {
                exec.command = "test -f /tmp/ncps-postgres-\${TEST_PC_PORT:-0}.ready";
                initial_delay_seconds = 3;
                period_seconds = 1;
              };
            };
            mariadb-server = {
              command = "${startMysqlApp}/bin/start-mysql";
              environment = testDepsMariaEnvironment;
              readiness_probe = {
                exec.command = "${pkgs.mariadb}/bin/mariadb-admin -h 127.0.0.1 -P \${MYSQL_TCP_PORT:-3306} --protocol=TCP ping";
                initial_delay_seconds = 3;
                period_seconds = 5;
              };
            };
            mariadb-init = {
              command = ''
                READY_MARKER="/tmp/ncps-mysql-''${TEST_PC_PORT:-0}.ready"
                rm -f "$READY_MARKER"
                ${initMysqlApp}/bin/init-mysql
                touch "$READY_MARKER"
                sleep infinity
              '';
              environment = testDepsMariaEnvironment // {
                TEST_PC_PORT = "\${TEST_PC_PORT:-0}";
              };
              depends_on.mariadb-server.condition = "process_healthy";
              readiness_probe = {
                exec.command = "test -f /tmp/ncps-mysql-\${TEST_PC_PORT:-0}.ready";
                initial_delay_seconds = 3;
                period_seconds = 1;
              };
            };
            redis-server = {
              command = "${startRedisApp}/bin/start-redis";
              environment = testDepsRedisEnvironment;
              readiness_probe = {
                exec.command = "${pkgs.redis}/bin/redis-cli -h 127.0.0.1 -p \${REDIS_PORT:-6379} ping";
                initial_delay_seconds = 1;
                period_seconds = 2;
              };
            };
          };
        };
      };
    };
}
