{
  perSystem =
    {
      self',
      pkgs,
      ...
    }:
    {
      checks =
        self'.packages
        // self'.devShells
        // {
          golangci-lint-check = pkgs.stdenvNoCC.mkDerivation {
            name = "golangci-lint-check";
            src = ../../.;
            nativeBuildInputs = [
              pkgs.go
              pkgs.golangci-lint
            ];
            CGO_ENABLED = 0;
            buildPhase = ''
              HOME=$NIX_BUILD_TOP
              golangci-lint run --timeout 10m
            '';
            installPhase = ''
              touch $out
            '';
          };
        };
    };
}
