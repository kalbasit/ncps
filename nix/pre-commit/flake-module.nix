{ inputs, ... }:

{
  imports = [
    inputs.git-hooks-nix.flakeModule
  ];

  perSystem = {
    pre-commit.check.enable = false;
    pre-commit.settings.hooks = {
      check-merge-conflicts.enable = true;
      deadnix.enable = true;
      gofmt.enable = true;
      golangci-lint = {
        enable = true;

        # XXX: Exclude dbmate-wrapper because it fails with this error message:
        # ERRO [linters_context] typechecking error: main module (github.com/kalbasit/ncps) does not contain package github.com/kalbasit/ncps/nix/dbmate-wrapper/src
        excludes = [ "nix/dbmate-wrapper/src" ];
      };
      no-commit-to-branch.enable = true;
      no-commit-to-branch.settings.branch = [ "main" ];
      nixfmt-rfc-style.enable = true;
      statix.enable = true;
      trim-trailing-whitespace.enable = true;
      yamlfmt.enable = true;
    };
  };
}
