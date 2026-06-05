_: {
  perSystem =
    {
      lib,
      pkgs,
      ...
    }:
    {
      # ncps-checktools bundles the small in-tree helper binaries that
      # several flake-check derivations exec. Building them once here lets
      # the per-check derivations consume the binaries from this output
      # via `stdenvNoCC.mkDerivation`, instead of each overriding the
      # main `ncps` package and paying a fresh `buildGoModule` invocation
      # to compile a few-hundred-line tool.
      #
      # Add a new tool by extending `subPackages` (and the fileset). The
      # binaries are installed under `$out/bin/<name>`.
      packages.ncps-checktools = pkgs.buildGoModule {
        pname = "ncps-checktools";
        version = "unstable";

        # Narrow source: only the two helper binaries plus the project's
        # go.mod / go.sum. Neither helper imports any internal ncps
        # package today, so we don't ship pkg/, internal/, etc. — keeping
        # the fixed-output goModules derivation small and stable across
        # unrelated edits.
        src = lib.fileset.toSource {
          fileset = lib.fileset.unions [
            ../../../cmd/ent-lint
            ../../../cmd/atlas-sum-check
            ../../../go.mod
            ../../../go.sum
          ];
          root = ../../..;
        };

        # Distinct hash from packages.ncps because the source set is
        # narrower; the module dependency list is still everything in
        # go.mod (Go module-mode pulls all required modules), but the
        # source tree hashed here is smaller.
        vendorHash = "sha256-32ZnCq3wVNDfaEopKbSBguO0C6TS4fqEhxvYLHsq3V4=";

        subPackages = [
          "cmd/ent-lint"
          "cmd/atlas-sum-check"
        ];

        # No tests in this derivation. The helper binaries' own tests
        # (e.g. cmd/ent-lint/main_test.go) run as part of the broader
        # ncps test suite, where the testdata directory is in scope.
        doCheck = false;

        meta = {
          description = "Static-analysis helper binaries for the ncps repo";
          license = lib.licenses.mit;
        };
      };
    };
}
