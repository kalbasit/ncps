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
                "${initMinio}/bin/init-minio";
              depends_on.minio-server.condition = "process_healthy";
              readiness_probe = {
                exec = {
                  command = "test -f /tmp/ncps-minio-ready";
                };
                initial_delay_seconds = 1;
                period_seconds = 1;
              };
            };
            postgres-server = {
              command = ''
                DATA_DIR=$(mktemp -d)
                echo "Storing ephemeral PostgreSQL data in $DATA_DIR"
                ${pkgs.postgresql}/bin/initdb -D $DATA_DIR -U postgres --no-locale --encoding=UTF8
                echo "host all all 127.0.0.1/32 trust" >> $DATA_DIR/pg_hba.conf
                echo "listen_addresses = '127.0.0.1'" >> $DATA_DIR/postgresql.conf
                echo "port = 5432" >> $DATA_DIR/postgresql.conf
                echo "unix_socket_directories = '$DATA_DIR'" >> $DATA_DIR/postgresql.conf
                ${pkgs.postgresql}/bin/postgres -D $DATA_DIR -k $DATA_DIR
              '';
              readiness_probe = {
                exec = {
                  command = "${pkgs.postgresql}/bin/pg_isready -h 127.0.0.1 -p 5432";
                };
                initial_delay_seconds = 2;
                period_seconds = 5;
              };
            };
            init-postgres = {
              command =
                let
                  initPostgres = pkgs.writeShellApplication {
                    name = "init-postgres";
                    runtimeInputs = [ pkgs.postgresql ];
                    text = builtins.readFile ./init-postgres.sh;
                  };
                in
                "${initPostgres}/bin/init-postgres";
              depends_on.postgres-server.condition = "process_healthy";
              readiness_probe = {
                exec = {
                  command = "test -f /tmp/ncps-postgres-ready";
                };
                initial_delay_seconds = 1;
                period_seconds = 1;
              };
            };
            mariadb-server = {
              command = ''
                DATA_DIR=$(mktemp -d)
                echo "Storing ephemeral MariaDB data in $DATA_DIR"
                ${pkgs.mariadb}/bin/mariadb-install-db --datadir=$DATA_DIR --auth-root-authentication-method=normal
                ${pkgs.mariadb}/bin/mariadbd \
                  --datadir=$DATA_DIR \
                  --bind-address=127.0.0.1 \
                  --port=3306 \
                  --socket=$DATA_DIR/mysql.sock \
                  --skip-networking=0
              '';
              readiness_probe = {
                exec = {
                  command = "${pkgs.mariadb}/bin/mariadb-admin -h 127.0.0.1 -P 3306 --protocol=TCP ping";
                };
                initial_delay_seconds = 3;
                period_seconds = 5;
              };
            };
            init-mariadb = {
              command =
                let
                  initMySQL = pkgs.writeShellApplication {
                    name = "init-mysql";
                    runtimeInputs = [ pkgs.mariadb ];
                    text = builtins.readFile ./init-mysql.sh;
                  };
                in
                "${initMySQL}/bin/init-mysql";
              depends_on.mariadb-server.condition = "process_healthy";
              readiness_probe = {
                exec = {
                  command = "test -f /tmp/ncps-mysql-ready";
                };
                initial_delay_seconds = 1;
                period_seconds = 1;
              };
            };
          };
        };
      };
    };
}
