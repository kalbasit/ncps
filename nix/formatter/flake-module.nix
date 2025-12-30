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
        "db/schema.sql"
        "db/query.*.sql" # sqlc query files use special syntax
        "db/migrations/postgres/**" # postgres migrations use postgres dialect
        "LICENSE"
        "renovate.json"
      ];

      programs = {
        deadnix.enable = true;
        gofumpt.enable = true;
        mdformat.enable = true;
        nixfmt.enable = true;
        sqlfluff = {
          enable = true;
          # Default dialect for SQLite migration files
          # PostgreSQL files are excluded above
          dialect = "sqlite";
        };
        statix.enable = true;
        yamlfmt.enable = true;
      };
    };
  };
}
