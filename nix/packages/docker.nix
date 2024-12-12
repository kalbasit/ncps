{
  perSystem =
    {
      config,
      pkgs,
      ...
    }:
    {
      packages.docker = pkgs.dockerTools.buildLayeredImage {
        name = "kalbasit/ncps";
        tag = "latest";
        contents = [
          pkgs.dbmate

          config.packages.ncps
        ];
        config = {
          Cmd = [ "/bin/ncps" ];
          Env = [
            "DBMATE_MIGRATIONS_DIR=/share/ncps/db/migrations"
            "DBMATE_NO_DUMP_SCHEMA=true"
          ];
          ExposedPorts = {
            "8501/tcp" = { };
          };
        };
      };
    };
}
