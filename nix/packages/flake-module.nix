{
  imports = [
    ./docker.nix
    ./ncps.nix
  ];

  perSystem =
    { config, ... }:
    {
      packages.default = config.packages.ncps;
    };
}
