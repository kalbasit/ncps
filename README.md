# 🚀 ncps: Nix Cache Proxy Server

> A high-performance proxy server that accelerates Nix dependency retrieval across your local network by caching and serving packages locally.

[![Go Report Card](https://goreportcard.com/badge/github.com/kalbasit/ncps)](https://goreportcard.com/report/github.com/kalbasit/ncps)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## 📋 Table of Contents

- [Overview](#-overview)
- [Problem & Solution](#-problem--solution)
- [Key Features](#-key-features)
- [How It Works](#-how-it-works)
- [Quick Start](#-quick-start)
- [Installation](#-installation)
- [Configuration](#-configuration)
- [High Availability Deployments](#-high-availability-deployments)
- [Client Setup](#-client-setup)
- [Troubleshooting](#-troubleshooting)
- [Contributing](#-contributing)

## 🎯 Overview

ncps acts as a local binary cache for Nix, fetching store paths from upstream caches (like cache.nixos.org) and storing them locally. This reduces download times and bandwidth usage, especially beneficial when multiple machines share the same dependencies.

## 🔍 Problem & Solution

### The Problem

When multiple machines running NixOS or Nix pull packages, they often download the same dependencies from remote caches, leading to:

- ❌ **Redundant downloads** - Each machine downloads identical files
- ❌ **High bandwidth usage** - Significant network traffic for large projects
- ❌ **Slower build times** - Network latency impacts development velocity

### The Solution

ncps solves these issues by acting as a **centralized cache** on your local network, dramatically reducing redundant downloads and improving build performance.

## ✨ Key Features

| Feature | Description |
| ----------------------- | ------------------------------------------------------------------------------------------------- |
| 🚀 **Easy Setup** | Simple configuration and deployment |
| 🔄 **Multi-Upstream** | Support for multiple upstream caches with failover |
| 💾 **Smart Caching** | LRU cache management with configurable size limits |
| ☁️ **Flexible Storage** | **NEW in v0.5.0:** Choose between local filesystem or S3-compatible storage (AWS S3, MinIO, etc.) |
| 🔐 **Secure Signing** | Signs cached paths with private keys for integrity |
| 📊 **Monitoring** | OpenTelemetry support for centralized logging |
| 🗜️ **Compression** | Harmonia's transparent zstd compression support |
| 🗄️ **Database Support** | SQLite (embedded), PostgreSQL, or MySQL/MariaDB for metadata storage |
| 🏗️ **High Availability** | **NEW:** Run multiple instances with Redis-based distributed locking for zero-downtime deployments |

## ⚙️ How It Works

### Request Flow

```mermaid
sequenceDiagram
    participant Client as Nix Client
    participant NCPS as ncps Server
    participant Cache as Cache Storage
    participant Upstream as Upstream Cache

    Client->>NCPS: Request store path
    NCPS->>Cache: Check cache

    alt Path exists in cache
        Cache-->>NCPS: Return cached path
        NCPS-->>Client: Serve cached path
    else Path not cached
        NCPS->>Upstream: Fetch from upstream
        Upstream-->>NCPS: Return store path
        NCPS->>Cache: Cache and sign path
        NCPS-->>Client: Serve downloaded path
    end
```

1. **Request** - Nix client requests a store path from ncps
1. **Cache Check** - ncps checks if the path exists in cache storage
1. **Upstream Fetch** - If not cached, fetches from configured upstream caches
1. **Cache & Sign** - Stores and signs the path with ncps private key
1. **Serve** - Delivers the path to the requesting client

### Storage Architecture

**NEW in v0.5.0:** ncps now supports flexible storage backends!

```mermaid
graph TB
    NCPS[ncps Server]

    subgraph Database["Database Backends (Choose One)"]
        SQLite[(SQLite<br/>--cache-database-url sqlite:...)]
        Postgres[(PostgreSQL<br/>--cache-database-url postgresql:...)]
        MySQL[(MySQL/MariaDB<br/>--cache-database-url mysql:...)]
    end

    subgraph Storage["Storage Backends (Choose One)"]
        Local[Local Filesystem<br/>--cache-storage-local]
        S3[S3-Compatible Storage<br/>--cache-storage-s3-*]
    end

    subgraph S3Detail["S3-Compatible Options"]
        AWS[AWS S3]
        MinIO[MinIO]
        Other[Other S3-Compatible]
    end

    NCPS -.->|Option 1| SQLite
    NCPS -.->|Option 2| Postgres
    NCPS -.->|Option 3| MySQL
    NCPS -.->|Option A| Local
    NCPS -.->|Option B| S3
    S3 --> S3Detail

    style Database fill:#ffe1f5
    style Storage fill:#e1f5ff
    style S3Detail fill:#fff4e1
    style NCPS fill:#d4edda
```

**Database Options:**

- **SQLite** (default) - Embedded database for single-server deployments, no external dependencies
- **PostgreSQL** - Scalable relational database for production deployments
- **MySQL/MariaDB** - Popular open-source relational database for production deployments

**Storage Options:**

- **Local Filesystem** - Traditional file-based storage for single-server deployments
- **S3-Compatible Storage** - Scalable cloud storage supporting AWS S3, MinIO, and other S3-compatible services

## 🚀 Quick Start

Get ncps running quickly with Docker:

<details open>
<summary><strong>Using Local Storage</strong></summary>

```bash
# Pull the images
docker pull alpine
docker pull kalbasit/ncps

# Create the storage volume
docker volume create ncps-storage
docker run --rm -v ncps-storage:/storage alpine /bin/sh -c \
  "mkdir -m 0755 -p /storage/var && mkdir -m 0700 -p /storage/var/ncps && mkdir -m 0700 -p /storage/var/ncps/db"

# Initialize database
docker run --rm -v ncps-storage:/storage kalbasit/ncps /bin/dbmate --url=sqlite:/storage/var/ncps/db/db.sqlite migrate up

# Start the server
docker run -d --name ncps -p 8501:8501 -v ncps-storage:/storage kalbasit/ncps \
  /bin/ncps serve \
  --cache-hostname=your-ncps-hostname \
  --cache-storage-local=/storage \
  --cache-database-url=sqlite:/storage/var/ncps/db/db.sqlite \
  --cache-upstream-url=https://cache.nixos.org \
  --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

</details>

<details>
<summary><strong>Using S3 Storage (NEW in v0.5.0)</strong></summary>

```bash
# Pull the image
docker pull kalbasit/ncps

# Create volume for database only (cache data goes to S3)
docker volume create ncps-db
docker run --rm -v ncps-db:/db alpine mkdir -m 0700 -p /db

# Initialize database
docker run --rm -v ncps-db:/db kalbasit/ncps /bin/dbmate --url=sqlite:/db/db.sqlite migrate up

# Start the server with S3 storage
docker run -d --name ncps -p 8501:8501 -v ncps-db:/db kalbasit/ncps \
  /bin/ncps serve \
  --cache-hostname=your-ncps-hostname \
  --cache-storage-s3-bucket=my-ncps-cache \
  --cache-storage-s3-endpoint=s3.amazonaws.com \
  --cache-storage-s3-region=us-east-1 \
  --cache-storage-s3-access-key-id=YOUR_ACCESS_KEY \
  --cache-storage-s3-secret-access-key=YOUR_SECRET_KEY \
  --cache-database-url=sqlite:/db/db.sqlite \
  --cache-upstream-url=https://cache.nixos.org \
  --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

**Benefits of S3 storage:**

- Scales independently of server resources
- Shared storage across multiple ncps instances
- Built-in durability and redundancy
- Works with AWS S3, MinIO, and other S3-compatible services

</details>

Your cache will be available at `http://localhost:8501` and the public key at `http://localhost:8501/pubkey`.

## 📦 Installation

<details>
<summary><strong>🐳 Docker</strong></summary>

### Docker Setup

**Step 1:** Pull the image

```bash
docker pull kalbasit/ncps
```

**Step 2:** Initialize storage and database

```bash
docker volume create ncps-storage

docker run --rm -v ncps-storage:/storage alpine /bin/sh -c \
  "mkdir -m 0755 -p /storage/var && mkdir -m 0700 -p /storage/var/ncps && mkdir -m 0700 -p /storage/var/ncps/db"

docker run --rm -v ncps-storage:/storage kalbasit/ncps /bin/dbmate --url=sqlite:/storage/var/ncps/db/db.sqlite migrate up
```

**Step 3:** Start the server

```bash
docker run -d \
  --name ncps \
  -p 8501:8501 \
  -v ncps-storage:/storage \
  kalbasit/ncps \
  /bin/ncps serve \
  --cache-hostname=your-ncps-hostname \
  --cache-storage-local=/storage \
  --cache-database-url=sqlite:/storage/var/ncps/db/db.sqlite \
  --cache-upstream-url=https://cache.nixos.org \
  --cache-upstream-url=https://nix-community.cachix.org \
  --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY= \
  --cache-upstream-public-key=nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs=
```

</details>

<details>
<summary><strong>🐳 Docker Compose</strong></summary>

Create a `docker-compose.yml` file:

```yaml
services:
  create-directories:
    image: alpine:latest
    volumes:
      - ncps-storage:/storage
    command: >
      /bin/sh -c "
        mkdir -m 0755 -p /storage/var &&
        mkdir -m 0700 -p /storage/var/ncps &&
        mkdir -m 0700 -p /storage/var/ncps/db
      "
    restart: "no"

  migrate-database:
    image: kalbasit/ncps:latest
    depends_on:
      create-directories:
        condition: service_completed_successfully
    volumes:
      - ncps-storage:/storage
    command: >
      /bin/dbmate --url=sqlite:/storage/var/ncps/db/db.sqlite migrate up
    restart: "no"

  ncps:
    image: kalbasit/ncps:latest
    depends_on:
      migrate-database:
        condition: service_completed_successfully
    ports:
      - "8501:8501"
    volumes:
      - ncps-storage:/storage
    command: >
      /bin/ncps serve
      --cache-hostname=your-ncps-hostname
      --cache-storage-local=/storage
      --cache-database-url=sqlite:/storage/var/ncps/db/db.sqlite
      --cache-upstream-url=https://cache.nixos.org
      --cache-upstream-url=https://nix-community.cachix.org
      --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
      --cache-upstream-public-key=nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs=
    restart: unless-stopped

volumes:
  ncps-storage:
```

Then run:

```bash
docker compose up -d
```

</details>

<details>
<summary><strong>☸️ Kubernetes</strong></summary>

<details>
<summary>PersistentVolumeClaim</summary>

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ncps
  labels:
    app: ncps
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
  name: ncps
  labels:
    app: ncps
    tier: proxy
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ncps
      tier: proxy
  template:
    metadata:
      labels:
        app: ncps
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
            - name: ncps-persistent-storage
              mountPath: /storage
        - image: kalbasit/ncps:latest # NOTE: It's recommended to use a tag here!
          name: migrate-database
          args:
            - /bin/dbmate
            - --url=sqlite:/storage/var/ncps/db/db.sqlite
            - migrate
            - up
          volumeMounts:
            - name: ncps-persistent-storage
              mountPath: /storage
      containers:
        - image: kalbasit/ncps:latest # NOTE: It's recommended to use a tag here!
          name: ncps
          args:
            - /bin/ncps
            - serve
            - --cache-hostname=ncps.yournetwork.local # TODO: Replace with your own hostname
            - --cache-storage-local=/storage
            - --cache-temp-path=/nar-temp-dir
            - --cache-database-url=sqlite:/storage/var/ncps/db/db.sqlite
            - --cache-upstream-url=https://cache.nixos.org
            - --cache-upstream-url=https://nix-community.cachix.org
            - --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
            - --cache-upstream-public-key=nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs=
          ports:
            - containerPort: 8501
              name: http-web
          volumeMounts:
            - name: ncps-persistent-storage
              mountPath: /storage
            - name: nar-temp-dir
              mountPath: /nar-temp-dir
      volumes:
        - name: ncps-persistent-storage
          persistentVolumeClaim:
            claimName: ncps
        - name: nar-temp-dir
          emptyDir:
            sizeLimit: 5Gi
```

</details>

<details>
<summary>Service</summary>

```yaml
apiVersion: v1
kind: Service
metadata:
  name: ncps
  labels:
    app: ncps
    tier: proxy
spec:
  type: ClusterIP
  ports:
    - name: http-web
      port: 8501
  selector:
    app: ncps
    tier: proxy
```

</details>
</details>

<details>
<summary><strong>🐧 NixOS</strong></summary>

### NixOS Service Module

ncps is available as a built-in NixOS service module (available in NixOS 25.05+). No additional installation needed!

**Basic Configuration:**

```nix
{
  services.ncps = {
    enable = true;
    cache.hostName = "your-ncps-hostname";
    upstream = {
      caches = [
        "https://cache.nixos.org"
        "https://nix-community.cachix.org"
      ];
      publicKeys = [
        "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="
        "nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs="
      ];
    };
  };
}
```

**Advanced Configuration:**

```nix
{
  services.ncps = {
    enable = true;
    cache = {
      hostName = "your-ncps-hostname";
      dataPath = "/path/to/ncps/data";
      tempPath = "/path/to/ncps/tmp"; # Introduced in NixOS 25.09
      databaseURL = "sqlite:/path/to/ncps/db/db.sqlite";
      maxSize = "50G";
      lru.schedule = "0 2 * * *"; # Clean up daily at 2 AM
      allowPutVerb = true;
      allowDeleteVerb = true;
    };
    server.addr = "0.0.0.0:8501";
    upstream = {
      caches = [
        "https://cache.nixos.org"
        "https://nix-community.cachix.org"
      ];
      publicKeys = [
        "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="
        "nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs="
      ];
    };
  };
}
```

**S3 Storage Support:**

S3 storage configuration in the NixOS module will be available in a future release. For now, use the Docker or standalone installation methods for S3 storage.

**Complete Options Reference:** [NixOS Options Search](https://search.nixos.org/options?query=services.ncps)

**✅ The NixOS module automatically handles:**

- Database initialization and migrations
- Systemd service configuration
- User and group creation
- Directory permissions
- Service dependencies

**📝 Note:** After enabling the service, configure your clients to use the cache (see [Client Setup](#-client-setup) section).

</details>

<details>
<summary><strong>🔧 Go Install & Source</strong></summary>

### Install with Go

```bash
go install github.com/kalbasit/ncps@latest
```

### Build from Source

```bash
git clone https://github.com/kalbasit/ncps.git
cd ncps
go build .
```

**Note:** You'll need to handle database setup and service management manually with these methods.

</details>

## ⚙️ Configuration

All the flags can be set using the configuration file. See [config.example.yaml](config.example.yaml) for a complete reference including S3 storage configuration examples.

### Global Options

| Option | Description | Environment Variable | Default |
| ---------------------- | ------------------------------------------------- | -------------------- | ----------------------------------- |
| `--config` | Path to the configuration file (json, toml, yaml) | `NCPS_CONFIG_FILE` | `$XDG_CONFIG_HOME/ncps/config.yaml` |
| `--otel-enabled` | Enable OpenTelemetry logs, metrics, and tracing | `OTEL_ENABLED` | `false` |
| `--prometheus-enabled` | Enable Prometheus metrics endpoint at /metrics | `PROMETHEUS_ENABLED` | `false` |
| `--log-level` | Set log level: debug, info, warn, error | `LOG_LEVEL` | `info` |
| `--otel-grpc-url` | OpenTelemetry gRPC URL (omit for stdout) | `OTEL_GRPC_URL` | - |

### Server Configuration

#### 🔧 Essential Options

| Option | Description | Environment Variable | Required |
| ----------------------- | ------------------------------------------------ | ---------------------- | -------- |
| `--cache-hostname` | **Cache hostname for key generation** | `CACHE_HOSTNAME` | ✅ |
| `--cache-storage-local` | Local storage directory (use this OR S3 storage) | `CACHE_STORAGE_LOCAL` | ✅ (1) |
| `--cache-upstream-url` | Upstream cache URL (repeatable) | `CACHE_UPSTREAM_URLS` | ✅ |
| `--cache-upstream-public-key` | Upstream public key (repeatable) | `CACHE_UPSTREAM_PUBLIC_KEYS` | ✅ |

**Note (1):** Either `--cache-storage-local` OR S3 storage flags (see below) are required, but not both.

#### ☁️ S3 Storage Options (NEW in v0.5.0)

Use these options for S3-compatible storage instead of `--cache-storage-local`:

| Option | Description | Environment Variable | Required for S3 | Default |
| ----------------------------------------- | --------------------------------------------------------------------- | ---------------------------------------- | --------------- | ------- |
| `--cache-storage-s3-bucket` | S3 bucket name | `CACHE_STORAGE_S3_BUCKET` | ✅ | - |
| `--cache-storage-s3-endpoint` | S3 endpoint URL with scheme (e.g., https://s3.amazonaws.com, http://localhost:9000) | `CACHE_STORAGE_S3_ENDPOINT` | ✅ | - |
| `--cache-storage-s3-access-key-id` | S3 access key ID | `CACHE_STORAGE_S3_ACCESS_KEY_ID` | ✅ | - |
| `--cache-storage-s3-secret-access-key` | S3 secret access key | `CACHE_STORAGE_S3_SECRET_ACCESS_KEY` | ✅ | - |
| `--cache-storage-s3-region` | S3 region (optional for some providers) | `CACHE_STORAGE_S3_REGION` | - | - |
| `--cache-storage-s3-force-path-style` | Force path-style S3 addressing (required for MinIO) | `CACHE_STORAGE_S3_FORCE_PATH_STYLE` | - | `false` |

**S3 Storage Examples:**

<details>
<summary><strong>AWS S3</strong></summary>

```bash
ncps serve \
  --cache-hostname=ncps.example.com \
  --cache-storage-s3-bucket=my-ncps-cache \
  --cache-storage-s3-endpoint=https://s3.amazonaws.com \
  --cache-storage-s3-region=us-east-1 \
  --cache-storage-s3-access-key-id=AKIAIOSFODNN7EXAMPLE \
  --cache-storage-s3-secret-access-key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY \
  --cache-database-url=sqlite:/var/lib/ncps/db.sqlite \
  --cache-upstream-url=https://cache.nixos.org \
  --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

</details>

<details>
<summary><strong>MinIO</strong></summary>

```bash
ncps serve \
  --cache-hostname=ncps.example.com \
  --cache-storage-s3-bucket=ncps-cache \
  --cache-storage-s3-endpoint=http://minio.example.com:9000 \
  --cache-storage-s3-access-key-id=minioadmin \
  --cache-storage-s3-secret-access-key=minioadmin \
  --cache-storage-s3-force-path-style \
  --cache-database-url=sqlite:/var/lib/ncps/db.sqlite \
  --cache-upstream-url=https://cache.nixos.org \
  --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

</details>

#### 📊 Database & Performance

| Option | Description | Environment Variable | Default |
| ---------------------- | ---------------------------------------------------------- | -------------------- | --------------- |
| `--cache-database-url` | Database URL (sqlite://, postgresql://, or mysql://) | `CACHE_DATABASE_URL` | embedded SQLite |
| `--cache-max-size` | Max cache size (5K, 10G, etc.) | `CACHE_MAX_SIZE` | unlimited |
| `--cache-lru-schedule` | Cleanup cron schedule | `CACHE_LRU_SCHEDULE` | - |
| `--cache-temp-path` | Temporary download directory | `CACHE_TEMP_PATH` | system temp |

#### 🔐 Security & Signing

| Option | Description | Environment Variable | Default |
| --------------------------- | ------------------------------------ | ------------------------- | -------------- |
| `--cache-sign-narinfo` | Sign narInfo files | `CACHE_SIGN_NARINFO` | `true` |
| `--cache-secret-key-path` | Path to signing key | `CACHE_SECRET_KEY_PATH` | auto-generated |
| `--cache-allow-put-verb` | Allow PUT uploads | `CACHE_ALLOW_PUT_VERB` | `false` |
| `--cache-allow-delete-verb` | Allow DELETE operations | `CACHE_ALLOW_DELETE_VERB` | `false` |
| `--netrc-file` | Path to netrc file for upstream auth | `NETRC_FILE` | `~/.netrc` |

#### ⏱️ Upstream Connection Timeouts

Configure timeout values for upstream cache connections. Increase these values if you experience timeout errors with slow or remote upstream caches.

| Option | Description | Environment Variable | Default |
| -------------------------------------------- | ---------------------------------------------------------- | ----------------------------------------------- | ------- |
| `--cache-upstream-dialer-timeout` | Timeout for establishing TCP connections to upstream caches | `CACHE_UPSTREAM_DIALER_TIMEOUT` | `3s` |
| `--cache-upstream-response-header-timeout` | Timeout for waiting for upstream server's response headers | `CACHE_UPSTREAM_RESPONSE_HEADER_TIMEOUT` | `3s` |

**Common timeout values:**

- `3s` - Default, works for most local/fast upstreams
- `10s` - Recommended for slow networks or distant upstreams
- `30s` - For very slow connections (e.g., satellite, slow VPN)

**Example:** For slow upstream connections, increase timeouts:

```bash
ncps serve \
  --cache-hostname=ncps.example.com \
  --cache-storage-local=/var/lib/ncps \
  --cache-upstream-url=https://cache.nixos.org \
  --cache-upstream-dialer-timeout=10s \
  --cache-upstream-response-header-timeout=10s
```

#### 🌐 Network

| Option | Description | Environment Variable | Default |
| --------------- | ----------------------- | -------------------- | ------- |
| `--server-addr` | Listen address and port | `SERVER_ADDR` | `:8501` |

## 🏗️ High Availability Deployments

**NEW in distributed-lock branch:** ncps now supports running multiple instances for high availability!

### Why High Availability?

Running multiple ncps instances provides:

- ✅ **Zero Downtime** - Instance failures don't interrupt service
- ✅ **Load Distribution** - Requests spread across multiple servers
- ✅ **Horizontal Scaling** - Add instances to handle more traffic
- ✅ **Geographic Distribution** - Deploy instances closer to clients

### Requirements for HA

To run ncps in high-availability mode, you need:

1. **Shared Storage** - All instances must access the same storage:
   - **S3-compatible storage** (AWS S3, MinIO, etc.), OR
   - **Shared filesystem** (NFS, GlusterFS, etc.) mounted on all instances
1. **Shared Database** - All instances must connect to the same PostgreSQL or MySQL database
   - **SQLite is NOT supported** in HA mode (doesn't support concurrent writes from multiple processes)
1. **Redis Server** - Provides distributed locking for coordination between instances

### Quick Start (HA Setup)

**Option 1: Using S3-compatible storage**

```bash
# Start Redis for distributed locking
redis-server

# Configure and start instance 1
docker run -d \
  --name ncps-1 \
  -p 8501:8501 \
  kalbasit/ncps:latest serve \
  --cache-hostname=cache.example.com \
  --cache-database-url=postgresql://user:pass@postgres:5432/ncps \
  --cache-storage-s3-bucket=ncps-cache \
  --cache-storage-s3-endpoint=https://s3.amazonaws.com \
  --cache-storage-s3-region=us-east-1 \
  --cache-redis-addrs=redis:6379

# Start instance 2 (same configuration, different port)
docker run -d \
  --name ncps-2 \
  -p 8502:8501 \
  kalbasit/ncps:latest serve \
  --cache-hostname=cache.example.com \
  --cache-database-url=postgresql://user:pass@postgres:5432/ncps \
  --cache-storage-s3-bucket=ncps-cache \
  --cache-storage-s3-endpoint=https://s3.amazonaws.com \
  --cache-storage-s3-region=us-east-1 \
  --cache-redis-addrs=redis:6379

# Set up load balancer (nginx, haproxy, etc.) to distribute traffic
```

**Option 2: Using shared filesystem (NFS)**

```bash
# Ensure NFS share is mounted on all instances at /mnt/ncps-cache

# Start instance 1
docker run -d \
  --name ncps-1 \
  -p 8501:8501 \
  -v /mnt/ncps-cache:/var/lib/ncps \
  kalbasit/ncps:latest serve \
  --cache-hostname=cache.example.com \
  --cache-database-url=postgresql://user:pass@postgres:5432/ncps \
  --cache-storage-local=/var/lib/ncps \
  --cache-redis-addrs=redis:6379

# Start instance 2 (mounting the same NFS share)
docker run -d \
  --name ncps-2 \
  -p 8502:8501 \
  -v /mnt/ncps-cache:/var/lib/ncps \
  kalbasit/ncps:latest serve \
  --cache-hostname=cache.example.com \
  --cache-database-url=postgresql://user:pass@postgres:5432/ncps \
  --cache-storage-local=/var/lib/ncps \
  --cache-redis-addrs=redis:6379
```

### Redis Configuration

| Option | Description | Environment Variable | Default |
| -------------------------------- | ---------------------------------------------------- | -------------------------------- | -------------- |
| `--cache-redis-addrs` | Redis server addresses (comma-separated for cluster) | `CACHE_REDIS_ADDRS` | (none) |
| `--cache-redis-username` | Redis username (for ACL authentication) | `CACHE_REDIS_USERNAME` | "" |
| `--cache-redis-password` | Redis password | `CACHE_REDIS_PASSWORD` | "" |
| `--cache-redis-db` | Redis database number (0-15) | `CACHE_REDIS_DB` | 0 |
| `--cache-redis-use-tls` | Enable TLS for Redis connections | `CACHE_REDIS_USE_TLS` | false |
| `--cache-redis-pool-size` | Connection pool size | `CACHE_REDIS_POOL_SIZE` | 10 |
| `--cache-redis-key-prefix` | Key prefix for all locks | `CACHE_REDIS_KEY_PREFIX` | "ncps:lock:" |
| `--cache-lock-download-ttl` | Lock timeout for downloads | `CACHE_LOCK_DOWNLOAD_TTL` | 5m |
| `--cache-lock-lru-ttl` | Lock timeout for LRU operations | `CACHE_LOCK_LRU_TTL` | 30m |
| `--cache-lock-retry-max-attempts` | Maximum lock retry attempts | `CACHE_LOCK_RETRY_MAX_ATTEMPTS` | 3 |
| `--cache-lock-retry-initial-delay` | Initial retry delay | `CACHE_LOCK_RETRY_INITIAL_DELAY` | 100ms |
| `--cache-lock-retry-max-delay` | Maximum retry delay (exponential backoff cap) | `CACHE_LOCK_RETRY_MAX_DELAY` | 2s |
| `--cache-lock-retry-jitter` | Enable jitter in retry delays | `CACHE_LOCK_RETRY_JITTER` | true |

**Note:** If `--cache-redis-addrs` is not provided, ncps runs in single-instance mode using local locks.

### How Distributed Locking Works

ncps uses Redis to coordinate multiple instances:

- **Download Deduplication** - Only one instance downloads each package, others wait and share the result
- **LRU Coordination** - Only one instance runs cache cleanup at a time
- **Retry with Backoff** - Failed lock acquisitions retry with exponential backoff and jitter
- **Lock Expiry** - Locks automatically expire (via TTL) to prevent deadlocks

This prevents duplicate downloads and ensures cache consistency across all instances.

### Monitoring HA Deployments

See the [distributed locking documentation](docs/distributed-locking.md) for:

- OpenTelemetry metrics for lock operations
- Monitoring dashboards
- Troubleshooting guide
- Performance tuning

## 🔧 Client Setup

### Get Your Public Key

First, retrieve the public key from your running ncps instance:

```bash
curl http://your-ncps-hostname:8501/pubkey
```

### NixOS Configuration

Add ncps to your `configuration.nix`:

```nix
nix.settings = {
  substituters = [
    "http://your-ncps-hostname:8501"  # Use https:// if behind reverse proxy
    "https://cache.nixos.org"
    # ... other substituters
  ];

  trusted-public-keys = [
    "your-ncps-hostname=<paste-public-key-here>"
    "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="
    # ... other keys
  ];
};
```

### Non-NixOS Configuration

Edit your `nix.conf` file (typically `/etc/nix/nix.conf` or `~/.config/nix/nix.conf`):

```ini
substituters = http://your-ncps-hostname:8501 https://cache.nixos.org
trusted-public-keys = your-ncps-hostname=<paste-public-key-here> cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

## 🔧 Troubleshooting

<details>
<summary><strong>🐳 Docker Issues</strong></summary>

### "no such table: nars" Error

**Cause:** Database not properly initialized

**Solutions:**

1. ✅ **Run migration first:**

   ```bash
   docker run --rm -v ncps-storage:/storage kalbasit/ncps /bin/sh -c \
     "mkdir -m 0755 -p /storage/var && mkdir -m 0700 -p /storage/var/ncps && mkdir -m 0700 -p /storage/var/ncps/db && /bin/dbmate --url=sqlite:/storage/var/ncps/db/db.sqlite migrate up"
   ```

1. ✅ **Check database path consistency** between migration and application

1. ✅ **Verify directory permissions** (0700 for database directory)

### "unable to open database file" Error

**Cause:** Permissions or volume mounting issues

**Solutions:**

- ✅ Ensure storage volume is mounted to `/storage`
- ✅ Check directory permissions
- ✅ For bind mounts, ensure host directory is writable

### Container Exits Immediately

**Cause:** Missing required parameters

**Required options:**

- ✅ `--cache-hostname`
- ✅ `--cache-storage-local` or (`--cache-storage-s3-bucket` and other `--cache-storage-s3-*` such as endpoint and credentials)
- ✅ `--cache-database-url`
- ✅ At least one `--cache-upstream-url` and `--cache-upstream-public-key`

</details>

<details>
<summary><strong>🔍 General Issues</strong></summary>

### Cache Not Working

1. **Check public key setup:**

   ```bash
   curl http://your-ncps-hostname:8501/pubkey
   ```

1. **Verify Nix configuration:**

   ```bash
   nix show-config | grep substituters
   nix show-config | grep trusted-public-keys
   ```

1. **Test cache connectivity:**

   ```bash
   curl http://your-ncps-hostname:8501/nix-cache-info
   ```

### Performance Issues

- ✅ Check available disk space
- ✅ Monitor cache hit rates in logs
- ✅ Consider adjusting `--cache-max-size`
- ✅ Review LRU cleanup schedule

</details>

## 🤝 Contributing

Contributions are welcome! We appreciate bug reports, feature requests, documentation improvements, and code contributions.

### Quick Start for Contributors

1. **Clone the repository:**

   ```bash
   git clone https://github.com/kalbasit/ncps.git
   cd ncps
   ```

1. **Start development server:**

   ```bash
   # Using local filesystem storage (default, no dependencies required)
   ./dev-scripts/run.sh

   # Using S3/MinIO storage (requires dependencies)
   nix run .#deps  # In a separate terminal
   ./dev-scripts/run.sh s3
   ```

1. **Make your changes and test:**

   ```bash
   # Format code
   nix fmt

   # Run linter with auto-fix
   golangci-lint run --fix

   # Run tests with race detector
   go test -race ./...
   ```

1. **Submit a pull request**

For detailed contribution guidelines, development workflow, code quality standards, and testing procedures, please see **[CONTRIBUTING.md](CONTRIBUTING.md)**.

### Getting Help

- 📖 Read the [Contributing Guide](CONTRIBUTING.md)
- 🐛 Report bugs via [issues](https://github.com/kalbasit/ncps/issues)
- 💬 Ask questions in [discussions](https://github.com/kalbasit/ncps/discussions)
- 📧 Contact maintainers for security issues

______________________________________________________________________

## 📄 License

This project is licensed under the **MIT License** - see the [LICENSE](/LICENSE) file for details.

______________________________________________________________________

<div align="center">

**⭐ Found this helpful? Give us a star!**

[Report Bug](https://github.com/kalbasit/ncps/issues) • [Request Feature](https://github.com/kalbasit/ncps/issues) • [Contribute](https://github.com/kalbasit/ncps/pulls)

</div>
