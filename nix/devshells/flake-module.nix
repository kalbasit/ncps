{ inputs, ... }:

{
  imports = [
    inputs.git-hooks-nix.flakeModule
  ];

  perSystem =
    { config, pkgs, ... }:
    {
      pre-commit.settings.hooks = {
        golangci-lint.enable = true;
        gofmt.enable = true;
        no-commit-to-branch.enable = true;
        no-commit-to-branch.settings.branch = [ "main" ];
        nixfmt-rfc-style.enable = true;
        statix.enable = true;
      };

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
