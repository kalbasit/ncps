{
  description = "ncps - Nix binary cache proxy service";

  inputs = {
    flake-parts.url = "github:hercules-ci/flake-parts";
    nixpkgs.url = "github:NixOS/nixpkgs/release-24.11";
  };

  outputs =
    inputs@{ flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
        "x86_64-darwin"
      ];
      perSystem =
        {
          config,
          self',
          inputs',
          pkgs,
          system,
          lib,
          ...
        }:
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
              ${pkgs.gnused}/bin/sed -e "s:^\(go \)[0-9.]*$:\1''${_GO_VERSION}:" -i go.mod
              ${pkgs.gnused}/bin/sed -e "s:^\(ARG GO_VERSION=\).*$:\1''${_GO_VERSION}:" -i Dockerfile
              ${pkgs.gnused}/bin/sed -e "s:^\(ARG DBMATE_VERSION=\).*$:\1''${_DBMATE_VERSION}:" -i Dockerfile
              ${pkgs.gnused}/bin/sed -e "s/\(go-version: \).*$/\1\"''${_GO_VERSION}\"/" -i .github/workflows/golangci-lint.yml
            '';
          };
        };
    };
}
