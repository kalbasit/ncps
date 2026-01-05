{
  perSystem =
    { pkgs, ... }:
    {
      packages.gen-db-wrappers = pkgs.buildGoModule {
        name = "gen-db-wrappers";
        src = ./src;

        vendorHash = null;

        meta = {
          description = "Generate database wrappers based on the Querier interface";
          mainProgram = "gen-db-wrappers";
        };
      };
    };
}
