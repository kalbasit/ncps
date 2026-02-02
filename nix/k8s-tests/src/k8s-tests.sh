#!/usr/bin/env bash
# NCPS Kubernetes Integration Testing CLI
#
# Provides unified interface for Kind cluster integration testing
# - cluster management (create/destroy/info)
# - test values generation
# - deployment management (install/test/cleanup)

set -euo pipefail

# Paths to config and library (substituted by Nix at build time)
CONFIG_FILE="${CONFIG_FILE:-}"
CLUSTER_SCRIPT="${CLUSTER_SCRIPT:-}"


# Constants
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || echo ".")"
TEST_VALUES_DIR="$REPO_ROOT/charts/ncps/test-values"

# Show usage information
show_usage() {
  cat <<'EOF'
NCPS Kubernetes Integration Testing

USAGE:
    k8s-tests <command> [options]

COMMANDS:
    cluster create          Create Kind cluster with dependencies
    cluster destroy         Destroy Kind cluster
    cluster info            Show cluster connection information

    generate [OPTIONS]      Generate test values files
        --push              Build and push image to local registry
        --last              Reuse the most recently built/used image tag
        <tag> [reg] [repo]  Use external image (tag required, registry and repo optional)

    install [name]          Install test deployments (all or specific)
    test [name] [-v]        Run integration tests (all or specific)
    cleanup [name]          Remove test deployments (all or specific)

    all                     Run complete workflow (create + generate + install + test)

WORKFLOW (5 steps):
    k8s-tests cluster create     # 1. Start Kind cluster
    k8s-tests generate --push    # 2. Build image & generate values
    k8s-tests install            # 3. Deploy all 13 permutations
    k8s-tests test               # 4. Run comprehensive tests
    k8s-tests cleanup            # 5. Remove deployments

EXAMPLES:
    # Complete workflow
    k8s-tests all

    # Individual steps
    k8s-tests cluster create
    k8s-tests generate --push
    k8s-tests install
    k8s-tests test
    k8s-tests cleanup

    # Single deployment
    k8s-tests install single-local-sqlite
    k8s-tests test single-local-sqlite -v
    k8s-tests cleanup single-local-sqlite

    # Use external image
    k8s-tests generate sha-cf09394
    k8s-tests generate 0.5.1 docker.io kalbasit/ncps

See nix/k8s-tests/README.md for details.
EOF
}

# Parse cluster info from cluster.sh info output
parse_cluster_info() {
  local cluster_output="$1"

  # Parse the cluster info into a JSON structure
  # The cluster.sh info command outputs key=value pairs

  # Extract values by evaluating the output
  # The input is expected to be in KEY=VALUE format
  eval "$cluster_output"

  # Output as JSON
  jq -n \
    --arg s3_bucket "${S3_BUCKET:-}" \
    --arg s3_endpoint "${S3_ENDPOINT:-}" \
    --arg s3_access_key "${S3_ACCESS_KEY:-}" \
    --arg s3_secret_key "${S3_SECRET_KEY:-}" \
    --arg pg_host "${POSTGRESQL_HOST:-}" \
    --arg pg_port "${POSTGRESQL_PORT:-}" \
    --arg pg_db "${POSTGRESQL_DB:-}" \
    --arg pg_user "${POSTGRESQL_USER:-}" \
    --arg pg_pass "${POSTGRESQL_PASS:-}" \
    --arg maria_host "${MARIADB_HOST:-}" \
    --arg maria_port "${MARIADB_PORT:-}" \
    --arg maria_db "${MARIADB_DB:-}" \
    --arg maria_user "${MARIADB_USER:-}" \
    --arg maria_pass "${MARIADB_PASS:-}" \
    --arg redis_host "${REDIS_HOST:-}" \
    --arg redis_port "${REDIS_PORT:-}" \
    '{
      s3: {
        bucket: $s3_bucket,
        endpoint: $s3_endpoint,
        access_key: $s3_access_key,
        secret_key: $s3_secret_key
      },
      postgresql: {
        host: $pg_host,
        port: ($pg_port | tonumber),
        database: $pg_db,
        username: $pg_user,
        password: $pg_pass
      },
      mariadb: {
        host: $maria_host,
        port: ($maria_port | tonumber),
        database: $maria_db,
        username: $maria_user,
        password: $maria_pass
      },
      redis: {
        host: $redis_host,
        port: ($redis_port | tonumber)
      }
    }'
}

