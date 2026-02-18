# Concepts

## Core Concepts

This guide explains what ncps is, the problems it solves, and how it works under the hood.

## What is ncps?

ncps (Nix Cache Proxy Server) is a high-performance proxy server that acts as a local binary cache for Nix. It fetches store paths from upstream caches (like cache.nixos.org) and stores them locally, reducing download times and bandwidth usage.

## The Problem

When multiple machines running NixOS or Nix pull packages, they often download the same dependencies from remote caches, leading to:

- **Redundant downloads** - Each machine downloads identical files
- **High bandwidth usage** - Significant network traffic for large projects
- **Slower build times** - Network latency impacts development velocity
- **Internet dependency** - Every build requires external connectivity

### Real-World Example

Imagine a team of 10 developers all working on the same Nix-based project:

- A large dependency (500MB) gets updated
- Without ncps: 10 machines × 500MB = 5GB of internet bandwidth used
- With ncps: 500MB downloaded once from internet, then served locally 9 times

## The Solution

ncps solves these issues by acting as a **centralized cache** on your local network:

1. **Single Download**: Package downloaded once from upstream
1. **Local Distribution**: Served to all local machines from cache
1. **Bandwidth Savings**: Dramatic reduction in internet usage
1. **Faster Builds**: Local network speeds vs internet speeds
1. **Offline Capability**: Cached packages available without internet

## How It Works

### Request Flow

```
┌────────────┐
│ Nix Client │
└─────┬──────┘
      │ 1. Request store path
      ▼
┌─────────────┐
│    ncps     │
└─────┬───────┘
      │ 2. Check cache
      ▼
┌──────────────┐      ┌─────────────┐
│ Cache exists?│──No──▶│  Download   │
└──────┬───────┘      │ from upstream│
       │Yes           └──────┬───────┘
       │                     │
       │ ◀───────────────────┘
       │ 3. Cache & sign
       ▼
┌──────────────┐
│ Serve to     │
│ client       │
└──────────────┘
```

**Step-by-step:**

1. **Request** - Nix client requests a store path (e.g., `/nix/store/abc123-package`)
1. **Cache Check** - ncps checks if NarInfo metadata exists in database
1. **Cache Hit** - If cached, serve directly from storage
1. **Cache Miss** - If not cached:
   - Fetch NarInfo and NAR from upstream cache
   - Store NAR file in storage backend
   - Store NarInfo metadata in database
   - Sign NarInfo with ncps private key
1. **Serve** - Deliver the path to the requesting client

### Storage Architecture

ncps uses a flexible storage architecture with separate components for different types of data:

```
┌─────────────────────────────────────┐
│            ncps Server              │
└──────┬──────────────────┬───────────┘
       │                  │
       ▼                  ▼
┌──────────────┐   ┌──────────────────┐
│   Database   │   │  Storage Backend │
│              │   │                  │
│ - NarInfo    │   │ - NAR files      │
│ - Metadata   │   │ - Secret keys    │
│ - Cache size │   │                  │
└──────────────┘   └──────────────────┘
```

#### Database Backend (Metadata)

Stores metadata about cached packages:

- **SQLite** (default): Embedded, no external dependencies, single-instance only
- **PostgreSQL**: Production-ready, supports concurrent access, required for HA
- **MySQL/MariaDB**: Production-ready, supports concurrent access, works for HA

#### Storage Backend (Binary Data)

Stores actual package files:

- **Local Filesystem**: Traditional file storage, simple setup, single-instance
- **S3-Compatible**: AWS S3, MinIO, etc., required for HA, scalable

### Key Concepts

#### NAR (Nix ARchive)

A NAR is an archive format containing the actual package files. When you install a package, Nix downloads the NAR and unpacks it into `/nix/store`.

- Binary package data
- Typically compressed (xz, zstd)
- Can be very large (500MB+ for some packages)
- Stored in the storage backend
- **NAR Compression Normalization**: `ncps` normalizes NAR compression internally to improve storage efficiency and client compatibility.
  - Uncompressed NARs are stored compressed with `zstd` to reduce storage usage.
  - When serving, `ncps` provides transparent decompression so clients always receive the format they expect.
  - It also re-compresses on-the-fly based on the encoding the client advertises (e.g., `zstd`, `brotli`, `gzip`, or raw).

#### NarInfo

NarInfo is metadata about a NAR file:

- Hash and size of the NAR
- Compression type
- References to other store paths
- Digital signature
- Stored in the database

