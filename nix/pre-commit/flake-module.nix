{ inputs, ... }:

{
  imports = [
    inputs.git-hooks-nix.flakeModule
  ];

  perSystem =
    { pkgs, ... }:
    {
      pre-commit.settings.hooks = {
        golangci-lint.enable = true;
        gofmt.enable = true;
        no-commit-to-branch.enable = true;
        no-commit-to-branch.settings.branch = [ "main" ];
        nixfmt-rfc-style.enable = true;
        statix.enable = true;
      };
    };
}