# Cluster management commands
cmd_cluster() {
  local subcommand="${1:-}"

  if [ -z "$subcommand" ]; then
    echo "❌ Error: cluster subcommand required (create/destroy/info)" >&2
    exit 1
  fi

  if [ -z "$CLUSTER_SCRIPT" ] || [ ! -f "$CLUSTER_SCRIPT" ]; then
    echo "❌ Error: cluster script not found: $CLUSTER_SCRIPT" >&2
    exit 1
  fi

  # Delegate to cluster.sh
  "$CLUSTER_SCRIPT" "$@"
}

# Generate test values files
cmd_generate() {
  local build_and_push=false
  local use_last=false
  local image_tag=""
  local image_registry="127.0.0.1:30000"
  local image_repository="ncps"

  # Parse arguments
  while [ $# -gt 0 ]; do
    case "$1" in
      --push)
        build_and_push=true
        shift
        ;;
      --last)
        use_last=true
        shift
        ;;
      *)
        if [ -z "$image_tag" ]; then
          image_tag="$1"
        elif [ -z "$image_registry" ] || [ "$image_registry" = "127.0.0.1:30000" ]; then
          image_registry="$1"
        elif [ -z "$image_repository" ] || [ "$image_repository" = "ncps" ]; then
          image_repository="$1"
        else
          echo "❌ Error: too many arguments" >&2
          exit 1
        fi
        shift
        ;;
    esac
  done

  # Handle --last flag
  if [ "$use_last" = true ]; then
    if [ -f "$TEST_VALUES_DIR/.last_tag" ]; then
      image_tag=$(cat "$TEST_VALUES_DIR/.last_tag")
      echo "⏮️  Reusing last image tag: ${image_tag}"
    else
      echo "❌ Error: no last tag found (run with --push or provide tag first)" >&2
      exit 1
    fi
  fi

  # Build and push image if requested
  if [ "$build_and_push" = true ]; then
    echo "🔨 Building Docker image with Nix..."
    local build_output
    if ! build_output=$(nix build "$REPO_ROOT#docker" --print-out-paths --no-link); then
      echo "❌ Error: failed to build Docker image" >&2
      exit 1
    fi

    echo "📦 Loading image into Docker..."
    # Capture output to find the loaded image name/tag
    local load_output
    if ! load_output=$(docker load < "$build_output"); then
      echo "❌ Error: failed to load Docker image" >&2
      exit 1
    fi
    echo "$load_output"

    # Extract image name and tag from "Loaded image: ..." line
    # The image will be tagged as 127.0.0.1:30000/ncps:<sha>-<platform> or similar
    local nix_image
    nix_image=$(echo "$load_output" | grep "Loaded image:" | sed 's/Loaded image: //')

    if [ -z "$nix_image" ]; then
      echo "❌ Error: could not determine loaded image name" >&2
      exit 1
    fi

    echo "Nix built image: $nix_image"

    # Extract tag from nix image
    image_tag=$(echo "$nix_image" | cut -d':' -f2)
    image_registry="127.0.0.1:30000"
    image_repository="ncps"

    echo "📤 Pushing image to local registry..."
    local full_image="${image_registry}/${image_repository}:${image_tag}"

    # Use docker-daemon source since we just loaded it there
    if ! skopeo --insecure-policy copy --dest-tls-verify=false "docker-daemon:${nix_image}" "docker://${full_image}"; then
      echo "❌ Error: failed to push image to registry" >&2
      exit 1
    fi

    echo "✅ Image pushed: ${full_image}"
  fi

  # Validate image tag
  if [ -z "$image_tag" ]; then
    echo "❌ Error: image tag required (use --push, --last, or provide tag)" >&2
    exit 1
  fi

  # Save tag for --last
  mkdir -p "$TEST_VALUES_DIR"
  echo "$image_tag" > "$TEST_VALUES_DIR/.last_tag"

  # Get cluster credentials
  echo "🔍 Querying cluster credentials..."
  local cluster_output
  if ! cluster_output=$("$CLUSTER_SCRIPT" env); then
    echo "❌ Error: failed to get cluster info (is cluster running?)" >&2
    exit 1
  fi

  local cluster_json
  cluster_json=$(parse_cluster_info "$cluster_output")

  # Load permutations from Nix config
  echo "📋 Loading test permutations from config..."
  if [ -z "$CONFIG_FILE" ] || [ ! -f "$CONFIG_FILE" ]; then
    echo "❌ Error: config file not found: $CONFIG_FILE" >&2
    exit 1
  fi

  local permutations_json
  if ! permutations_json=$(nix eval --json --file "$CONFIG_FILE" permutations); then
    echo "❌ Error: failed to evaluate Nix configuration" >&2
    exit 1
  fi

  # Create output directory
  mkdir -p "$TEST_VALUES_DIR"

  # Generate values files
  # Generate values files using Nix
  echo "📝 Generating values files with Nix..."
  local values_json_file
  values_json_file=$(mktemp)

  # Construct arguments JSON for Nix function
  local args_json
  args_json=$(jq -n \
    --arg reg "$image_registry" \
    --arg repo "$image_repository" \
    --arg tag "$image_tag" \
    --arg cluster "$cluster_json" \
    '{image_registry: $reg, image_repository: $repo, image_tag: $tag, cluster: $cluster}')

  # Passing arguments to Nix via apply wrapper
  # We pass all arguments as a single parsed JSON object
  if ! nix eval --json --file "$CONFIG_FILE" generateValues \
      --apply "f: f (builtins.fromJSON ''$args_json'')" \
      > "$values_json_file"; then
      echo "❌ Error: failed to generate values with Nix" >&2
      rm -f "$values_json_file"
      exit 1
  fi

  local count=0
  # Iterate over the generated values (map of filename -> content)
  while IFS=$'\t' read -r name content; do
    echo "  Generating ${name}.yaml..."

    # Write header
    echo "# Auto-generated from config.nix" > "$TEST_VALUES_DIR/${name}.yaml"

    # Convert JSON content to YAML and append
    echo "$content" | yq -P >> "$TEST_VALUES_DIR/${name}.yaml"

    count=$((count + 1))
  done < <(jq -r 'to_entries | .[] | .key + "\t" + (.value | tojson)' "$values_json_file")

  rm -f "$values_json_file"

  echo "✅ Generated $count values files"

  # Generate helper scripts
  generate_quick_install
  generate_test_script
  generate_cleanup_script
  generate_test_config "$cluster_json" "$permutations_json"
  generate_existing_secret_scripts "$cluster_json" "$permutations_json"

  echo "✅ All test files generated in: $TEST_VALUES_DIR"
}