#### Signing

ncps signs all cached NarInfo files with its own private key:

- Clients trust ncps by adding its public key to their configuration
- Ensures integrity and authenticity of cached packages
- Private key generated automatically on first run
- Public key available at `http://your-ncps/pubkey`

#### Upstream Caches

ncps fetches packages from configured upstream caches:

- Primary: `cache.nixos.org` (official Nix cache)
- Secondary: Custom caches, Cachix, etc.
- Failover support: tries next upstream if one fails
- Respects upstream public keys for verification

### Deployment Modes

#### Single-Instance Mode

```
┌─────────────────┐
│  Nix Clients    │
│  (1-100+)       │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   ncps Server   │
│                 │
│ Local Locks     │
│ (sync.Mutex)    │
└────────┬────────┘
         │
    ┌────┴────┐
    ▼         ▼
┌────────┐ ┌───────┐
│Storage │ │SQLite │
│(Local  │ │  or   │
│or S3)  │ │PG/SQL │
└────────┘ └───────┘
```

**Characteristics:**

- Single ncps server
- Local locks (no Redis needed)
- Simple to set up and manage
- Perfect for teams up to 100+ users
- Can use any storage and database option

**When to Use:**

- Development teams
- Single location deployments
- Simpler operations preferred
- No need for zero-downtime updates

#### High Availability Mode

```
┌────────────────────────┐
│    Load Balancer       │
└────────┬───────────────┘
         │
    ┌────┴────┬────────┐
    ▼         ▼        ▼
┌────────┐┌────────┐┌────────┐
│ ncps 1 ││ ncps 2 ││ ncps 3 │
└───┬────┘└───┬────┘└───┬────┘
    │         │         │
    └────┬────┴────┬────┘
         │         │
         ▼         ▼
    ┌────────┐ ┌──────┐
    │ Redis  │ │  S3  │
    │(Locks) │ │      │
    └────────┘ │  +   │
               │      │
               │ PG/  │
               │MySQL │
               └──────┘
```

**Characteristics:**

- Multiple ncps instances (2+)
- Redis for distributed locking
- Shared S3 storage (required)
- Shared PostgreSQL/MySQL (required, SQLite NOT supported)
- Load balancer distributes requests

**When to Use:**

- Production deployments
- Need zero-downtime updates
- Geographic distribution
- Very high traffic (1000+ users)
- SLA requirements

**Key Features:**

- **Download Deduplication**: Only one instance downloads each package
- **LRU Coordination**: Only one instance runs cache cleanup at a time
- **Automatic Failover**: Instance failures don't interrupt service
- **Horizontal Scaling**: Add instances to handle more load

See the <a class="reference-link" href="../Deployment/High%20Availability.md">High Availability</a> for detailed setup instructions.

## Performance Characteristics

### Cache Hit Rates

Typical cache hit rates depend on usage patterns:

- Stable environments: 80-95% hit rate
- Active development: 50-80% hit rate
- Fresh installations: 0-20% hit rate (builds up over time)

### Speed Improvements

Typical speed improvements with ncps:

- **Local network**: 10-100× faster than internet download
- **Example**: 1Gbps LAN vs 100Mbps internet = 10× faster
- **Latency**: Sub-millisecond vs 10-100ms to upstream

### Storage Requirements

Plan storage based on usage:

- **Small team (5-10 users)**: 20-50GB
- **Medium team (10-50 users)**: 50-200GB
- **Large team (50+ users)**: 200GB-1TB+
- **LRU cleanup**: Automatically manages size with `--cache-max-size`

## Next Steps

Now that you understand how ncps works:

1. <a class="reference-link" href="../Installation.md">Installation</a> - Docker, Kubernetes, NixOS, etc.
1. <a class="reference-link" href="../Configuration/Storage.md">Storage</a> - Local vs S3
1. <a class="reference-link" href="../Configuration/Database.md">Database</a> - SQLite vs PostgreSQL/MySQL
1. <a class="reference-link" href="../Deployment.md">Deployment</a> - Single-instance vs High Availability
1. <a class="reference-link" href="../Usage/Client%20Setup.md">Client Setup</a> - Configure Nix to use your cache

## Related Documentation

- <a class="reference-link" href="Quick%20Start.md">Quick Start</a> - Get ncps running quickly
- [Deep dive into internals](../../Developer%20Guide/Architecture.md) - Deep dive into internals
- <a class="reference-link" href="../Configuration/Reference.md">Reference</a> - All configuration options
