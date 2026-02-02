#!/usr/bin/env bash
# NCPS K8s Tests Library
# Shell functions for rendering Helm values files from Nix configuration

# Render a complete Helm values file for a permutation
render_values_file() {
  local perm_json="$1"
  local cluster_json="$2"
  local image_registry="$3"
  local image_repository="$4"
  local image_tag="$5"

  # Extract basic permutation fields
  local name=$(echo "$perm_json" | jq -r '.name')
  local description=$(echo "$perm_json" | jq -r '.description')
  local replicas=$(echo "$perm_json" | jq -r '.replicas')
  local mode=$(echo "$perm_json" | jq -r '.mode // empty')
  local migration_mode=$(echo "$perm_json" | jq -r '.migration.mode')

  # Generate YAML
  cat <<EOF
# ${description}
# Auto-generated from config.nix

image:
  registry: ${image_registry}
  repository: ${image_repository}
  tag: "${image_tag}"

replicaCount: ${replicas}
$([ -n "$mode" ] && [ "$mode" != "null" ] && echo "mode: ${mode}")

migration:
  enabled: true
  mode: ${migration_mode}

config:
  analytics:
    reporting:
      enabled: false

  hostname: "ncps-${name}.local"

$(render_storage_config "$perm_json" "$cluster_json")

$(render_database_config "$perm_json" "$cluster_json")

$(render_lock_config "$perm_json")

$(render_redis_config "$perm_json" "$cluster_json")

$(render_cdc_config "$perm_json")
$(render_pod_disruption_budget "$perm_json")
$(render_affinity "$perm_json")
EOF
}

# Render storage configuration section
render_storage_config() {
  local perm_json="$1"
  local cluster_json="$2"

  local storage_type=$(echo "$perm_json" | jq -r '.storage.type')

  case "$storage_type" in
    local)
      render_local_storage "$perm_json"
      ;;
    s3)
      render_s3_storage "$perm_json" "$cluster_json"
      ;;
  esac
}

# Render local storage configuration
render_local_storage() {
  local perm_json="$1"

  local path=$(echo "$perm_json" | jq -r '.storage.local.path')
  local persistence_enabled=$(echo "$perm_json" | jq -r '.storage.local.persistence.enabled')
  local persistence_size=$(echo "$perm_json" | jq -r '.storage.local.persistence.size')

  cat <<EOF
  storage:
    type: local
    local:
      path: ${path}
      persistence:
        enabled: ${persistence_enabled}
        size: ${persistence_size}
EOF
}

# Render S3 storage configuration
render_s3_storage() {
  local perm_json="$1"
  local cluster_json="$2"

  local use_existing_secret=$(echo "$perm_json" | jq -r '.storage.useExistingSecret // false')
  local existing_secret_name=$(echo "$perm_json" | jq -r '.storage.existingSecretName // empty')

  # Extract S3 credentials from cluster
  local s3_bucket=$(echo "$cluster_json" | jq -r '.s3.bucket')
  local s3_endpoint=$(echo "$cluster_json" | jq -r '.s3.endpoint')
  local s3_access_key=$(echo "$cluster_json" | jq -r '.s3.access_key')
  local s3_secret_key=$(echo "$cluster_json" | jq -r '.s3.secret_key')

  # Check if we need local persistence (for SQLite with S3 storage)
  local has_local_persistence=$(echo "$perm_json" | jq -r '.storage.local.persistence.enabled // false')

  cat <<EOF
  storage:
    type: s3
    s3:
      bucket: ${s3_bucket}
      endpoint: ${s3_endpoint}
      region: us-east-1
EOF

  if [ "$use_existing_secret" = "true" ] && [ -n "$existing_secret_name" ]; then
    cat <<EOF
      existingSecret: ${existing_secret_name}
EOF
  else
    cat <<EOF
      accessKeyId: ${s3_access_key}
      secretAccessKey: ${s3_secret_key}
EOF
  fi

  # Add local persistence if needed (for SQLite database)
  if [ "$has_local_persistence" = "true" ]; then
    local persistence_size=$(echo "$perm_json" | jq -r '.storage.local.persistence.size')
    cat <<EOF
    # Even though storage is S3, we need local persistence for SQLite database
    local:
      persistence:
        enabled: true
        size: ${persistence_size}
EOF
  fi
}