# Install deployments
cmd_install() {
  local deployment_name="${1:-}"

  if [ ! -f "$TEST_VALUES_DIR/QUICK-INSTALL.sh" ]; then
    echo "❌ Error: test values not generated (run 'k8s-tests generate' first)" >&2
    exit 1
  fi

  if [ -z "$deployment_name" ]; then
    # Install all
    echo "📦 Installing all test deployments..."
    bash "$TEST_VALUES_DIR/QUICK-INSTALL.sh"
  else
    # Install specific deployment
    echo "📦 Installing ${deployment_name}..."
    if [ ! -f "$TEST_VALUES_DIR/${deployment_name}.yaml" ]; then
      echo "❌ Error: values file not found: ${deployment_name}.yaml" >&2
      exit 1
    fi

    helm upgrade --install "ncps-${deployment_name}" "$REPO_ROOT/charts/ncps" \
      -f "$TEST_VALUES_DIR/${deployment_name}.yaml" \
      --create-namespace \
      --namespace "ncps-${deployment_name}"

    echo "✅ Deployment installed"
  fi
}

# Run tests
cmd_test() {
  if [ ! -f "$TEST_VALUES_DIR/TEST.sh" ]; then
    echo "❌ Error: test script not generated (run 'k8s-tests generate' first)" >&2
    exit 1
  fi

  echo "🧪 Running tests..."
  bash "$TEST_VALUES_DIR/TEST.sh" "$@"
}

