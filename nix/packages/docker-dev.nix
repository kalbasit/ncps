{
  perSystem =
    {
      pkgs,
      ...
    }:
    {
      packages.docker-dev = pkgs.dockerTools.buildLayeredImage {
        name = "kalbasit/ncps-dev";

        contents =
          let
            etc-passwd = pkgs.writeTextFile {
              name = "passwd";
              text = ''
                root:x:0:0:Super User:/root:/bin/sh
                ncps:x:1000:1000:NCPS:/home/ncps:/bin/sh
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

            # /tmp and /home/ncps dirs with correct permissions are created via
            # fakeRootCommands below; listed here so they appear in the PATH env.
            imageContents = pkgs.buildEnv {
              name = "ncps-dev-contents";
              paths = [
                etc-passwd
                etc-group

                pkgs.bash
                pkgs.coreutils
                pkgs.cacert
                pkgs.git # needed for shellHook git rev-parse calls
                pkgs.gcc
                pkgs.gnugrep
                pkgs.gnused
              ]
              ++ (import ../dev-packages.nix pkgs)
              ++ [
                (pkgs.python3.withPackages (
                  ps: with ps; [
                    psycopg2-binary
                    pymysql
                    boto3
                    zstandard
                    blake3
                  ]
                ))

              ];
            };
          in
          [ imageContents ];

        config = {
          User = "ncps";
          WorkingDir = "/workdir";
          Env = [
            "HOME=/home/ncps"
            "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
            # Point ncps at the mounted repo's migration/schema dirs
            # Integration test service endpoints — assumes `nix run .#deps` is running on the host.
            # host.docker.internal resolves to the Docker host from inside the container.
            "NCPS_TEST_S3_BUCKET=test-bucket"
            "NCPS_TEST_S3_ENDPOINT=http://host.docker.internal:9000"
            "NCPS_TEST_S3_REGION=us-east-1"
            "NCPS_TEST_S3_ACCESS_KEY_ID=test-access-key"
            "NCPS_TEST_S3_SECRET_ACCESS_KEY=test-secret-key"
            "NCPS_TEST_ADMIN_POSTGRES_URL=postgresql://test-user:test-password@host.docker.internal:5432/test-db?sslmode=disable"
            "NCPS_TEST_ADMIN_MYSQL_URL=mysql://test-user:test-password@host.docker.internal:3306/test-db"
            "NCPS_ENABLE_REDIS_TESTS=1"
            "NCPS_TEST_REDIS_ADDRS=host.docker.internal:6379"
          ];
          ExposedPorts = {
            "8501/tcp" = { };
          };
          Labels = {
            "org.opencontainers.image.description" = "ncps development container";
            "org.opencontainers.image.licenses" = "MIT";
            "org.opencontainers.image.source" = "https://github.com/kalbasit/ncps";
            "org.opencontainers.image.title" = "ncps";
            "org.opencontainers.image.url" = "https://github.com/kalbasit/ncps";
          };
        };

        fakeRootCommands = ''
          #!${pkgs.runtimeShell}
          mkdir -p tmp
          chmod 1777 tmp
          mkdir -p home/ncps
          chown 1000:1000 home/ncps
        '';

        enableFakechroot = true;
      };

      packages.update-cu-base = pkgs.writeShellApplication {
        name = "update-cu-base";
        runtimeInputs = [
          pkgs.nix
          pkgs.skopeo
          pkgs.jq
        ];
        text = ''
          set -euo pipefail

          TMP_FILE=""
          trap 'rm -f "$TMP_FILE"' EXIT

          # Determine the Linux target system (handles macOS cross-compilation)
          HOST_SYSTEM=$(nix eval --impure --expr 'builtins.currentSystem' --raw)
          case "$HOST_SYSTEM" in
            aarch64-darwin|aarch64-linux) LINUX_SYSTEM="aarch64-linux" ;;
            x86_64-darwin|x86_64-linux)  LINUX_SYSTEM="x86_64-linux"  ;;
            *) echo "Unsupported system: $HOST_SYSTEM" >&2; exit 1 ;;
          esac

          # Derive the image tag via nix eval — no build required
          IMAGE_TAG=$(nix eval --raw ".#packages.$LINUX_SYSTEM.docker-dev.imageTag")
          IMAGE="kalbasit/ncps-dev:$IMAGE_TAG"
          echo "Image: $IMAGE"

          # Check if image already exists on Docker Hub; push if not
          if skopeo inspect --insecure-policy "docker://$IMAGE" > /dev/null 2>&1; then
            echo "Image $IMAGE already exists on Docker Hub, skipping push."
          else
            echo "Building docker-dev for $LINUX_SYSTEM..."
            nix build ".#packages.$LINUX_SYSTEM.docker-dev"

            echo "Pushing $IMAGE..."
            skopeo --insecure-policy copy "docker-archive:./result" "docker://$IMAGE"
          fi

          # Update .container-use/environment.json
          ENV_FILE=".container-use/environment.json"
          mkdir -p .container-use
          if [ -f "$ENV_FILE" ]; then
            TMP_FILE=$(mktemp)
            jq --arg img "$IMAGE" '.base_image = $img' "$ENV_FILE" > "$TMP_FILE"
            mv "$TMP_FILE" "$ENV_FILE"
          else
            jq -n --arg img "$IMAGE" '{"workdir":"/workdir","base_image":$img}' > "$ENV_FILE"
          fi

          echo "Updated $ENV_FILE to use $IMAGE"
        '';
      };
    };
}
