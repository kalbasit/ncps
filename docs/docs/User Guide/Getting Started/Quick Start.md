# Quick Start

## Quick Start Guide

Get ncps up and running in minutes. This guide shows you the fastest path from zero to a working ncps instance.

## Prerequisites

- Docker installed and running
- Basic understanding of Nix package manager
- Network access to upstream caches (cache.nixos.org)

## Option 1: Local Storage (Recommended for Getting Started)

This is the simplest setup perfect for testing and single-machine deployments.

### Step 1: Pull Required Images

```
docker pull alpine
docker pull kalbasit/ncps
```

### Step 2: Create and Initialize Storage

```
# Create storage volume
docker volume create ncps-storage

# Create required directories
docker run --rm -v ncps-storage:/storage alpine /bin/sh -c \
  "mkdir -m 0755 -p /storage/var && mkdir -m 0700 -p /storage/var/ncps && mkdir -m 0700 -p /storage/var/ncps/db"
```

### Step 3: Initialize Database

```
docker run --rm -v ncps-storage:/storage kalbasit/ncps \
  /bin/dbmate --url=sqlite:/storage/var/ncps/db/db.sqlite up
```

### Step 4: Start ncps

```
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
  --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

**Replace `your-ncps-hostname`** with the actual hostname or IP where ncps will be accessible to clients.

### Step 5: Verify Installation

```
# Check container is running
docker ps | grep ncps

# Test the cache info endpoint
curl http://localhost:8501/nix-cache-info

# Get your public key (needed for client setup)
curl http://localhost:8501/pubkey
```

You should see output showing cache information and a public key. Save this public key for configuring clients!

## Option 2: S3 Storage (For Production/HA)

This setup uses S3-compatible storage, which is required for high-availability deployments.

### Step 1: Pull Image

```
docker pull kalbasit/ncps
```

### Step 2: Create Database Volume

```
# Create volume for database (cache data goes to S3)
docker volume create ncps-db
docker run --rm -v ncps-db:/db alpine mkdir -m 0700 -p /db
```

### Step 3: Initialize Database

```
docker run --rm -v ncps-db:/db kalbasit/ncps \
  /bin/dbmate --url=sqlite:/db/db.sqlite up
```

### Step 4: Start ncps with S3

```
docker run -d \
  --name ncps \
  -p 8501:8501 \
  -v ncps-db:/db \
  kalbasit/ncps \
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

**Replace:**

- `my-ncps-cache` with your S3 bucket name
- `YOUR_ACCESS_KEY` and `YOUR_SECRET_KEY` with your S3 credentials
- Adjust `endpoint` and `region` for your S3 provider

### Step 5: Verify Installation

```
# Check logs
docker logs ncps

# Test the cache
curl http://localhost:8501/nix-cache-info
curl http://localhost:8501/pubkey
```

## What's Running?

Your ncps instance is now:

- Listening on port 8501
- Caching packages from cache.nixos.org
- Storing data in Docker volume (local) or S3 bucket
- Signing cached paths with its own private key
- Ready to serve Nix clients

## Next Steps

1. <a class="reference-link" href="../Usage/Client%20Setup.md">Client Setup</a> - Set up your Nix clients to use ncps
1. <a class="reference-link" href="Concepts.md">Concepts</a> - Learn how ncps works under the hood
1. <a class="reference-link" href="../Installation.md">Installation</a> - Pick the best installation method for your needs
1. <a class="reference-link" href="../Configuration/Reference.md">Reference</a> - Explore more configuration options

## Quick Troubleshooting

**Container exits immediately:**

- Check you provided all required flags: `--cache-hostname`, storage, database, upstream
- Check logs: `docker logs ncps`

**Can't access [http://localhost:8501](http://localhost:8501):**

- Verify container is running: `docker ps | grep ncps`
- Check port mapping: `-p 8501:8501`
- Try from inside container: `docker exec ncps wget -O- http://localhost:8501/nix-cache-info`

**Database errors:**

- Ensure you ran the database migration step
- Verify database path matches between migration and serve commands

See the <a class="reference-link" href="../Operations/Troubleshooting.md">Troubleshooting</a> for more help.

## Related Documentation

- <a class="reference-link" href="../Installation/Docker.md">Docker</a> - Detailed Docker setup
- <a class="reference-link" href="../Installation/Docker%20Compose.md">Docker Compose</a> - Automated setup
- <a class="reference-link" href="../Configuration/Reference.md">Reference</a> - All configuration options
