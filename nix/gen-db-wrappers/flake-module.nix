{
  perSystem =
    { pkgs, ... }:
    {
      packages.gen-db-wrappers = pkgs.buildGoModule {
        name = "gen-db-wrappers";
        src = ./src;

        vendorHash = "sha256-EsZuN5MPT1bTImjkcyNd5sxy7srSS0JlDJiGGfpEhtM=";

        meta = {
          description = "Generate database wrappers based on the Querier interface";
          mainProgram = "gen-db-wrappers";
        };
      };
    };
}
