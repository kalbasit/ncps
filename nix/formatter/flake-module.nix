{ inputs, ... }:
{
  imports = [ inputs.treefmt-nix.flakeModule ];

  perSystem = {
    treefmt = {
      # Used to find the project root
      projectRootFile = ".git/config";

      settings.global.excludes = [
        ".env"
        ".envrc"
        "LICENSE"
        "renovate.json"
      ];

      programs = {
        deadnix.enable = true;
        gofumpt.enable = true;
        mdformat.enable = true;
        nixfmt.enable = true;
        sqlfluff.enable = true;
        sqlfluff.dialect = "sqlite";
        statix.enable = true;
        yamlfmt.enable = true;
      };
    };
  };
}
