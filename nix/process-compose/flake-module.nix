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
      };

      minioEnvironment = {
        MINIO_ENDPOINT = "http://127.0.0.1:9000";
        MINIO_CONSOLE_PORT = "9001";
        MINIO_CONSOLE = "http://127.0.0.1:9001";
        MINIO_REGION = "us-east-1";
        MINIO_ROOT_PASSWORD = "password";
        MINIO_ROOT_USER = "admin";
        MINIO_TEST_S3_ACCESS_KEY_ID = "test-access-key";
        MINIO_TEST_S3_BUCKET = "test-bucket";
        MINIO_TEST_S3_SECRET_ACCESS_KEY = "test-secret-key";
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
            minio-server = {
              command =
                let
                  startMinio = pkgs.writeShellApplication {
                    name = "start-minio";
                    runtimeInputs = [ pkgs.minio ];
                    text = builtins.readFile ./start-minio.sh;
                  };
                in
                "${startMinio}/bin/start-minio";
              environment = minioEnvironment;
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
            minio-init = {
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
                ''
                  # Remove stale marker file from previous runs
                  rm -f /tmp/ncps-minio-ready

                  ${initMinio}/bin/init-minio

                  # Create ready marker file for process-compose health check
                  touch /tmp/ncps-minio-ready

                  sleep infinity
                '';
              environment = minioEnvironment;
              depends_on.minio-server.condition = "process_healthy";
              readiness_probe = {
                exec.command = "test -f /tmp/ncps-minio-ready";
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
