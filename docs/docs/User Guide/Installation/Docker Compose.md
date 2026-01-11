# Docker Compose

## Docker Compose Installation

Install ncps using Docker Compose for automated, reproducible deployments. This method is ideal for production single-instance deployments and development environments.

## Prerequisites

- Docker and Docker Compose installed
- Basic familiarity with Docker Compose
- 2GB+ available disk space

## Basic Setup (Local Storage)

### Step 1: Create docker-compose.yml

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

**Important:** Replace `your-ncps-hostname` with your actual hostname.

### Step 2: Start the Services

```
docker compose up -d
```

This will:

1. Create directories with correct permissions
1. Run database migrations
1. Start ncps server

### Step 3: Verify Installation

```
# Check services are running
docker compose ps

# View logs
docker compose logs ncps

# Test the cache
curl http://localhost:8501/nix-cache-info
curl http://localhost:8501/pubkey
```

## Advanced Setup (S3 Storage with HA)

For high-availability setups with S3 storage, PostgreSQL, and Redis:

```yaml
services:
  # PostgreSQL Database
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: ncps
      POSTGRES_USER: ncps
      POSTGRES_PASSWORD: changeme
    volumes:
      - postgres-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ncps"]
      interval: 10s
      timeout: 5s
      retries: 5
    restart: unless-stopped

  # Redis for Distributed Locking
  redis:
    image: redis:7-alpine
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5
    restart: unless-stopped

  # MinIO (S3-compatible storage)
  minio:
    image: minio/minio:latest
    command: server /data --console-address ":9001"
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
    volumes:
      - minio-data:/data
    ports:
      - "9000:9000"
      - "9001:9001"
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:9000/minio/health/live"]
      interval: 10s
      timeout: 5s
      retries: 5
    restart: unless-stopped

  # Create MinIO bucket
  createbuckets:
    image: minio/mc:latest
    depends_on:
      minio:
        condition: service_healthy
    entrypoint: >
      /bin/sh -c "
      /usr/bin/mc alias set myminio http://minio:9000 minioadmin minioadmin;
      /usr/bin/mc mb myminio/ncps-cache --ignore-existing;
      exit 0;
      "
    restart: "no"

  # Migrate database
  migrate-database:
    image: kalbasit/ncps:latest
    depends_on:
      postgres:
        condition: service_healthy
    command: >
      /bin/dbmate --url=postgresql://ncps:changeme@postgres:5432/ncps?sslmode=disable migrate up
    restart: "no"

  # ncps instance 1
  ncps-1:
    image: kalbasit/ncps:latest
    depends_on:
      migrate-database:
        condition: service_completed_successfully
      redis:
        condition: service_healthy
      createbuckets:
        condition: service_completed_successfully
    ports:
      - "8501:8501"
    environment:
      CACHE_HOSTNAME: cache.example.com
      CACHE_STORAGE_S3_BUCKET: ncps-cache
      CACHE_STORAGE_S3_ENDPOINT: http://minio:9000
      CACHE_STORAGE_S3_ACCESS_KEY_ID: minioadmin
      CACHE_STORAGE_S3_SECRET_ACCESS_KEY: minioadmin
      CACHE_STORAGE_S3_USE_SSL: "false"
      CACHE_DATABASE_URL: postgresql://ncps:changeme@postgres:5432/ncps?sslmode=disable
      CACHE_REDIS_ADDRS: redis:6379
      CACHE_UPSTREAM_URLS: https://cache.nixos.org
      CACHE_UPSTREAM_PUBLIC_KEYS: cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
    command: /bin/ncps serve
    restart: unless-stopped

  # ncps instance 2 (for HA)
  ncps-2:
    image: kalbasit/ncps:latest
    depends_on:
      migrate-database:
        condition: service_completed_successfully
      redis:
        condition: service_healthy
      createbuckets:
        condition: service_completed_successfully
    ports:
      - "8502:8501"
    environment:
      CACHE_HOSTNAME: cache.example.com
      CACHE_STORAGE_S3_BUCKET: ncps-cache
      CACHE_STORAGE_S3_ENDPOINT: http://minio:9000
      CACHE_STORAGE_S3_ACCESS_KEY_ID: minioadmin
      CACHE_STORAGE_S3_SECRET_ACCESS_KEY: minioadmin
      CACHE_STORAGE_S3_USE_SSL: "false"
      CACHE_DATABASE_URL: postgresql://ncps:changeme@postgres:5432/ncps?sslmode=disable
      CACHE_REDIS_ADDRS: redis:6379
      CACHE_UPSTREAM_URLS: https://cache.nixos.org
      CACHE_UPSTREAM_PUBLIC_KEYS: cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
    command: /bin/ncps serve
    restart: unless-stopped

volumes:
  postgres-data:
  minio-data:
```