# Cleanup deployments
cmd_cleanup() {
  local deployment_name="${1:-}"

  if [ ! -f "$TEST_VALUES_DIR/CLEANUP.sh" ]; then
    echo "❌ Error: cleanup script not generated (run 'k8s-tests generate' first)" >&2
    exit 1
  fi

  if [ -z "$deployment_name" ]; then
    # Cleanup all
    echo "🧹 Cleaning up all test deployments..."
    bash "$TEST_VALUES_DIR/CLEANUP.sh"
  else
    # Cleanup specific deployment
    echo "🧹 Cleaning up ${deployment_name}..."
    helm uninstall "ncps-${deployment_name}" -n "ncps-${deployment_name}" || true
    kubectl delete namespace "ncps-${deployment_name}" || true
    echo "✅ Deployment removed"
  fi
}

# Run complete workflow
cmd_all() {
  echo "========================================="
  echo "NCPS K8s Tests - Complete Workflow"
  echo "========================================="
  echo ""

  echo "Step 1/5: Creating Kind cluster..."
  cmd_cluster create

  echo ""
  echo "Step 2/5: Generating test values..."
  cmd_generate --push

  echo ""
  echo "Step 3/5: Installing deployments..."
  cmd_install

  echo ""
  echo "Step 4/5: Running tests..."
  cmd_test

  echo ""
  echo "========================================="
  echo "✅ Complete workflow finished successfully"
  echo "========================================="
  echo ""
  echo "To cleanup: k8s-tests cleanup"
}

# Generate QUICK-INSTALL.sh helper script
generate_quick_install() {
  cat > "$TEST_VALUES_DIR/QUICK-INSTALL.sh" <<'INSTALL_EOF'
#!/usr/bin/env bash
# Quick install script for all NCPS test scenarios
# Auto-generated by k8s-tests

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "========================================="
echo "Installing NCPS Test Deployments"
echo "========================================="
echo "Chart directory: $CHART_DIR"
echo ""

cd "$CHART_DIR"

# Install all 13 test deployments
for values_file in test-values/single-*.yaml test-values/ha-*.yaml; do
  name=$(basename "$values_file" .yaml)
  echo "📦 Installing ncps-${name}..."

  # Run setup script if it exists (for existing-secret tests)
  if [ -f "test-values/install-${name}.sh" ]; then
    echo "  Running setup script for ${name}..."
    bash "test-values/install-${name}.sh"
  fi

  helm upgrade --install "ncps-${name}" . \
    -f "test-values/${name}.yaml" \
    --create-namespace \
    --namespace "ncps-${name}"
done

echo ""
echo "========================================="
echo "✅ All deployments installed successfully"
echo "========================================="
INSTALL_EOF

  chmod +x "$TEST_VALUES_DIR/QUICK-INSTALL.sh"
  echo "✅ Generated QUICK-INSTALL.sh"
}

# Generate TEST.sh helper script
generate_test_script() {
  cat > "$TEST_VALUES_DIR/TEST.sh" <<'TEST_EOF'
#!/usr/bin/env bash
# Test script for NCPS deployments
# Auto-generated by k8s-tests

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Delegate to test-deployments.py
python3 "$(git rev-parse --show-toplevel)/dev-scripts/test-deployments.py" \
  "$SCRIPT_DIR/test-config.yaml" "$@"
TEST_EOF

  chmod +x "$TEST_VALUES_DIR/TEST.sh"
  echo "✅ Generated TEST.sh"
}

