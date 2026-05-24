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

            # Wrap all your contents in a buildEnv to assert disallowedRequisites
            # securely before they are processed by the Docker tools.
            imageContents =
              (pkgs.buildEnv {
                name = "ncps-contents";
                paths = [
                  # required for Open-Telemetry auto-detection of process information
                  etc-passwd
                  etc-group

                  # required for TLS certificate validation
                  pkgs.cacert

                  # required for working with timezones
                  # Filtered tzdata to drop the bash dependency
                  (pkgs.runCommand "tzdata-filtered" { } ''
                    mkdir -p $out/share
                    cp -a ${pkgs.tzdata}/share/zoneinfo $out/share/
                  '')

                  # the ncps package. packages.ncps now has doCheck = false
                  # and a single `out` output by default (Phase 5 of
                  # openspec/changes/lean-flake-check), so no overrideAttrs
                  # is needed here to strip tests or extra outputs.
                  config.packages.ncps
                ];
              }).overrideAttrs
                (_: {
                  # Nix will fail the build if any of these sneak into the closure recursively
                  disallowedRequisites = [
                    # no tools
                    pkgs.coreutils

                    # no shell
                    pkgs.bash
                    pkgs.bashInteractive
                    pkgs.busybox
                    pkgs.dash
                    pkgs.stdenv.shellPackage
                    pkgs.zsh
                  ];
                });
          in
          [ imageContents ];

        config = {
          Cmd = [ "/bin/ncps" ];
          Env = [ ];
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
