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
        config.packages.ncps.overrideAttrs (oa: {
          inherit name;
          outputs = [ "out" ];
          nativeBuildInputs = oa.nativeBuildInputs ++ cohortTestDeps;
          # The cohort doesn't need the ncps binary; skip the build phase.
          buildPhase = ''
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
            go test -race -count=1 -timeout 20m ./...
            runHook postCheck
          '';
          # No coverage output is produced here, so override the parent
          # postCheck which moves coverage.txt into the coverage output.
          postCheck = "";
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
      #   ncps-coverage      Exists so `nix build .#ncps.coverage`
      #                      resolves (CI codecov step). `nix flake
      #                      check` MUST NOT pay the ~12m coverage run
      #                      on every PR.
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

        # Per-backend Go test cohorts (see mkCohort above). Each runs
        # `go test -race ./...` with only its backend up; test bodies
        # gate per-backend subtests on NCPS_TEST_* env vars via
        # `t.Skip`.
        ncps-unit-tests = mkCohort {
          name = "ncps-unit-tests";
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
          vendorHash = "sha256-WPNzzt2cFj6nwpw1VymzsTkGx19ybyrl5RRGP5s7wj4=";
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
