{
  imports = [
    ./ncps.nix
  ];

  perSystem =
    { config, ... }:
    {
      packages.default = config.packages.ncps;
    };
}
