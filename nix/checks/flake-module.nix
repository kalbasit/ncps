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
            nativeBuildInputs = [ pkgs.golangci-lint ];
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
