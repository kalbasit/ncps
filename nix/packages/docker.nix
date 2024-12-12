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
          Labels = {
            "org.opencontainers.image.description" =
              "Nix binary cache proxy service -- with local caching and signing";
            "org.opencontainers.image.licenses" = "MIT";
            "org.opencontainers.image.source" = "https://github.com/kalbasit/ncps";
            "org.opencontainers.image.title" = "ncps";
            "org.opencontainers.image.url" = "https://github.com/kalbasit/ncps";
          };
        };
      };

      packages.push-docker-image = pkgs.writeShellScript "push-docker-image" ''
        set -euo pipefail

        if [[ ! -v DOCKER_IMAGE_TAGS ]]; then
          echo "DOCKER_IMAGE_TAGS is not set but is required." >&2
          exit 1
        fi

        for tag in $DOCKER_IMAGE_TAGS; do
          ${pkgs.skopeo}/bin/skopeo --insecure-policy copy \
            "docker-archive:${config.packages.docker}" docker://$tag
        done
      '';
    };
}
