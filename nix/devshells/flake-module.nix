{
  perSystem =
    { config, pkgs, ... }:
    {
      devShells.default = pkgs.mkShell {
        buildInputs = [
          # Use real dbmate for the wrapper to call
          (pkgs.writeShellScriptBin "dbmate.real" ''
            exec ${pkgs.dbmate}/bin/dbmate "$@"
          '')
          # dbmate-wrapper provides the dbmate command
          (pkgs.writeShellScriptBin "dbmate" ''
            exec ${config.packages.dbmate-wrapper}/bin/dbmate-wrapper "$@"
          '')
          pkgs.delve
          pkgs.go
          pkgs.golangci-lint
          pkgs.minio
          pkgs.minio-client
          pkgs.sqlc
          pkgs.sqlfluff
          pkgs.watchexec
        ];

        _GO_VERSION = "${pkgs.go.version}";
        _DBMATE_VERSION = "${pkgs.dbmate.version}";

        # Disable hardening for fortify otherwize it's not possible to use Delve.
        hardeningDisable = [ "fortify" ];

        shellHook = ''
          ${config.pre-commit.installationScript}

          ${pkgs.gnused}/bin/sed -e "s:^\(go \)[0-9.]*$:\1''${_GO_VERSION}:" -i go.mod
          ${pkgs.gnused}/bin/sed -e "s:^\(go \)[0-9.]*$:\1''${_GO_VERSION}:" -i nix/dbmate-wrapper/src/go.mod
        '';
      };
    };
}
