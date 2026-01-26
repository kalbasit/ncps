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
        };
    };
}