**This setup includes:**

- PostgreSQL for shared database
- Redis for distributed locking
- MinIO for S3-compatible storage
- Two ncps instances for high availability

**Access points:**

- ncps instance 1: [http://localhost:8501](http://localhost:8501)
- ncps instance 2: [http://localhost:8502](http://localhost:8502)
- MinIO console: [http://localhost:9001](http://localhost:9001)

## Using Configuration File

Create a `config.yaml`:

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

Update `docker-compose.yml`:

```yaml
services:
  ncps:
    # ... other config ...
    volumes:
      - ncps-storage:/storage
      - ./config.yaml:/config.yaml:ro
    command: /bin/ncps serve --config=/config.yaml
```

## Management Commands

### Start Services

```
docker compose up -d
```

### Stop Services

```
docker compose down
```

### View Logs

```
docker compose logs ncps
docker compose logs -f ncps  # Follow logs
docker compose logs --tail 100  # Last 100 lines
```

### Restart ncps

```
docker compose restart ncps
```

### Update to Latest Version

```
docker compose pull
docker compose up -d
```

### Remove Everything

```
docker compose down -v  # WARNING: Deletes all data!
```

## Monitoring with Prometheus

Add Prometheus and Grafana to your stack:

```yaml
services:
  # ... existing services ...

  ncps:
    # ... existing config ...
    environment:
      PROMETHEUS_ENABLED: "true"
    # ... rest of config ...

  prometheus:
    image: prom/prometheus:latest
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - prometheus-data:/prometheus
    ports:
      - "9090:9090"
    command:
      - '--config.file=/etc/prometheus/prometheus.yml'
    restart: unless-stopped

  grafana:
    image: grafana/grafana:latest
    volumes:
      - grafana-data:/var/lib/grafana
    ports:
      - "3000:3000"
    environment:
      GF_SECURITY_ADMIN_PASSWORD: admin
    restart: unless-stopped

volumes:
  # ... existing volumes ...
  prometheus-data:
  grafana-data:
```

Create `prometheus.yml`:

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'ncps'
    static_configs:
      - targets: ['ncps:8501']
```

See <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> for Grafana dashboard setup.

## Troubleshooting

### Services Won't Start

```
# Check service status
docker compose ps

# Check logs
docker compose logs

# Check specific service
docker compose logs ncps
```

### Database Migration Fails

```
# Run migration manually
docker compose run --rm migrate-database
```

### Can't Connect to ncps

```
# Verify ncps is running
docker compose ps ncps

# Test from host
curl http://localhost:8501/nix-cache-info

# Test from within Docker network
docker compose exec ncps wget -O- http://localhost:8501/nix-cache-info
```

See the <a class="reference-link" href="../Operations/Troubleshooting.md">Troubleshooting</a> for more help.

## Next Steps

1. <a class="reference-link" href="../Usage/Client%20Setup.md">Client Setup</a> - Set up Nix clients to use your cache
1. <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> - Set up Prometheus and Grafana
1. <a class="reference-link" href="../Configuration/Reference.md">Reference</a> - Explore more options
1. <a class="reference-link" href="../Deployment.md">Deployment</a> - Consider deployment strategies

## Related Documentation

- <a class="reference-link" href="Docker.md">Docker</a> - Manual Docker setup
- <a class="reference-link" href="Kubernetes.md">Kubernetes</a> - For K8s environments
- <a class="reference-link" href="../Deployment/High%20Availability.md">High Availability</a> - HA setup guide
- <a class="reference-link" href="../Configuration/Reference.md">Reference</a> - All configuration options
