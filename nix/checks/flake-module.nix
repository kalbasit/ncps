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
