{ self, ... }:
{
  perSystem =
    {
      lib,
      pkgs,
      ...
    }:
    {
      packages.ncps =
        let
          shortRev = self.shortRev or self.dirtyShortRev;
          rev = self.rev or self.dirtyRev;
          tag = builtins.getEnv "RELEASE_VERSION";

          version = if tag != "" then tag else rev;
        in
        pkgs.buildGoModule {
          name = "ncps-${shortRev}";

          src = lib.fileset.toSource {
            fileset = lib.fileset.unions [
              ../../cmd
              ../../db/migrations
              ../../go.mod
              ../../go.sum
              ../../main.go
              ../../pkg
              ../../testdata
              ../../testhelper
            ];
            root = ../..;
          };

          ldflags = [
            "-X github.com/kalbasit/ncps/cmd.Version=${version}"
          ];

          subPackages = [ "." ];

          vendorHash = "sha256-3ovcjYcb4H79JSO/3WioOEiK8KqD/xdbzqER3HlF1ZY=";

          doCheck = true;

          nativeBuildInputs = [
            pkgs.dbmate # used for testing
          ];

          postInstall = ''
            mkdir -p $out/share/ncps
            cp -r db $out/share/ncps/db
          '';

          meta = {
            description = "Nix binary cache proxy service";
            homepage = "https://github.com/kalbasit/ncps";
            license = lib.licenses.mit;
            maintainers = [ lib.maintainers.kalbasit ];
          };
        };

    };
}
