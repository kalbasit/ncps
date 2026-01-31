# docs
## ncps: Nix Cache Proxy Server

Welcome to the official documentation for **ncps** (Nix Cache Proxy Server).

**ncps** is a high-performance proxy server designed to accelerate Nix dependency retrieval across your local network. By acting as a local binary cache, it fetches store paths from upstream caches (such as cache.nixos.org) and stores them locally. This significantly reduces download times and bandwidth usage, making it ideal for environments where multiple machines share the same dependencies.

## Documentation

Please choose one of the following guides to get started:

*   [**User Guide**](docs/User%20Guide.md) Everything you need to know about installing, configuring, deploying, and operating ncps. Start here if you are setting up ncps for yourself or your organization.
*   [**Developer Guide**](docs/Developer%20Guide.md) Detailed information on the system architecture, component design, and contribution guidelines. Head here if you want to understand the internals or contribute to the project.

## About the Author

**ncps** is created and maintained by **Wael Nasreddine**.

If you find this project useful, check out my blog or consider supporting my work:

*   **Blog**: [https://wael.nasreddine.com/](https://wael.nasreddine.com/)
*   **Sponsor**: [https://github.com/sponsors/kalbasit](https://github.com/sponsors/kalbasit)