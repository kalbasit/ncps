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
- **Cache size management:** Configure a maximum cache size and a cron schedule to automatically remove least recently used (LRU) store paths, preventing the cache from growing indefinitely.

## Installation

ncps can be installed in several ways:

- **Pre-built binaries:**

  - Download the latest release for your platform from the [release page](https://github.com/kalbasit/ncps/releases).
  - Extract the binary and place it in your desired location.
  - Make it executable.

- **Install with Go from GitHub:**

  - Ensure you have Go installed and configured.
  - Run the following command:

    ```bash
    go install github.com/kalbasit/ncps@latest
    ```

- **Build from source:**

  - Ensure you have Go installed and configured.
  - Clone the repository: `git clone https://github.com/kalbasit/ncps.git`
  - Navigate to the root directory of ncps: `cd ncps`
  - Build the binary: `go build .`

- **Docker:**

  - Pull the Docker image: `docker pull kalbasit/ncps`
  - Run the container with appropriate port mappings and volume mounts for the cache directory.

- **Kubernetes**

  The following resources are provided as an example for running ncps on Kubernetes. Personally, I run it on my k3s cluster.

<details>
<summary>PersistentVolumeClaim</summary>

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: nix-cache
  labels:
    app: nix-cache
    tier: proxy
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 20Gi
```

</details>

<details>
<summary>StatefulSet</summary>

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: nix-cache
  labels:
    app: nix-cache
    tier: proxy
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nix-cache
      tier: proxy
  template:
    metadata:
      labels:
        app: nix-cache
        tier: proxy
    spec:
      initContainers:
        - image: alpine:latest
          name: create-directories
          args:
            - /bin/sh
            - -c
            - "mkdir -m 0755 -p /storage/var && mkdir -m 0700 -p /storage/var/ncps && mkdir -m 0700 -p /storage/var/ncps/db"
          volumeMounts:
            - name: nix-cache-persistent-storage
              mountPath: /storage
        - image: kalbasit/ncps:latest # NOTE: It's recommended to use a tag here!
          name: migrate-database
          args:
            - /bin/dbmate
            - --url=sqlite:/storage/var/ncps/db/db.sqlite
            - migrate
            - up
          volumeMounts:
            - name: nix-cache-persistent-storage
              mountPath: /storage
      containers:
        - image: kalbasit/ncps:latest # NOTE: It's recommended to use a tag here!
          name: nix-cache
          args:
            - /bin/ncps
            - serve
            - --cache-hostname=nix-cache.yournetwork.local # TODO: Replace with your own hostname
            - --cache-data-path=/storage
            - --upstream-cache=https://cache.nixos.org
            - --upstream-cache=https://nix-community.cachix.org
            - --upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
            - --upstream-public-key=nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs=
          ports:
            - containerPort: 8501
              name: http-web
          volumeMounts:
            - name: nix-cache-persistent-storage
              mountPath: /storage
      volumes:
        - name: nix-cache-persistent-storage
          persistentVolumeClaim:
            claimName: nix-cache
```

</details>

<details>
<summary>Service</summary>

```yaml
apiVersion: v1
kind: Service
metadata:
  name: nix-cache
  labels:
    app: nix-cache
    tier: proxy
spec:
  type: ClusterIP
  ports:
    - name: http-web
      port: 8501
  selector:
    app: nix-cache
    tier: proxy
```

</details>

## Configuration

ncps can be configured using the following flags:

- `--allow-delete`: Whether to allow the DELETE verb to delete `narinfo` and `nar` files from the cache (default: false). (Environment variable: `$ALLOW_DELETE_VERB`)
- `--allow-put`: Whether to allow the PUT verb to push `narinfo` and `nar` files directly to the cache (default: false). (Environment variable: `$ALLOW_PUT_VERB`)
- `--cache-hostname`: The hostname of the cache server. **This is used to generate the private key used for signing store paths (.narinfo).** (Environment variable: `$CACHE_HOSTNAME`)
- `--cache-data-path`: The local directory for storing configuration and cached store paths. (Environment variable: `$CACHE_DATA_PATH`)
- `--cache-max-size`: The maximum size of the store. It can be given with units such as 5K, 10G etc. Supported units: B, K, M, G, T (Environment variable: `$CACHE_MAX_SIZE`)
- `--cache-lru-schedule`: The cron spec for cleaning the store to keep it under `--cache-max-size`. Refer to https://pkg.go.dev/github.com/robfig/cron/v3#hdr-Usage for documentation (Environment variable: `$CACHE_LRU_SCHEDULE`)
- `--cache-lru-schedule-timezone`: The name of the timezone to use for the cron schedule (default: "Local"). (Environment variable: `$CACHE_LRU_SCHEDULE_TZ`)
- `--server-addr`: The address and port the server listens on (default: ":8501"). (Environment variable: `$SERVER_ADDR`)
- `--upstream-cache`: The URL of an upstream binary cache (e.g., `https://cache.nixos.org`). This flag can be used multiple times to specify multiple upstream caches. (Environment variable: `$UPSTREAM_CACHES`)
- `--upstream-public-key`: The public key of an upstream cache in the format `host:public-key`. This flag is used to verify the signatures of store paths downloaded from upstream caches. This flag can be used multiple times, once for each upstream cache. (Environment variable: `$UPSTREAM_PUBLIC_KEYS`)

## Nix Configuration

On your NixOS or Nix clients, you need to configure Nix to use ncps as a binary cache.

**On NixOS**, you can configure these settings in your `configuration.nix` file using the `nix.settings` option, like this:

```nix
nix.settings.substituters = [
  "https://ncps-server-ip:port"
  // ... other substituters
];

nix.settings.trusted-public-keys = [
  "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="
  // ... other keys, and the public key of your ncps server
];
```

**On non-NixOS**, this involves two steps:

1. **Add ncps to `substituters`:**

   - In your `nix.conf` file (usually located at `/etc/nix/nix.conf` or `~/.config/nix/nix.conf`), add the URL of your ncps server to the `substituters` list. This tells Nix to try fetching store paths from ncps.

   ```
   substituters = https://ncps-server-ip:port ... other substituters
   ```

2. **Add ncps public key to `trusted-public-keys`:**

   - Add the public key of your ncps server to the `trusted-public-keys` list in your `nix.conf`. This allows Nix to verify the signatures generated by ncps for cached store paths.

   ```
   trusted-public-keys = ncps-server-hostname:ncps-public-key ... other keys
   ```

## Contributing

Contributions are welcome! Feel free to open issues or submit pull requests.

## License

This project is licensed under the MIT License - see the [LICENSE](/LICENSE) file for details.
