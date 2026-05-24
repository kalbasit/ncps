_: {
  perSystem =
    {
      config,
      pkgs,
      ...
    }:
    {
      # ncps-coverage is the test-running variant of packages.ncps. It runs
      # the full Go test suite (`go test -race -coverprofile=coverage.txt
      # ./...`) with all four integration backends (Garage, Postgres,
      # MariaDB, Redis) spun up in preCheck — the same scaffold the
      # monolithic packages.ncps used before Phase 5.
      #
      # Two outputs:
      #   out      — empty sentinel
      #   coverage — the merged coverage.txt for codecov upload
      #
      # packages.ncps.passthru.coverage points at this derivation's
      # `coverage` output so the shared CI workflow's
      # `nix build ".#ncps.coverage" -L` invocation (and its
      # `files: result-coverage` codecov step) continue to work without
      # changes upstream at kalbasit/gh-actions.
      #
      # This derivation is NOT in the `checks` attrset — `nix flake check`
      # does not pay for coverage instrumentation. Per-backend test
      # execution lives in the cohort derivations under
      # nix/checks/flake-module.nix; ncps-coverage exists only so codecov
      # keeps seeing a single merged profile covering the same packages as
      # before the topology change.
      packages.ncps-coverage = config.packages.ncps.overrideAttrs (oa: {
        name = "ncps-coverage";

        # Re-introduce the integration backends and helpers that the lean
        # packages.ncps drops. preCheck sources scripts that drive these
        # binaries to spin up Garage, MariaDB, PostgreSQL, and Redis inside
        # the build sandbox.
        nativeBuildInputs = oa.nativeBuildInputs ++ [
          pkgs.awscli2 # init-garage smoke test (put/get/presign)
          pkgs.curl # HTTP health checks and anonymous-access check
          pkgs.garage # S3-compatible storage for integration tests
          pkgs.jq # init-garage validation
          pkgs.mariadb # MySQL/MariaDB integration tests
          pkgs.postgresql # PostgreSQL integration tests
          pkgs.python3 # generating ephemeral ports
          pkgs.redis # Redis distributed-lock integration tests
        ];

        doCheck = true;
        checkFlags = [
          "-race"
          "-coverprofile=coverage.txt"
        ];

        preCheck = ''
          # Cleanup trap kills background services even on failure.
          cleanup() {
            source $src/nix/packages/ncps/post-check-garage.sh
            source $src/nix/packages/ncps/post-check-mysql.sh
            source $src/nix/packages/ncps/post-check-postgres.sh
            source $src/nix/packages/ncps/post-check-redis.sh
          }
          trap cleanup EXIT

          source $src/nix/packages/ncps/pre-check-garage.sh
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

        # packages.ncps' passthru carries a `coverage` attribute that
        # references THIS derivation; carrying it through overrideAttrs
        # would cycle. Drop just that one attr; preserve everything else
        # (notably buildGoModule's overrideModAttrs helper).
        passthru = removeAttrs (oa.passthru or { }) [ "coverage" ];
      });
    };
}
