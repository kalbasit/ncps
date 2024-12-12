{
  perSystem =
    {
      config,
      pkgs,
      ...
    }:
    {
      packages.docker = pkgs.dockerTools.buildImage {
        name = "ncps";
        tag = "latest";
        copyToRoot = [
          config.packages.ncps
          pkgs.dbmate
        ];
        config = {
          Cmd = [ "/bin/ncps" ];
        };
      };
    };
}
