{
  perSystem =
    {
      lib,
      pkgs,
      config,
      ...
    }:
    let
      # mkCohort builds a backend-cohort test derivation: a stripped-down
      # variant of packages.ncps that runs `go test -race ./...` with only
      # one external backend up (Garage, Postgres, MariaDB, or Redis), or
      # none for the unit cohort. Cohort membership is selected at runtime
      # by the env vars each pre-check-*.sh script exports — the test
      # bodies already gate per-backend subtests via `t.Skip` on the
      # corresponding NCPS_TEST_* env var. Spec: flake-check-topology
      # "Backend-cohort granularity for integration test derivations" and
      # "Env-var presence drives integration-cohort membership".
      #
      # Args:
      #   name     — derivation name and the attr it gets bound to
      #   backends — list of backend slugs ("garage", "postgres", "mysql",
      #              "redis"). Each one MUST have matching
      #              pre-check-<slug>.sh / post-check-<slug>.sh scripts
      #              under nix/packages/ncps/.
      # Tools the pre-check-*.sh scripts need: python3 generates ephemeral
      # ports for every backend, and each backend binary is required to
      # actually start that backend. packages.ncps no longer carries these
      # (Phase 5 stripped them when ncps lost its tests); re-introduce them
      # at the cohort layer so the preCheck plumbing works inside the build
      # sandbox.
      cohortTestDeps = with pkgs; [
        awscli2 # init-garage smoke test (put/get/presign)
        curl # HTTP health checks
        garage # S3-compatible storage
        jq # init-garage validation
        mariadb # MySQL/MariaDB integration tests
        postgresql # PostgreSQL integration tests
        python3 # ephemeral port allocation
        redis # Redis distributed-lock integration tests
      ];

      mkCohort =
        {
          name,
          backends,
        }:
        # Cohorts build from packages._ncps-base (a passthru-free buildGoModule
        # with the shared src + vendorHash) rather than packages.ncps. This
        # avoids the cycle that would otherwise form: ncps-coverage depends on
        # the cohorts → cohorts depend on the base → if the base were
        # packages.ncps, ncps's passthru.coverage points back to ncps-coverage.
        config.packages._ncps-base.overrideAttrs (oa: {
          inherit name;
          # Two outputs: an empty `out` sentinel and `coverage`, which
          # holds this cohort's `cover.out` profile. ncps-coverage merges
          # these per-cohort profiles into a single coverage.txt for
          # codecov upload, avoiding a separate monolith test run.
          outputs = [
            "out"
            "coverage"
          ];
          # Append the backend tools to whatever the base derivation already
          # has (the Go toolchain etc.).
          nativeBuildInputs = oa.nativeBuildInputs ++ cohortTestDeps;
          # The cohort doesn't need the ncps binary; skip the buildPhase
          # except for the cmd cohort, which uses it to pre-build
          # cmd/generate-migrations so its test doesn't pay an in-test
          # `go build` that has been observed taking >180 s on aarch64 CI.
          # The path is passed to the test via NCPS_TEST_GENERATE_MIGRATIONS_BIN
          # in checkPhase below; see cmd/generate-migrations/main_test.go.
          buildPhase =
            if backends == [ ] then
              ''
                runHook preBuild
                mkdir -p $TMPDIR/cohort-bin
                go build -o $TMPDIR/cohort-bin/generate-migrations ./cmd/generate-migrations
                export NCPS_TEST_GENERATE_MIGRATIONS_BIN=$TMPDIR/cohort-bin/generate-migrations
                runHook postBuild
              ''
            else
              ''
                :
              '';
          preCheck =
            if backends == [ ] then
              # Unit cohort: no env vars, no backends. All integration
              # subtests skip at runtime via their NCPS_TEST_* gates.
              ""
            else
              ''
                cleanup() {
                ${lib.concatMapStringsSep "\n" (b: ''source "$src/nix/packages/ncps/post-check-${b}.sh"'') backends}
                }
                trap cleanup EXIT

                ${lib.concatMapStringsSep "\n" (b: ''source "$src/nix/packages/ncps/pre-check-${b}.sh"'') backends}
              '';
          checkPhase = ''
            runHook preCheck
            # Test path + coverage scope selection:
            #   Backend cohorts (s3, postgres, mysql, redis) test AND
            #   instrument the trees that contain integration tests
            #   (pkg/, internal/, migrations/, testhelper/). The -coverpkg
            #   scope matches the test paths so we don't pay the race-
            #   detector tax instrumenting packages whose tests aren't run.
            #
            #   The "cmd cohort" (a degenerate `backends = []` cohort with a
            #   testPaths override below) covers cmd/*, ent/*, main package.
            #   These tests don't gate on any backend env var, so running
            #   them in every cohort was 5x redundant work and was blowing
            #   the 180 s build timeout in cmd/generate-migrations under
            #   parallel `go build` contention on CI.
            #
            #   The merged coverage profile (assembled by ncps-coverage from
            #   per-cohort cover.out) covers the full code base: cmd cohort
            #   contributes cmd/+ent/+main, backend cohorts contribute their
            #   pkg/* etc. hits, and the merger sums hit counts for keys
            #   that appear in both.
            #
            # -covermode=atomic is required when combined with -race.
            ${
              if backends == [ ] then
                # cmd cohort: cover cmd/, ent/, main package only.
                "go test -race -count=1 -timeout 20m -coverprofile=cover.out -covermode=atomic -coverpkg=./cmd/...,./ent/...,. ./cmd/... ./ent/... ."
              else
                "go test -race -count=1 -timeout 20m -coverprofile=cover.out -covermode=atomic -coverpkg=./pkg/...,./internal/...,./migrations/...,./testhelper/... ./pkg/... ./internal/... ./migrations/... ./testhelper/..."
            }
            runHook postCheck
          '';
          # Stage cover.out into the coverage output.
          postCheck = ''
            mkdir -p $coverage
            mv cover.out $coverage/cover.out
          '';
          doCheck = true;
          installPhase = ''
            touch $out
          '';
        });
    in
    {
      # `checks` is an explicit enumeration of quality-gate derivations
      # — see the `flake-check-topology` spec, "The `checks` attrset is
      # an explicit enumeration" requirement.
      #
      # Excluded on purpose (packages that ARE buildable as
      # `nix build .#<name>` but are not gates):
      #
      #   ncps-coverage      Now a tiny merger of per-cohort cover.out
      #                      profiles (~30 s) rather than a monolith
      #                      test run. The CI build job builds it
      #                      via `nix build .#ncps.coverage` after the
      #                      cohort outputs are cached, which costs
      #                      seconds. Adding it to `checks` would
      #                      also cycle (cohorts → ncps base →
      #                      ncps.passthru.coverage → ncps-coverage →
      #                      cohorts).
      #   docker, docker-dev Runtime images; CI builds them explicitly
      #                      after flake check, validated downstream.
      #   push-docker-image  CLI wrapper for the CI image push step.
      #   deps               Process-compose dev dependencies, for
      #                      `nix run .#deps`.
      #   k8s-tests          CLI tool for local Kind testing.
      #   update-cu-base     CLI tool for the container-use base image.
      #   treefmt            Formatter devShell; `nix fmt` exercises it.
      #   default            Alias of packages.ncps, listed below as `ncps`.
      checks = {
        # Binary build (no tests). Proves the source tree compiles and
        # the vendor hash is current. Cheap (~1m). Sits at the top of
        # every other Go-using check's dependency graph.
        inherit (config.packages) ncps;

        # ent-lint + atlas-sum-check helper binaries compile cleanly.
        # Building here surfaces toolchain regressions before the two
        # tiny stdenvNoCC checks that consume them.
        inherit (config.packages) ncps-checktools;

        # Per-backend Go test cohorts (see mkCohort above). Each backend
        # cohort starts its backend and runs `go test ./pkg/... ./internal/...
        # ./migrations/... ./testhelper/...`; test bodies gate per-backend
        # subtests on NCPS_TEST_* env vars via `t.Skip`.
        #
        # The cmd cohort is a degenerate "no backends" cohort that covers
        # ./cmd/..., ./ent/..., and the main package — paths that don't
        # gate on any backend env var. Runs in parallel with the backend
        # cohorts; eliminates the redundancy of having a full unit cohort
        # that re-runs all the pkg/* unit tests the backend cohorts already
        # exercise.
        ncps-cmd-tests = mkCohort {
          name = "ncps-cmd-tests";
          backends = [ ];
        };
        ncps-s3-tests = mkCohort {
          name = "ncps-s3-tests";
          backends = [ "garage" ];
        };
        ncps-postgres-tests = mkCohort {
          name = "ncps-postgres-tests";
          backends = [ "postgres" ];
        };
        ncps-mysql-tests = mkCohort {
          name = "ncps-mysql-tests";
          backends = [ "mysql" ];
        };
        ncps-redis-tests = mkCohort {
          name = "ncps-redis-tests";
          backends = [ "redis" ];
        };

        # golangci-lint-check inherits src and vendorHash from packages.ncps
        # via overrideAttrs, so it shares the fixed-output goModules
        # derivation with the main package — the Go module cache is
        # populated once and re-used across both builds. .golangci.yml is
        # carried in packages.ncps' fileset so the linter finds its
        # configuration without needing the full repo as src.
        golangci-lint-check = config.packages.ncps.overrideAttrs (oa: {
          name = "golangci-lint-check";
          # ensure the output is only out since it's the only thing this package does.
          outputs = [ "out" ];
          nativeBuildInputs = oa.nativeBuildInputs ++ [ pkgs.golangci-lint ];
          buildPhase = ''
            HOME=$TMPDIR
            golangci-lint run --timeout 10m
          '';
          installPhase = ''
            touch $out
          '';
          doCheck = false;
        });

        # ent-codegen-drift-check verifies that the committed Ent codegen
        # output under ./ent matches what `go generate ./ent/...` would
        # produce from the current ent/schema/*.go files. Fails the build
        # if any file under ./ent differs after regeneration.
        #
        # The drift check uses `proxyVendor = true` so the Go module proxy
        # cache is populated with *all* module dependencies (including the
        # `tool` directive's `entgo.io/ent/cmd/ent`), then runs `go generate`
        # in module-mode against that cache. The default `buildGoModule`
        # vendor-mode setup is unusable here because Ent's tool dependency
        # is intentionally not vendored.
        #
        # Because proxyVendor mode produces a different fixed-output
        # goModules derivation than packages.ncps' vendor-mode build, this
        # check carries its own vendorHash and cannot share the module
        # cache with the rest of the flake. Override src to the whole repo
        # because the drift check needs all files for the in-build git
        # init/diff.
        ent-codegen-drift-check = config.packages.ncps.overrideAttrs (oa: {
          name = "ent-codegen-drift-check";
          src = ../../.;
          outputs = [ "out" ];
          proxyVendor = true;
          vendorHash = "sha256-PztQfsWQwmvfwYRuWRo1FWGh6YO8v2R6xYZMYaxxZyk=";
          nativeBuildInputs = oa.nativeBuildInputs ++ [ pkgs.git ];
          buildPhase = ''
            HOME=$TMPDIR

            # Materialize the source tree into a writable copy and turn it
            # into a git repository so `git diff --exit-code` has something
            # to compare against. buildGoModule's $src is read-only.
            cp -r $src ./repo
            chmod -R u+w ./repo
            cd ./repo

            git init --quiet
            git add -A
            git -c user.email=ci@example.invalid -c user.name=ci \
              commit --quiet -m baseline

            # Regenerate Ent code using the proxy module cache populated by
            # buildGoModule (GOPROXY/GOFLAGS are set by the wrapper).
            go generate ./ent/...

            if ! git diff --exit-code ./ent/; then
              echo "ent/ codegen is out of date — run 'go generate ./ent/...' and commit the result." >&2
              exit 1
            fi
          '';
          installPhase = ''
            touch $out
          '';
          doCheck = false;
        });

        # atlas-sum-check verifies that the atlas.sum file in each
        # migrations/<dialect>/ directory matches the directory's
        # recomputed checksum. Drift indicates a hand-edit of a tracked
        # migration without regenerating atlas.sum, which would break
        # Atlas's replay validator.
        #
        # The binary itself ships in `packages.ncps-checktools`; this
        # check just runs it against a narrow src containing only the
        # migrations tree. No Go toolchain needed at check time.
        atlas-sum-check = pkgs.stdenvNoCC.mkDerivation {
          name = "atlas-sum-check";
          src = lib.fileset.toSource {
            fileset = ../../migrations;
            root = ../..;
          };
          dontUnpack = false;
          dontConfigure = true;
          dontBuild = false;
          nativeBuildInputs = [ config.packages.ncps-checktools ];
          buildPhase = ''
            runHook preBuild
            atlas-sum-check --root .
            runHook postBuild
          '';
          installPhase = ''
            runHook preInstall
            touch $out
            runHook postInstall
          '';
        };

        # ent-lint-check runs cmd/ent-lint against the Ent schema tree and
        # fails if any [FAIL] line appears in the output. Same pattern as
        # atlas-sum-check: pre-built helper from `packages.ncps-checktools`,
        # narrow src (only the ent schema tree).
        ent-lint-check = pkgs.stdenvNoCC.mkDerivation {
          name = "ent-lint-check";
          src = lib.fileset.toSource {
            fileset = ../../ent;
            root = ../..;
          };
          dontUnpack = false;
          dontConfigure = true;
          dontBuild = false;
          nativeBuildInputs = [ config.packages.ncps-checktools ];
          buildPhase = ''
            runHook preBuild
            # pipefail so a non-[FAIL] ent-lint exit (crash, signal, non-zero
            # without diagnostics) still fails the build instead of being
            # masked by tee's exit code.
            set -o pipefail
            # Capture output so we can both display it and grep for FAIL.
            ent-lint --root . | tee ent-lint.out

            if grep -q '^\[FAIL\]' ent-lint.out; then
              echo "ent-lint reported invariant violations — see [FAIL] lines above." >&2
              exit 1
            fi
            runHook postBuild
          '';
          installPhase = ''
            runHook preInstall
            touch $out
            runHook postInstall
          '';
        };

        # Helm chart unit tests via helm-unittest.
        helm-unittest-check = pkgs.stdenvNoCC.mkDerivation {
          name = "ncps-helm-unittest";
          src = ../../charts/ncps;
          nativeBuildInputs = [
            (pkgs.wrapHelm pkgs.kubernetes-helm {
              plugins = [ pkgs.kubernetes-helmPlugins.helm-unittest ];
            })
          ];
          buildPhase = ''
            helm unittest .
          '';
          installPhase = ''
            touch $out
          '';
        };
      };
    };
}