# Generate CLEANUP.sh helper script
generate_cleanup_script() {
  cat > "$TEST_VALUES_DIR/CLEANUP.sh" <<'CLEANUP_EOF'
#!/usr/bin/env bash
# Cleanup script for NCPS test deployments
# Auto-generated by k8s-tests

set -e

echo "========================================="
echo "Cleaning up NCPS Test Deployments"
echo "========================================="
echo ""

# Uninstall all test deployments
for ns in $(kubectl get namespaces -o name | grep "namespace/ncps-"); do
  name=$(echo "$ns" | sed 's|namespace/||')
  echo "🧹 Removing ${name}..."
  helm uninstall "${name}" -n "${name}" || true
  kubectl delete namespace "${name}" || true
done

echo ""
echo "========================================="
echo "✅ All deployments removed successfully"
echo "========================================="
CLEANUP_EOF

  chmod +x "$TEST_VALUES_DIR/CLEANUP.sh"
  echo "✅ Generated CLEANUP.sh"
}

# Generate test-config.yaml
generate_test_config() {
  local cluster_json="$1"
  local permutations_json="$2"

  # Load test data from config
  local narinfo_hashes
  narinfo_hashes=$(nix eval --json --file "$CONFIG_FILE" testData.narinfo_hashes | jq -r '.[]')

  cat > "$TEST_VALUES_DIR/test-config.yaml" <<EOF
# NCPS Test Configuration
# Auto-generated by k8s-tests

cluster:
  s3:
    bucket: $(echo "$cluster_json" | jq -r '.s3.bucket')
    endpoint: $(echo "$cluster_json" | jq -r '.s3.endpoint')
    access_key: $(echo "$cluster_json" | jq -r '.s3.access_key')
    secret_key: $(echo "$cluster_json" | jq -r '.s3.secret_key')

  postgresql:
    host: $(echo "$cluster_json" | jq -r '.postgresql.host')
    port: $(echo "$cluster_json" | jq -r '.postgresql.port')
    database: $(echo "$cluster_json" | jq -r '.postgresql.database')
    username: $(echo "$cluster_json" | jq -r '.postgresql.username')
    password: $(echo "$cluster_json" | jq -r '.postgresql.password')

  mariadb:
    host: $(echo "$cluster_json" | jq -r '.mariadb.host')
    port: $(echo "$cluster_json" | jq -r '.mariadb.port')
    database: $(echo "$cluster_json" | jq -r '.mariadb.database')
    username: $(echo "$cluster_json" | jq -r '.mariadb.username')
    password: $(echo "$cluster_json" | jq -r '.mariadb.password')

  redis:
    host: $(echo "$cluster_json" | jq -r '.redis.host')
    port: $(echo "$cluster_json" | jq -r '.redis.port')

test_data:
  narinfo_hashes:
EOF

  while IFS= read -r hash; do
    echo "    - \"$hash\"" >> "$TEST_VALUES_DIR/test-config.yaml"
  done <<< "$narinfo_hashes"

  {
    echo ""
    echo "deployments:"
    echo "$permutations_json" | jq -r '
      .[] |
      "- name: " + .name,
      "  namespace: ncps-" + .name,
      "  service_name: ncps-" + .name,
      "  replicas: " + (.replicas|tostring),
      "  mode: " + (if .replicas > 1 then "ha" else "single" end),
      "  cdc: " + (if (.features | index("cdc")) then "true" else "false" end),
      "  database:",
      "    type: " + .database.type,
      (if .database.type == "sqlite" then "    path: " + .database.sqlite.path else empty end),
      "  storage:",
      "    type: " + .storage.type,
      (if .storage.type == "local" then "    path: " + .storage.local.path else empty end)
    '
  } >> "$TEST_VALUES_DIR/test-config.yaml"

  echo "✅ Generated test-config.yaml"
}

