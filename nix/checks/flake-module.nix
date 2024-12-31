{
  perSystem =
    {
      self',
      lib,
      pkgs,
      config,
      ...
    }:
    {
      checks =
        let
          packages = lib.mapAttrs' (n: lib.nameValuePair "package-${n}") self'.packages;
          devShells = lib.mapAttrs' (n: lib.nameValuePair "devShell-${n}") self'.devShells;
        in
        packages
        // devShells
        // {
          package-ncps = packages."package-ncps".overrideAttrs (oa: {
            # The testdata and testhelper packages are not used by the main
            # package but do (could) have tests. Have the check include them to
            # ensure they pass should they have tests.
            subPackages = oa.subPackages ++ [
              "testdata"
              "testhelper"
            ];
          });

          golangci-lint = config.packages.ncps.overrideAttrs (oa: {
            name = "golangci-lint";
            src = ../../.;
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
        };
    };
}
