{ inputs, ... }:

{
  imports = [
    inputs.git-hooks-nix.flakeModule
  ];

  perSystem =
    { pkgs, ... }:
    {
      pre-commit.check.enable = false;
      pre-commit.settings.hooks = {
        check-merge-conflicts.enable = true;
        chart-testing = {
          enable = true;

          # validate maintainer requires name to be my GitHub username.
          # https://github.com/helm/chart-testing/issues/192
          # TODO: Get upstream entry and append to it?
          entry = "${pkgs.chart-testing}/bin/ct lint --all --skip-helm-dependencies --validate-maintainers=false";
        };
        deadnix.enable = true;
        golangci-lint = {
          enable = true;

          # XXX: Exclude sub-modules because it fails with this error message:
          # ERRO [linters_context] typechecking error: main module (github.com/kalbasit/ncps) does not contain package github.com/kalbasit/ncps/nix/dbmate-wrapper/src
          excludes = [
            "nix/dbmate-wrapper/src"
          ];
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
