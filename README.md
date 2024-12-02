# ncps: Nix Cache Proxy Server

ncps is a proxy server that speeds up Nix dependency retrieval on your local network. It acts as a local binary cache for Nix, fetching store paths from upstream caches (like cache.nixos.org) and storing them locally. This reduces download times and bandwidth usage, especially when multiple machines share the same dependencies.

## Problem it Solves

When multiple machines running NixOS or Nix pull packages, they often download the same dependencies from remote caches like `cache.nixos.org`. This leads to:

- **Redundant downloads:** Each machine downloads the same files.
- **Increased bandwidth usage:** Potentially significant network traffic, especially for large projects.
- **Slower build times:** Waiting for downloads slows down development and deployments.

ncps addresses these issues by acting as a central cache on your local network.

## How it Works

1. **Request:** A Nix client configured to use ncps requests a store path.
2. **Check Local Cache:** ncps checks if the path is already cached locally. If found, it serves the path directly.
3. **Fetch from Upstream:** If the path is not found locally, ncps fetches it from the configured upstream caches (e.g., cache.nixos.org).
4. **Cache and Sign:** ncps stores the downloaded path in its local cache **and signs it with its own private key**, ensuring that all served paths have valid signatures from both the upstream cache and ncps.
5. **Serve to Client:** ncps serves the downloaded path to the requesting Nix client.

## Features

- **Easy setup:** ncps is easy to configure and run.
- **Multiple upstream caches:** Support for multiple upstream caches for redundancy and flexibility.
- **Reduced bandwidth usage:** Minimizes redundant downloads, saving bandwidth and time.
- **Improved build times:** Faster access to dependencies speeds up builds.
- **Secure caching:** ncps signs cached paths with its own key, ensuring integrity and authenticity.

## Installation

ncps can be installed in several ways:

- **Build from source:**

  - Ensure you have Go installed and configured.
  - Clone the repository: `git clone https://github.com/kalbasit/ncps.git`
  - Navigate to the `cmd/ncps` directory: `cd ncps/cmd/ncps`
  - Build the binary: `go build`

- **Pre-built binaries:**

  - Download the latest release for your platform from the [release page](https://github.com/kalbasit/ncps/releases).
  - Extract the binary and place it in your desired location.

- **Docker:**
  - Pull the Docker image: `docker pull kalbasit/ncps`
  - Run the container with appropriate port mappings and volume mounts for the cache directory.

## Configuration

ncps can be configured using the following flags:

- `--cache-hostname`: The hostname of the cache server. **This is used to generate the private key used for signing store paths (.narinfo).** (Environment variable: `$CACHE_HOSTNAME`)
- `--cache-data-path`: The local directory for storing configuration and cached store paths. (Environment variable: `$CACHE_DATA_PATH`)
- `--server-addr`: The address and port the server listens on (default: ":8501"). (Environment variable: `$SERVER_ADDR`)
- `--upstream-cache`: The **hostname** of an upstream binary cache (e.g., `cache.nixos.org`). **Do not include the scheme (https://).** This flag can be used multiple times to specify multiple upstream caches, for example: `--upstream-cache cache.nixos.org --upstream-cache nix-community.cachix.org`. (Environment variable: `$UPSTREAM_CACHES`)
- `--upstream-public-key`: The public key of an upstream cache in the format `host:public-key`. This flag is used to verify the signatures of store paths downloaded from upstream caches. This flag can be used multiple times, once for each upstream cache. (Environment variable: `$UPSTREAM_PUBLIC_KEYS`)

## Nix Configuration

- On your NixOS or Nix clients, configure Nix to use ncps as a binary cache by adding it to your `nix.conf` or `configuration.nix`:

  ```nix
  nix.settings.substituters = [
    "https://ncps-server-ip:port"
    # ... other substituters
  ];

  nix.settings.trusted-public-keys = [
    "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="
    # ... other keys, **and the public key of your ncps server**
  ];
  ```

## Contributing

Contributions are welcome! Feel free to open issues or submit pull requests.

## License

MIT License. See [LICENSE](LICENSE) for details.
