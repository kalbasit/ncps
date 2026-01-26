{
  perSystem =
    {
      config,
      lib,
      pkgs,
      ...
    }:
    let

      package-ncps = config.packages.ncps.overrideAttrs (oa: {
        checkFlags = lib.subtractLists [
          # Remove coverage since it's not going to test everything (see preCheck disabling below).
          "-coverprofile=coverage.txt"

          # Remove race-condition testing as it does not work on all platforms.
          "-race"
        ] (oa.checkFlags or [ ]);

        # Since we are not running the coverage tests, disable coverage output
        outputs = lib.remove "coverage" (oa.outputs or [ ]);

        # No need to run integration tests for the docker image, they are
        # covered by the other tests. Additionally, there seems to be a
        # incompatibility issue between Redis and GitHub actions that is
        # blocking the image build:
        # WARNING Your kernel has a bug that could lead to data corruption
        # during background save. Please upgrade to the latest stable kernel.
        # Redis will now exit to prevent data corruption. Note that it is
        # possible to suppress this warning by setting the following config:
        # ignore-warnings ARM64-COW-BUG
        preCheck = "";
        postCheck = "";
      });
    in
    {
      packages.docker = pkgs.dockerTools.buildLayeredImage {
        name = "kalbasit/ncps";

        contents =
          let
            etc-passwd = pkgs.writeTextFile {
              name = "passwd";
              text = ''
                root:x:0:0:Super User:/homeless-shelter:/dev/null
                ncps:x:1000:1000:NCPS:/homeless-shelter:/dev/null
              '';
              destination = "/etc/passwd";
            };

            etc-group = pkgs.writeTextFile {
              name = "group";
              text = ''
                root:x:0:
                ncps:x:1000:
              '';
              destination = "/etc/group";
            };
          in
          [
            # required for Open-Telemetry auto-detection of process information
            etc-passwd
            etc-group

            # required for TLS certificate validation
            pkgs.cacert

            # required for working with timezones
            pkgs.tzdata

            # required for migrating the database
            # Use real dbmate for the wrapper to call
            (pkgs.writeShellScriptBin "dbmate.real" ''
              exec ${pkgs.dbmate}/bin/dbmate "$@"
            '')
            # dbmate-wrapper provides the dbmate command
            (pkgs.writeShellScriptBin "dbmate" ''
              exec ${config.packages.dbmate-wrapper}/bin/dbmate-wrapper "$@"
            '')

            # the ncps package
            package-ncps
          ];

        config = {
          Cmd = [ "/bin/ncps" ];
          Env = [
            # NCPS_DB_MIGRATIONS_DIR tells dbmate-wrapper where to find migrations
            "NCPS_DB_MIGRATIONS_DIR=/share/ncps/db/migrations"

            # NCPS_DB_SCHEMA_DIR tells dbmate-wrapper where to find schema files
            "NCPS_DB_SCHEMA_DIR=/share/ncps/db/schema"

            # Instruct dbmate not to migrate the database
            "DBMATE_NO_DUMP_SCHEMA=true"

            # XXX: It's important not to set these variables in order to
            # support multiple database engines.
            # DBMATE_MIGRATIONS_DIR is set dynamically by dbmate-wrapper based on --url
            # DBMATE_SCHEMA_FILE is set dynamically by dbmate-wrapper based on --url
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
          Volumes = {
            "/storage" = { };
          };
        };

        fakeRootCommands = ''
          #!${pkgs.runtimeShell}
          mkdir -p tmp
          chmod -R 1777 tmp
        '';
      };

      packages.push-docker-image = pkgs.writeShellScriptBin "push-docker-image" ''
        set -euo pipefail

        if [[ ! -v DOCKER_IMAGE_TAGS ]]; then
          echo "DOCKER_IMAGE_TAGS is not set but is required." >&2
          exit 1
        fi

        for tag in $DOCKER_IMAGE_TAGS; do
          echo "Pushing the image tag $tag for system ${pkgs.hostPlatform.system}. final tag: $tag-${pkgs.hostPlatform.system}"
          ${pkgs.skopeo}/bin/skopeo --insecure-policy copy \
            "docker-archive:${config.packages.docker}" docker://$tag-${pkgs.hostPlatform.system}
        done
      '';
    };
}
