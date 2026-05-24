_: {
  perSystem =
    {
      self',
      pkgs,
      ...
    }:
    {
      # ncps-coverage merges the per-cohort `cover.out` profiles emitted by
      # the five cohort derivations under nix/checks/flake-module.nix into a
      # single coverage.txt for codecov upload. By piggybacking on work the
      # cohorts already do, this avoids a separate monolith test run that
      # would duplicate ~12 min of test execution.
      #
      # Output:
      #   coverage — the merged coverage.txt for codecov upload.
      #
      # packages.ncps.passthru.coverage points at this derivation's
      # `coverage` output so the shared CI workflow's
      # `nix build ".#ncps.coverage" -L` invocation (and its
      # `files: result-coverage` codecov step) keep working without
      # changes upstream at kalbasit/gh-actions.
      packages.ncps-coverage = pkgs.stdenvNoCC.mkDerivation {
        name = "ncps-coverage";
        outputs = [
          "out"
          "coverage"
        ];

        # No source: the merger reads coverage profiles from cohort outputs
        # listed in buildInputs below.
        dontUnpack = true;
        dontConfigure = true;

        nativeBuildInputs = [ pkgs.python3 ];

        # Depending on the cohort outputs is enough; we read their cover.out
        # by path in buildPhase.
        buildInputs = [
          self'.checks.ncps-unit-tests.coverage
          self'.checks.ncps-s3-tests.coverage
          self'.checks.ncps-postgres-tests.coverage
          self'.checks.ncps-mysql-tests.coverage
          self'.checks.ncps-redis-tests.coverage
        ];

        buildPhase = ''
          runHook preBuild

          # Merge the five per-cohort cover.out files. Go coverage text
          # format: one header line, then rows of
          #   <file>:<range> <block_count> <hit_count>
          # Identical <file>:<range> entries appear across cohorts because
          # every cohort compiles the same code; the merge sums hit_count
          # and preserves block_count. The script is invoked with the five
          # cover.out paths as positional args and writes the merged
          # profile to stdout, which we redirect to merged.cov.
          python3 ${./merge_coverage.py} \
            ${self'.checks.ncps-unit-tests.coverage}/cover.out \
            ${self'.checks.ncps-s3-tests.coverage}/cover.out \
            ${self'.checks.ncps-postgres-tests.coverage}/cover.out \
            ${self'.checks.ncps-mysql-tests.coverage}/cover.out \
            ${self'.checks.ncps-redis-tests.coverage}/cover.out \
            >merged.cov

          runHook postBuild
        '';

        installPhase = ''
          runHook preInstall
          mkdir -p $coverage
          mv merged.cov $coverage/coverage.txt
          touch $out
          runHook postInstall
        '';
      };
    };
}
