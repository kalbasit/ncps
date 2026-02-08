{
  perSystem =
    { pkgs, ... }:
    {
      packages.gen-db-wrappers = pkgs.buildGoModule {
        name = "gen-db-wrappers";
        src = ./src;

        vendorHash = "sha256-KUP/icbmfapTZSRV+GeAqZw6vMCkrcmse+WhbM8yi78=";

        meta = {
          description = "Generate database wrappers based on the Querier interface";
          mainProgram = "gen-db-wrappers";
        };
      };
    };
}
