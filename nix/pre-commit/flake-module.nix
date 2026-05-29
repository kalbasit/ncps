{ inputs, ... }:

{
  imports = [
    inputs.git-hooks-nix.flakeModule
  ];

  perSystem =
    { pkgs, ... }:
    {
      pre-commit = {
        check.enable = false;
        settings = {
          # git-hooks.nix emits language="unsupported" for pre-commit >= 4.4.0,
          # but pre-commit 4.5.1 in nixpkgs-26.05 never added "unsupported" as
          # a valid language — cfgv rejects it. Override the version attribute
          # only (using // so no rebuild is triggered) to make git-hooks.nix
          # emit language="system" instead.
          package = pkgs.pre-commit // {
            version = "4.3.0";
          };
          hooks = {
            check-merge-conflicts.enable = true;
            chart-testing = {
              enable = true;

              # validate maintainer requires name to be my GitHub username.
              # https://github.com/helm/chart-testing/issues/192
              # TODO: Get upstream entry and append to it?
              entry = "${pkgs.chart-testing}/bin/ct lint --all --skip-helm-dependencies --validate-maintainers=false";
            };
            deadnix.enable = true;
            golangci-lint.enable = true;
            no-commit-to-branch.enable = true;
            no-commit-to-branch.settings.branch = [ "main" ];
            nixfmt-rfc-style.enable = true;
            statix.enable = true;
            trim-trailing-whitespace.enable = true;
            yamlfmt.enable = true;
          };
        };
      };
    };
}
