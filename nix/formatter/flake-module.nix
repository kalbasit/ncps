{ inputs, ... }:
{
  imports = [ inputs.treefmt-nix.flakeModule ];

  perSystem = {
    treefmt = {
      # Used to find the project root
      projectRootFile = ".git/config";

      settings.global.excludes = [ ".envrc" ];

      programs = {
        nixfmt.enable = true;
        deadnix.enable = true;
        gofumpt.enable = true;
        yamlfmt.enable = true;
        mdformat.enable = true;
        sqlfluff.enable = true;
        sqlfluff.dialect = "sqlite";
        statix.enable = true;
      };
    };
  };
}
