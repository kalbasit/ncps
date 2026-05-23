{
  imports = [
    ./docker.nix
    ./docker-dev.nix
    ./ncps
    ./ncps-checktools
  ];

  perSystem =
    { config, ... }:
    {
      packages.default = config.packages.ncps;
    };
}
