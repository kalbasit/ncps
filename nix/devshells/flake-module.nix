{
  perSystem =
    { config, pkgs, ... }:
    {
      devShells.default = pkgs.mkShell {
        buildInputs = [
          pkgs.dbmate
          pkgs.go
          pkgs.golangci-lint
          pkgs.sqlc
        ];

        _GO_VERSION = "${pkgs.go.version}";
        _DBMATE_VERSION = "${pkgs.dbmate.version}";

        shellHook = ''
          ${config.pre-commit.installationScript}   

          ${pkgs.gnused}/bin/sed -e "s:^\(go \)[0-9.]*$:\1''${_GO_VERSION}:" -i go.mod
          ${pkgs.gnused}/bin/sed -e "s:^\(ARG GO_VERSION=\).*$:\1''${_GO_VERSION}:" -i Dockerfile
          ${pkgs.gnused}/bin/sed -e "s:^\(ARG DBMATE_VERSION=\).*$:\1''${_DBMATE_VERSION}:" -i Dockerfile
        '';
      };
    };
}
