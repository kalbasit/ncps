{
  imports = [
    ./docker.nix
    ./docker-dev.nix
    ./ncps
  ];

  perSystem =
    { config, ... }:
    {
      packages.default = config.packages.ncps;
    };
}
