{ inputs, ... }:

{
  imports = [
    inputs.git-hooks-nix.flakeModule
  ];

  perSystem = {
    pre-commit.check.enable = false;
    pre-commit.settings.hooks = {
      golangci-lint.enable = true;
      gofmt.enable = true;
      no-commit-to-branch.enable = true;
      no-commit-to-branch.settings.branch = [ "main" ];
      nixfmt-rfc-style.enable = true;
      statix.enable = true;
      trim-trailing-whitespace.enable = true;
    };
  };
}