# Generate existing-secret setup scripts
# Generate existing-secret setup scripts
generate_existing_secret_scripts() {
  local cluster_json="$1"
  local permutations_json="$2"

  echo "  Generating existing-secret setup scripts..."

  # Extract common credentials
  local s3_access_key s3_secret_key
  s3_access_key=$(echo "$cluster_json" | jq -r '.s3.access_key')
  s3_secret_key=$(echo "$cluster_json" | jq -r '.s3.secret_key')

  # Loop through permutations to find those with setupScript
  while IFS= read -r perm_json; do
    local name setup_script
    name=$(echo "$perm_json" | jq -r '.name')
    setup_script=$(echo "$perm_json" | jq -r '.setupScript // empty')

    if [ -n "$setup_script" ] && [ "$setup_script" != "null" ]; then
      local db_type db_url
      db_type=$(echo "$perm_json" | jq -r '.database.type')

      # Construct Database URL
      if [ "$db_type" = "postgresql" ]; then
        local pg_host pg_port pg_db pg_user pg_pass
        pg_host=$(echo "$cluster_json" | jq -r '.postgresql.host')
        pg_port=$(echo "$cluster_json" | jq -r '.postgresql.port')
        pg_db=$(echo "$cluster_json" | jq -r '.postgresql.database')
        pg_user=$(echo "$cluster_json" | jq -r '.postgresql.username')
        pg_pass=$(echo "$cluster_json" | jq -r '.postgresql.password')

        # Encode user/pass
        local pg_user_enc pg_pass_enc
        pg_user_enc=$(jq -nr --arg v "$pg_user" '$v|@uri')
        pg_pass_enc=$(jq -nr --arg v "$pg_pass" '$v|@uri')

        db_url="postgresql://${pg_user_enc}:${pg_pass_enc}@${pg_host}:${pg_port}/${pg_db}?sslmode=disable"

      elif [ "$db_type" = "mysql" ]; then
        local maria_host maria_port maria_db maria_user maria_pass
        maria_host=$(echo "$cluster_json" | jq -r '.mariadb.host')
        maria_port=$(echo "$cluster_json" | jq -r '.mariadb.port')
        maria_db=$(echo "$cluster_json" | jq -r '.mariadb.database')
        maria_user=$(echo "$cluster_json" | jq -r '.mariadb.username')
        maria_pass=$(echo "$cluster_json" | jq -r '.mariadb.password')

        # Encode user/pass
        local maria_user_enc maria_pass_enc
        maria_user_enc=$(jq -nr --arg v "$maria_user" '$v|@uri')
        maria_pass_enc=$(jq -nr --arg v "$maria_pass" '$v|@uri')

        db_url="mysql://${maria_user_enc}:${maria_pass_enc}@${maria_host}:${maria_port}/${maria_db}"
      fi

      echo "    Generating $setup_script..."

      # Generate the setup script
      cat > "$TEST_VALUES_DIR/$setup_script" <<EOF
#!/usr/bin/env bash
# Setup script for $name
# Auto-generated by k8s-tests

set -e

echo "Creating namespace ncps-${name}..."
kubectl create namespace "ncps-${name}" --dry-run=client -o yaml | kubectl apply -f -

echo "Creating secret ncps-external-secrets..."
kubectl create secret generic ncps-external-secrets \\
  --namespace "ncps-${name}" \\
  --from-literal=access-key-id="${s3_access_key}" \\
  --from-literal=secret-access-key="${s3_secret_key}" \\
  --from-literal=database-url="${db_url}" \\
  --dry-run=client -o yaml | kubectl apply -f -

echo "✅ Secret created for $name"
EOF
      chmod +x "$TEST_VALUES_DIR/$setup_script"
    fi
  done < <(echo "$permutations_json" | jq -c '.[]')
}

# Main command dispatcher
main() {
  local command="${1:-}"

  if [ -z "$command" ]; then
    show_usage
    exit 1
  fi

  shift || true

  case "$command" in
    cluster)
      cmd_cluster "$@"
      ;;
    generate)
      cmd_generate "$@"
      ;;
    install)
      cmd_install "$@"
      ;;
    test)
      cmd_test "$@"
      ;;
    cleanup)
      cmd_cleanup "$@"
      ;;
    all)
      cmd_all
      ;;
    help|--help|-h)
      show_usage
      ;;
    *)
      echo "❌ Unknown command: $command" >&2
      echo "" >&2
      show_usage
      exit 1
      ;;
  esac
}

main "$@"
