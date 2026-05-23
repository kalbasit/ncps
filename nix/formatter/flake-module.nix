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
        # Ent generated code: produced by `go generate ./ent/...`; never
        # hand-edited and never re-formatted — the Ent generator emits its
        # own canonical layout, and any external formatting causes a diff
        # loop against the codegen drift check (see
        # nix/checks/flake-module.nix → ent-codegen-drift-check). The
        # schemas under ent/schema/ remain formattable (they are inputs to
        # the generator); only the generated tree is excluded. The
        # hand-written ent/generate.go (which carries the //go:generate
        # directive) is explicitly re-included so it stays formattable.
        "ent/*.go"
        "!ent/generate.go"
        "ent/chunk/**/*.go"
        "ent/configentry/**/*.go"
        "ent/enttest/**/*.go"
        "ent/hook/**/*.go"
        "ent/migrate/**/*.go"
        "ent/narfile/**/*.go"
        "ent/narfilechunk/**/*.go"
        "ent/narinfo/**/*.go"
        "ent/narinfonarfile/**/*.go"
        "ent/narinforeference/**/*.go"
        "ent/narinfosignature/**/*.go"
        "ent/pinnedclosure/**/*.go"
        "ent/predicate/**/*.go"
        "ent/runtime/**/*.go"
        # Atlas-generated migration files: their integrity is sealed by
        # atlas.sum under each migrations/<dialect>/. Reformatting the SQL
        # would invalidate the sum and break the schema-equivalence check.
        "migrations/**/*.sql"
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
