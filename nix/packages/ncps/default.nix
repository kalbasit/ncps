{ self, ... }:
{
  perSystem =
    {
      config,
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

          vendorHash = "sha256-ubKcWD80jnH8UyCiXUdf/m2ddLJhzybXCEtyA1NEWiw=";
        in
        pkgs.buildGoModule {
          inherit version vendorHash;

          pname = "ncps";

          src = lib.fileset.toSource {
            fileset = lib.fileset.unions [
              ./post-check-garage.sh
              ./pre-check-garage.sh

              ./post-check-mysql.sh
              ./pre-check-mysql.sh

              ./post-check-postgres.sh
              ./pre-check-postgres.sh

              ./post-check-redis.sh
              ./pre-check-redis.sh

              # .golangci.yml lives in the source set so golangci-lint-check
              # can re-use this fileset (and the goModules cache) instead of
              # overriding src to the whole repo. Inert for the ncps build
              # itself.
              ../../../.golangci.yml
              ../../../cmd
              ../../../ent
              ../../../go.mod
              ../../../go.sum
              ../../../internal
              ../../../main.go
              ../../../migrations
              ../../../nix/process-compose/init-garage.sh
              ../../../nix/process-compose/init-mysql.sh
              ../../../nix/process-compose/init-postgres.sh
              ../../../nix/process-compose/postgres-dblink-create-drop-functions.sql
              ../../../nix/process-compose/start-garage.sh
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

          buildInputs = [
            pkgs.xz # required for xz decompression
          ];

          nativeBuildInputs = [
            pkgs.makeBinaryWrapper # used for wrapping the binary so it can always find the xz binary
          ];

          # Tests run in the per-backend cohort derivations under
          # nix/checks/flake-module.nix; coverage runs in packages.ncps-coverage
          # (exposed via passthru.coverage below so `nix build .#ncps.coverage`
          # keeps working unchanged for the shared CI workflow at
          # kalbasit/gh-actions). The main ncps build is a pure binary build
          # — fast, cacheable, and what every downstream consumer of
          # `nix build .#ncps` actually wants.
          doCheck = false;

          postInstall = ''
            # ncps makes use of xz for decompression as it's 3-5x faster than
            # using the native Go implementation of xz. By wrapping ncps, and
            # setting the XZ_BINARY_PATH environment variable, we ensure that
            # ncps can always find the xz binary. This environment variable is
            # read by a flag in pkg/ncps and can be overriden by using calling
            # ncps with the --xz-binary-path flag.
            wrapProgram $out/bin/ncps --set XZ_BINARY_PATH ${lib.getExe' pkgs.xz "xz"}
          '';

          # Expose the coverage output of packages.ncps-coverage as `.coverage`
          # on this derivation. The shared CI workflow at
          # kalbasit/gh-actions/.github/workflows/build.yml invokes
          # `nix build ".#${PRIMARY_PACKAGE}.coverage" -L` and consumes
          # `result-coverage`; with this passthru that invocation continues
          # to resolve to a multi-output derivation with a `coverage` output,
          # so the symlink name and codecov payload shape are preserved.
          passthru = {
            inherit (config.packages.ncps-coverage) coverage;
          };

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
