{
  perSystem =
    {
      self',
      pkgs,
      config,
      ...
    }:
    {
      checks =
        self'.packages
        // self'.devShells
        // {
          # TODO: Simplify this to not use buildGoModule as it seems to be a
          # waste of time. This could be a simple stdenvNoCC.mkDerviation.
          golangci-lint-check = config.packages.ncps.overrideAttrs (oa: {
            name = "golangci-lint-check";
            src = ../../.;
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
          ent-codegen-drift-check = config.packages.ncps.overrideAttrs (oa: {
            name = "ent-codegen-drift-check";
            src = ../../.;
            outputs = [ "out" ];
            proxyVendor = true;
            vendorHash = "sha256-aMu071MyAkeWzRSj84DrMBbvf8aDw5x83hjsPSz9FKo=";
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

          # ent-lint-check runs cmd/ent-lint against the Ent schema tree and
          # fails if any [FAIL] line appears in the output.
          ent-lint-check = config.packages.ncps.overrideAttrs (_oa: {
            name = "ent-lint-check";
            src = ../../.;
            outputs = [ "out" ];
            buildPhase = ''
              HOME=$TMPDIR

              # Build the lint binary, then run it against the schema tree.
              # Capture output so we can both display it and grep for FAIL.
              go build -o ./ent-lint ./cmd/ent-lint
              ./ent-lint --root . | tee ent-lint.out

              if grep -q '^\[FAIL\]' ent-lint.out; then
                echo "ent-lint reported invariant violations — see [FAIL] lines above." >&2
                exit 1
              fi
            '';
            installPhase = ''
              touch $out
            '';
            doCheck = false;
          });

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
