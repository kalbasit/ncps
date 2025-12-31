{
  perSystem =
    { pkgs, ... }:
    {
      packages.dbmate-wrapper = pkgs.buildGoModule {
        name = "dbmate-wrapper";
        src = ./src;

        vendorHash = null;

        meta = {
          description = "Wrapper for dbmate that auto-detects migrations directory from database URL";
          mainProgram = "dbmate-wrapper";
        };
      };
    };
}
