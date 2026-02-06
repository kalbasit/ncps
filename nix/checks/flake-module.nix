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
        (builtins.removeAttrs self'.packages [ "ncps" ])
        // {
          ncps = self'.packages.ncps.overrideAttrs (oa: {
            checkFlags = (oa.checkFlags or [ ]) ++ [ "-count=5" ];
          });
        }
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
