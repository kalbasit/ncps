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
          version =
            let
              rev = self.rev or self.dirtyRev;
              tag = lib.trim (builtins.readFile ./version.txt);
            in
            if tag != "" then tag else rev;

          vendorHash = "sha256-QZikr0kE/kvnI4RG02lxVpG4teTg3Uo68st9xLlbfm0=";
        in
        pkgs.buildGoModule {
          inherit version vendorHash;

          pname = "ncps";

          src = lib.fileset.toSource {
            fileset = lib.fileset.unions [
              ./post-check-minio.sh
              ./pre-check-minio.sh

              ./post-check-mysql.sh
              ./pre-check-mysql.sh

              ./post-check-postgres.sh
              ./pre-check-postgres.sh

              ./post-check-redis.sh
              ./pre-check-redis.sh

              ../../../db/migrations
              ../../../db/schema
              ../../../go.mod
              ../../../go.sum
              ../../../main.go
              ../../../nix/process-compose/init-minio.sh
              ../../../nix/process-compose/init-mysql.sh
              ../../../nix/process-compose/init-postgres.sh
              ../../../nix/process-compose/postgres-dblink-create-drop-functions.sql
              ../../../nix/process-compose/start-minio.sh
              ../../../nix/process-compose/start-mysql.sh
              ../../../nix/process-compose/start-postgres.sh
              ../../../nix/process-compose/start-redis.sh
              ../../../pkg
              ../../../testdata
              ../../../testhelper
            ];
            root = ../../..;
          };

          ldflags = [
            "-X github.com/kalbasit/ncps/pkg/ncps.Version=${version}"
          ];

          nativeBuildInputs = [
            pkgs.curl # used for checking MinIO health check
            pkgs.dbmate # used for testing
            pkgs.jq # used for testing by the init-minio
            pkgs.mariadb # MySQL/MariaDB for integration tests
            pkgs.minio # S3-compatible storage for integration tests
            pkgs.minio-client # mc CLI for MinIO setup
            pkgs.postgresql # PostgreSQL for integration tests
            pkgs.python3 # used for generating the ports
            pkgs.redis # Redis for distributed locking integration tests
          ];

          doCheck = true;
          checkFlags = [
            "-race"
            "-coverprofile=coverage.txt"
          ];

          preCheck = ''
            # Set up cleanup trap to ensure background processes are killed even if tests fail
            cleanup() {
              source $src/nix/packages/ncps/post-check-minio.sh
              source $src/nix/packages/ncps/post-check-mysql.sh
              source $src/nix/packages/ncps/post-check-postgres.sh
              source $src/nix/packages/ncps/post-check-redis.sh
            }
            trap cleanup EXIT

            source $src/nix/packages/ncps/pre-check-minio.sh
            source $src/nix/packages/ncps/pre-check-mysql.sh
            source $src/nix/packages/ncps/pre-check-postgres.sh
            source $src/nix/packages/ncps/pre-check-redis.sh
          '';

          outputs = [
            "out"
            "coverage"
          ];

          postCheck = ''
            mv coverage.txt $coverage
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