# Render database configuration section
render_database_config() {
  local perm_json="$1"
  local cluster_json="$2"

  local db_type=$(echo "$perm_json" | jq -r '.database.type')

  case "$db_type" in
    sqlite)
      render_sqlite_config "$perm_json"
      ;;
    postgresql)
      render_postgresql_config "$perm_json" "$cluster_json"
      ;;
    mysql)
      render_mysql_config "$perm_json" "$cluster_json"
      ;;
  esac
}

# Render SQLite database configuration
render_sqlite_config() {
  local perm_json="$1"

  local sqlite_path=$(echo "$perm_json" | jq -r '.database.sqlite.path')

  cat <<EOF
  database:
    type: sqlite
    sqlite:
      path: ${sqlite_path}
EOF
}

# Render PostgreSQL database configuration
render_postgresql_config() {
  local perm_json="$1"
  local cluster_json="$2"

  local use_existing_secret=$(echo "$perm_json" | jq -r '.database.useExistingSecret // false')
  local existing_secret_name=$(echo "$perm_json" | jq -r '.database.existingSecretName // empty')

  # Extract PostgreSQL credentials from cluster
  local pg_host=$(echo "$cluster_json" | jq -r '.postgresql.host')
  local pg_port=$(echo "$cluster_json" | jq -r '.postgresql.port')
  local pg_db=$(echo "$cluster_json" | jq -r '.postgresql.database')
  local pg_user=$(echo "$cluster_json" | jq -r '.postgresql.username')
  local pg_pass=$(echo "$cluster_json" | jq -r '.postgresql.password')

  cat <<EOF
  database:
    type: postgresql
    postgresql:
      host: ${pg_host}
      port: ${pg_port}
      database: ${pg_db}
      username: ${pg_user}
EOF

  if [ "$use_existing_secret" = "true" ] && [ -n "$existing_secret_name" ]; then
    cat <<EOF
      existingSecret: ${existing_secret_name}
EOF
  else
    cat <<EOF
      password: "${pg_pass}"
EOF
  fi

  cat <<EOF
      sslMode: disable
EOF
}

# Render MySQL/MariaDB database configuration
render_mysql_config() {
  local perm_json="$1"
  local cluster_json="$2"

  local use_existing_secret=$(echo "$perm_json" | jq -r '.database.useExistingSecret // false')
  local existing_secret_name=$(echo "$perm_json" | jq -r '.database.existingSecretName // empty')

  # Extract MariaDB credentials from cluster
  local maria_host=$(echo "$cluster_json" | jq -r '.mariadb.host')
  local maria_port=$(echo "$cluster_json" | jq -r '.mariadb.port')
  local maria_db=$(echo "$cluster_json" | jq -r '.mariadb.database')
  local maria_user=$(echo "$cluster_json" | jq -r '.mariadb.username')
  local maria_pass=$(echo "$cluster_json" | jq -r '.mariadb.password')

  cat <<EOF
  database:
    type: mysql
    mysql:
      host: ${maria_host}
      port: ${maria_port}
      database: ${maria_db}
      username: ${maria_user}
EOF

  if [ "$use_existing_secret" = "true" ] && [ -n "$existing_secret_name" ]; then
    cat <<EOF
      existingSecret: ${existing_secret_name}
EOF
  else
    cat <<EOF
      password: "${maria_pass}"
EOF
  fi
}

# Render lock configuration (for PostgreSQL advisory locks)
render_lock_config() {
  local perm_json="$1"

  local lock_backend=$(echo "$perm_json" | jq -r '.lock.backend // empty')

  if [ -n "$lock_backend" ] && [ "$lock_backend" != "null" ]; then
    cat <<EOF
  lock:
    backend: ${lock_backend}
EOF
  fi
}

# Render Redis configuration
render_redis_config() {
  local perm_json="$1"
  local cluster_json="$2"

  local redis_enabled=$(echo "$perm_json" | jq -r '.redis.enabled')

  if [ "$redis_enabled" = "true" ]; then
    local redis_host=$(echo "$cluster_json" | jq -r '.redis.host')
    local redis_port=$(echo "$cluster_json" | jq -r '.redis.port')

    cat <<EOF
  redis:
    enabled: true
    addresses:
      - ${redis_host}:${redis_port}
    db: 0
    useTLS: false
EOF
  else
    cat <<EOF
  redis:
    enabled: false
EOF
  fi
}

# Render CDC (Content-Defined Chunking) configuration
render_cdc_config() {
  local perm_json="$1"

  local has_cdc=$(echo "$perm_json" | jq -r '.features | contains(["cdc"])')

  if [ "$has_cdc" = "true" ]; then
    cat <<EOF
  cdc:
    enabled: true
EOF
  fi
}

