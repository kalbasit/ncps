{
  description = "ncps - Nix binary cache proxy service";

  inputs = {

    flake-parts = {
      inputs.nixpkgs-lib.follows = "nixpkgs";
      url = "github:hercules-ci/flake-parts";
    };

    git-hooks-nix = {
      inputs.nixpkgs.follows = "nixpkgs";
      url = "github:cachix/git-hooks.nix";
    };

    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";

    process-compose-flake.url = "github:Platonic-Systems/process-compose-flake";

    treefmt-nix = {
      inputs.nixpkgs.follows = "nixpkgs";
      url = "github:numtide/treefmt-nix";
    };

    trilium = {
      inputs.nixpkgs.follows = "nixpkgs";
      # url = "github:TriliumNext/Trilium";
      url = "github:kalbasit/Trilium/ncps-branch";
    };
  };

  outputs =
    inputs@{ flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      imports = [
        ./nix/checks/flake-module.nix
        ./nix/dbmate-wrapper/flake-module.nix
        ./nix/devshells/flake-module.nix
        ./nix/formatter/flake-module.nix
        ./nix/gen-db-wrappers/flake-module.nix
        ./nix/k8s-tests/flake-module.nix
        ./nix/packages/flake-module.nix
        ./nix/pre-commit/flake-module.nix
        ./nix/process-compose/flake-module.nix
      ];
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
        "x86_64-darwin"
      ];
    };
}
