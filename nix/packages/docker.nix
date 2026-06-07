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
            # NOTE: /etc/passwd and /etc/group are intentionally NOT placed in the
            # buildEnv below. buildEnv combines paths by symlinking, which makes
            # them absolute symlinks into the nix store in the layered image
            # (e.g. /etc/passwd -> /nix/store/...-passwd/etc/passwd). Recent
            # container runtimes securejoin/openat etc/passwd during container
            # creation and reject the absolute symlink as escaping the rootfs
            # ("openat etc/passwd: path escapes from parent"), so the container
            # never starts. They are materialized as REAL files via
            # `extraCommands` below instead. See change fix-docker-etc-passwd.

            # Wrap all your contents in a buildEnv to assert disallowedRequisites
            # securely before they are processed by the Docker tools.
            imageContents =
              (pkgs.buildEnv {
                name = "ncps-contents";
                paths = [
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

        # Materialize /etc/passwd and /etc/group as REAL files (not store
        # symlinks) so the container starts on runtimes that strictly securejoin
        # etc/passwd during container creation. Content matches the previous
        # writeTextFile entries (root + ncps uid/gid 1000). Done under fakeroot
        # (in the assembled rootfs) so the writes succeed regardless of the
        # contents layer's directory permissions. These commands run at
        # image-build time only; the tools do not enter the image closure, so
        # the disallowedRequisites guard on imageContents still holds.
        fakeRootCommands = ''
          #!${pkgs.runtimeShell}
          mkdir -p tmp
          chmod -R 1777 tmp

          # /etc arrives from the contents layer as a symlink into the
          # read-only nix store (e.g. cacert contributes /etc/ssl), so we cannot
          # create files in it directly — and fakeroot only fakes ownership, not
          # real write perms. Re-materialize /etc as a writable real directory,
          # preserving its existing entries (kept as symlinks via `cp -a`), then
          # add /etc/passwd and /etc/group as REAL files. Copy *through* the
          # symlink (`etc/.`) rather than resolving it with readlink, so a failed
          # readlink can never expand to `cp -a /. etc/` (copying the sandbox root).
          if [ -L etc ]; then
            mkdir etc.tmp
            cp -a etc/. etc.tmp/
            rm etc
            mv etc.tmp etc
          else
            mkdir -p etc
          fi
          chmod -R u+w etc
          printf '%s\n' \
            'root:x:0:0:Super User:/homeless-shelter:/dev/null' \
            'ncps:x:1000:1000:NCPS:/homeless-shelter:/dev/null' \
            > etc/passwd
          printf '%s\n' \
            'root:x:0:' \
            'ncps:x:1000:' \
            > etc/group
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