# Render Pod Disruption Budget
render_pod_disruption_budget() {
  local perm_json="$1"

  local has_pdb=$(echo "$perm_json" | jq -r '.features | contains(["pod-disruption-budget"])')

  if [ "$has_pdb" = "true" ]; then
    cat <<EOF

podDisruptionBudget:
  enabled: true
  minAvailable: 1
EOF
  fi
}

# Render Pod Anti-Affinity
render_affinity() {
  local perm_json="$1"

  local has_affinity=$(echo "$perm_json" | jq -r '.features | contains(["anti-affinity"])')

  if [ "$has_affinity" = "true" ]; then
    cat <<EOF

affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          labelSelector:
            matchExpressions:
              - key: app.kubernetes.io/name
                operator: In
                values:
                  - ncps
          topologyKey: kubernetes.io/hostname
EOF
  fi
}

# Parse cluster info from cluster.sh info output
parse_cluster_info() {
  local cluster_output="$1"

  # Parse the cluster info into a JSON structure
  # The cluster.sh info command outputs key=value pairs

  # Extract values using grep and awk
  local s3_bucket=$(echo "$cluster_output" | grep "S3_BUCKET=" | cut -d'=' -f2)
  local s3_endpoint=$(echo "$cluster_output" | grep "S3_ENDPOINT=" | cut -d'=' -f2)
  local s3_access_key=$(echo "$cluster_output" | grep "S3_ACCESS_KEY=" | cut -d'=' -f2)
  local s3_secret_key=$(echo "$cluster_output" | grep "S3_SECRET_KEY=" | cut -d'=' -f2)

  local pg_host=$(echo "$cluster_output" | grep "POSTGRESQL_HOST=" | cut -d'=' -f2)
  local pg_port=$(echo "$cluster_output" | grep "POSTGRESQL_PORT=" | cut -d'=' -f2)
  local pg_db=$(echo "$cluster_output" | grep "POSTGRESQL_DB=" | cut -d'=' -f2)
  local pg_user=$(echo "$cluster_output" | grep "POSTGRESQL_USER=" | cut -d'=' -f2)
  local pg_pass=$(echo "$cluster_output" | grep "POSTGRESQL_PASS=" | cut -d'=' -f2)

  local maria_host=$(echo "$cluster_output" | grep "MARIADB_HOST=" | cut -d'=' -f2)
  local maria_port=$(echo "$cluster_output" | grep "MARIADB_PORT=" | cut -d'=' -f2)
  local maria_db=$(echo "$cluster_output" | grep "MARIADB_DB=" | cut -d'=' -f2)
  local maria_user=$(echo "$cluster_output" | grep "MARIADB_USER=" | cut -d'=' -f2)
  local maria_pass=$(echo "$cluster_output" | grep "MARIADB_PASS=" | cut -d'=' -f2)

  local redis_host=$(echo "$cluster_output" | grep "REDIS_HOST=" | cut -d'=' -f2)
  local redis_port=$(echo "$cluster_output" | grep "REDIS_PORT=" | cut -d'=' -f2)

  # Output as JSON
  jq -n \
    --arg s3_bucket "$s3_bucket" \
    --arg s3_endpoint "$s3_endpoint" \
    --arg s3_access_key "$s3_access_key" \
    --arg s3_secret_key "$s3_secret_key" \
    --arg pg_host "$pg_host" \
    --arg pg_port "$pg_port" \
    --arg pg_db "$pg_db" \
    --arg pg_user "$pg_user" \
    --arg pg_pass "$pg_pass" \
    --arg maria_host "$maria_host" \
    --arg maria_port "$maria_port" \
    --arg maria_db "$maria_db" \
    --arg maria_user "$maria_user" \
    --arg maria_pass "$maria_pass" \
    --arg redis_host "$redis_host" \
    --arg redis_port "$redis_port" \
    '{
      s3: {
        bucket: $s3_bucket,
        endpoint: $s3_endpoint,
        access_key: $s3_access_key,
        secret_key: $s3_secret_key
      },
      postgresql: {
        host: $pg_host,
        port: $pg_port,
        database: $pg_db,
        username: $pg_user,
        password: $pg_pass
      },
      mariadb: {
        host: $maria_host,
        port: $maria_port,
        database: $maria_db,
        username: $maria_user,
        password: $maria_pass
      },
      redis: {
        host: $redis_host,
        port: $redis_port
      }
    }'
}
