{
  perSystem =
    { lib, pkgs, ... }:
    {
      packages.dbmate-wrapper = pkgs.buildGoModule {
        name = "dbmate-wrapper";
        src = ./src;

        vendorHash = null;

        buildInputs = lib.singleton pkgs.dbmate;
        nativeBuildInputs = lib.singleton pkgs.makeBinaryWrapper;

        postInstall = ''
          # the dbmate-wrapper needs access to the original dbmate executable, wrap it so it can find it correctly.
          wrapProgram $out/bin/dbmate-wrapper --set DBMATE_BIN ${lib.getExe pkgs.dbmate}
        '';

        meta = {
          description = "Wrapper for dbmate that auto-detects migrations directory from database URL";
          mainProgram = "dbmate-wrapper";
        };
      };
    };
}
