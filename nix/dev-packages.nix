# Common development packages shared between the devShell and the docker-dev image.
# Note: python3 is NOT included here because devShell and docker-dev need different
# package sets (devShell includes httpx; docker-dev does not).
# Usage: import ./dev-packages.nix pkgs
pkgs: with pkgs; [
  go
  golangci-lint
  sqlc
  sqlfluff
  watchexec
  # direnv: dev-scripts/run.py launches each ncps replica via `direnv exec` to
  # load the flake env. Bundling it makes the dev shell self-contained (CI runs
  # local-mode e2e via `nix develop` and must not rely on a host-installed
  # direnv).
  direnv
  xz
  delve
  awscli2
  garage
  mariadb
  postgresql
  redis
  skopeo
  pre-commit
  kubernetes-helm
  kubernetes-helmPlugins.helm-unittest
]
