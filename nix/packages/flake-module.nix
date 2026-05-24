{
  imports = [
    ./docker.nix
    ./docker-dev.nix
    ./ncps
    ./ncps-checktools
    ./ncps-coverage
  ];

  perSystem =
    { config, ... }:
    {
      packages.default = config.packages.ncps;
    };
}
