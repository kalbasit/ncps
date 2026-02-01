# Docker

## Docker Installation

Install and run ncps using Docker. This is the simplest installation method, perfect for testing and single-instance deployments.

## Prerequisites

- Docker installed (version 20.10 or later)
- Basic familiarity with Docker commands
- 2GB+ available disk space
- Network access to upstream caches

## Step 1: Pull the Image

```sh
docker pull ghcr.io/kalbasit/ncps
```

> [!NOTE]
> The image is also available on Docker Hub as `kalbasit/ncps` (or `docker.io/kalbasit/ncps`).

**Available tags:**

- `latest` - Latest stable release
- `vX.Y.Z` - Specific version (recommended for production)
- See [Docker Hub](https://hub.docker.com/r/kalbasit/ncps) or [GitHub packages](https://github.com/kalbasit/ncps/pkgs/container/ncps) for all tags

### Step 2: Initialize Storage and Database

```sh
# Create storage volume
docker volume create ncps-storage

# Create required directories with correct permissions
# Note: ncps runs as UID 1000 (ncps user), so directories must be owned by this user
docker run --rm -v ncps-storage:/storage alpine /bin/sh -c \
  "mkdir -m 0755 -p /storage/var && \
   mkdir -m 0700 -p /storage/var/ncps && \
   mkdir -m 0700 -p /storage/var/ncps/db && \
   chown -R 1000:1000 /storage"

# Initialize the database
docker run --rm -v ncps-storage:/storage ghcr.io/kalbasit/ncps \
  /bin/dbmate --url=sqlite:/storage/var/ncps/db/db.sqlite migrate up
```

**What this does:**

- Creates a Docker volume for persistent storage
- Sets up the directory structure
- **Sets ownership to UID 1000** (ncps user in the container)
- Runs database migrations to create required tables

**Important:** The ncps Docker container runs as a non-root user (`ncps`, UID 1000, GID 1000) for security. All storage directories must be owned by UID 1000 for the container to access them.

### Step 3: Start the Server

```
docker run -d \
  --name ncps \
  -p 8501:8501 \
  -v ncps-storage:/storage \
  --restart unless-stopped \
  ghcr.io/kalbasit/ncps \
  /bin/ncps serve \
  --cache-hostname=your-ncps-hostname \
  --cache-storage-local=/storage \
  --cache-database-url=sqlite:/storage/var/ncps/db/db.sqlite \
  --cache-upstream-url=https://cache.nixos.org \
  --cache-upstream-url=https://nix-community.cachix.org \
  --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY= \
  --cache-upstream-public-key=nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs=
```

**Important:** Replace `your-ncps-hostname` with the actual hostname or IP address where ncps will be accessible to clients.

**Flags explained:**

- `-d` - Run in detached mode (background)
- `--name ncps` - Container name for easy reference
- `-p 8501:8501` - Expose port 8501
- `-v ncps-storage:/storage` - Mount persistent volume
- `--restart unless-stopped` - Auto-restart on failures

### Step 4: Verify Installation

```sh
# Check container is running
docker ps | grep ncps

# View logs
docker logs ncps

# Test the cache info endpoint
curl http://localhost:8501/nix-cache-info

# Get your public key (save this!)
curl http://localhost:8501/pubkey
```

**Expected output:**

- Container status: "Up"
- Cache info: JSON with StoreDir, Priority, etc.
- Public key: `your-ncps-hostname:base64encodedkey`

## Using S3 Storage

For production deployments or HA setups, use S3-compatible storage instead of local storage:

```sh
# Create volume for database only (cache data goes to S3)
docker volume create ncps-db
docker run --rm -v ncps-db:/db alpine mkdir -m 0700 -p /db

# Initialize database
docker run --rm -v ncps-db:/db ghcr.io/kalbasit/ncps \
  /bin/dbmate --url=sqlite:/db/db.sqlite migrate up

# Start server with S3 storage
docker run -d \
  --name ncps \
  -p 8501:8501 \
  -v ncps-db:/db \
  --restart unless-stopped \
  ghcr.io/kalbasit/ncps \
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

**For MinIO:**

```sh
--cache-storage-s3-endpoint=http://minio.example.com:9000 \
--cache-storage-s3-use-ssl=false \
```

See <a class="reference-link" href="../Configuration/Storage.md">Storage</a> for more details.

## Using Environment Variables

Instead of command-line flags, you can use environment variables:

```
docker run -d \
  --name ncps \
  -p 8501:8501 \
  -v ncps-storage:/storage \
  -e CACHE_HOSTNAME=your-ncps-hostname \
  -e CACHE_STORAGE_LOCAL=/storage \
  -e CACHE_DATABASE_URL=sqlite:/storage/var/ncps/db/db.sqlite \
  -e CACHE_UPSTREAM_URLS=https://cache.nixos.org \
  -e CACHE_UPSTREAM_PUBLIC_KEYS=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY= \
  ghcr.io/kalbasit/ncps /bin/ncps serve
```

See <a class="reference-link" href="../Configuration/Reference.md">Reference</a> for all environment variables.

## Using a Configuration File

Create a `config.yaml` file:

```yaml
cache:
  hostname: your-ncps-hostname
  storage:
    local: /storage
  database-url: sqlite:/storage/var/ncps/db/db.sqlite
  upstream:
    urls:
      - https://cache.nixos.org
    public-keys:
      - cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

Then run:

```
docker run -d \
  --name ncps \
  -p 8501:8501 \
  -v ncps-storage:/storage \
  -v $(pwd)/config.yaml:/config.yaml:ro \
  ghcr.io/kalbasit/ncps \
  /bin/ncps serve --config=/config.yaml
```

## Management Commands

### View Logs

```
docker logs ncps
docker logs -f ncps  # Follow logs
docker logs --tail 100 ncps  # Last 100 lines
```

### Stop/Start/Restart

```
docker stop ncps
docker start ncps
docker restart ncps
```

### Update to Latest Version

```sh
docker pull ghcr.io/kalbasit/ncps:latest
docker stop ncps
docker rm ncps
# Then re-run the docker run command from Step 3
```

### Remove Everything

```
docker stop ncps
docker rm ncps
docker volume rm ncps-storage  # WARNING: Deletes all cached data!
```

## Troubleshooting

### Container Exits Immediately

**Check logs:**

```
docker logs ncps
```

**Common causes:**

- Missing required flags (--cache-hostname, storage, database, upstream)
- Database not initialized (missing migration step)
- Invalid configuration

### Can't Access [http://localhost:8501](http://localhost:8501)

**Check container is running:**

```sh
docker ps | grep ncps
```

**Check from inside container:**

```sh
docker exec ncps wget -O- http://localhost:8501/nix-cache-info
```

**Check port binding:**

```sh
docker port ncps
```

### Database Errors

**Symptom:** "no such table: nars"

**Solution:** Run the database migration step:

```sh
docker run --rm -v ncps-storage:/storage ghcr.io/kalbasit/ncps \
  /bin/dbmate --url=sqlite:/storage/var/ncps/db/db.sqlite migrate up
```

### Permission Errors

**Ensure correct permissions:**

```sh
docker run --rm -v ncps-storage:/storage alpine ls -la /storage/var/ncps
```

Database directory should be `drwx------` (0700).

See the <a class="reference-link" href="../Operations/Troubleshooting.md">Troubleshooting</a> for more help.

## Next Steps

1. <a class="reference-link" href="../Usage/Client%20Setup.md">Client Setup</a> - Set up Nix clients to use your cache
1. <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> - Enable Prometheus metrics
1. <a class="reference-link" href="../Configuration/Reference.md">Reference</a> - Explore more options
1. **Consider** <a class="reference-link" href="Docker%20Compose.md">Docker Compose</a> - For easier management

## Related Documentation

- <a class="reference-link" href="Docker%20Compose.md">Docker Compose</a> - Automated Docker setup
- <a class="reference-link" href="../Configuration/Reference.md">Reference</a> - All configuration options
- <a class="reference-link" href="../Configuration/Storage.md">Storage</a> - Local vs S3 storage
- <a class="reference-link" href="../Operations.md">Operations</a> - Monitoring, backup, and maintenance
