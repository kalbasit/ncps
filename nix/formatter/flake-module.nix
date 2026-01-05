{ inputs, ... }:
{
  imports = [ inputs.treefmt-nix.flakeModule ];

  perSystem = {
    treefmt = {
      settings.global.excludes = [
        ".env"
        ".envrc"
        "db/schema.sql"
        "db/migrations/sqlite/20251230224159_add-cascade-to-nars-fk.sql" # sqlfluff has parsing issues with transaction blocks
        "LICENSE"
        "renovate.json"
      ];

      # Exclude sqlc query files from sqlfluff - they use sqlc-specific syntax
      settings.formatter = {
        sqlfluff.excludes = [
          "db/query.sql"
          "db/query.*.sql"
        ];
        sqlfluff-lint.excludes = [
          "db/query.sql"
          "db/query.*.sql"
        ];
      };

      programs = {
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
