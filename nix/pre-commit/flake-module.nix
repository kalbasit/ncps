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
      golangci-lint.enable = true;
      no-commit-to-branch.enable = true;
      no-commit-to-branch.settings.branch = [ "main" ];
      nixfmt-rfc-style.enable = true;
      statix.enable = true;
      trim-trailing-whitespace.enable = true;
      yamlfmt.enable = true;
    };
  };
}
