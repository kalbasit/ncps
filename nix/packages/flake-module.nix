{
  imports = [
    ./docker.nix
    ./ncps
  ];

  perSystem =
    { config, ... }:
    {
      packages.default = config.packages.ncps;
    };
}
