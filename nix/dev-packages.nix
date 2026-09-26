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
  awscli2
  garage
  mariadb
  postgresql
  redis
  skopeo
  pre-commit
  # helm wrapped with the helm-unittest plugin. Listing kubernetes-helm and the
  # plugin as two independent packages does NOT register the plugin: helm
  # discovers plugins through HELM_PLUGINS, so a bare plugin on PATH leaves
  # `helm unittest` reporting `unknown command "unittest"`. wrapHelm sets
  # HELM_PLUGINS for us, which is what makes `helm unittest charts/ncps`
  # reproduce the helm-unittest-check result locally.
  (wrapHelm kubernetes-helm { plugins = [ kubernetes-helmPlugins.helm-unittest ]; })
]
