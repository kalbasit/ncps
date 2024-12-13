{
  perSystem =
    {
      lib,
      pkgs,
      ...
    }:
    {
      packages.ncps = pkgs.buildGoModule {
        name = "ncps";

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

        subPackages = [ "." ];

        vendorHash = "sha256-P8B5G3AqVZ5VQMNK+ecCVHBR7XEKL7AATuWxJGIAw+w=";

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
