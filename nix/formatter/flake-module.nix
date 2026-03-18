{ inputs, ... }:
{
  imports = [ inputs.treefmt-nix.flakeModule ];

  perSystem = {
    treefmt = {
      settings.global.excludes = [
        ".agent/skills/**/*.md"
        ".agent/workflows/*.md"
        ".claude/commands/**/*.md"
        ".github/prompts/*.md"
        ".github/skills/**/*.md"
        ".env"
        ".envrc"
        "LICENSE"
        "openspec/**/*.md"
        "renovate.json"
      ];

      programs = {
        actionlint.enable = true;
        deadnix.enable = true;
        gofumpt.enable = true;
        mdformat.enable = true;
        nixfmt.enable = true;
        sqlfluff.enable = true;
        sqlfluff-lint.enable = true;
        statix.enable = true;
        yamlfmt.enable = true;
      };
    };
  };
}
