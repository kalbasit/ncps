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
  xz
  delve
  mariadb
  minio
  minio-client
  postgresql
  redis
  skopeo
  pre-commit
  kubernetes-helm
  kubernetes-helmPlugins.helm-unittest
]
