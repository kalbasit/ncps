{
  perSystem =
    { pkgs, ... }:
    {
      packages.gen-db-wrappers = pkgs.buildGoModule {
        name = "gen-db-wrappers";
        src = ./src;

        vendorHash = "sha256-dWDZ7LJq5y9c34Pi/PluiIQ363a5Ph0bcLq7jv+7Pbc=";

        meta = {
          description = "Generate database wrappers based on the Querier interface";
          mainProgram = "gen-db-wrappers";
        };
      };
    };
}
