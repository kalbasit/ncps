#!/usr/bin/env bash
# Generate test values files for Helm chart based on Kind cluster configuration

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CHART_DIR="$REPO_ROOT/charts/ncps"
TEST_VALUES_DIR="$CHART_DIR/test-values"

# Check if image tag is provided
if [ -z "$1" ]; then
  echo "Usage: $0 <image-tag> <image-registry> <image-repository>"
  echo ""
  echo "Example: $0 sha-cf09394"
  echo "         $0 0.5.1"
  echo "         $0 sha3eb1fe1-aarch64-darwin myrepo.example.com ncps"
  exit 1
fi

IMAGE_TAG="$1"
IMAGE_REGISTRY="${2:-docker.io}"
IMAGE_REPOSITORY="${3:-kalbasit/ncps}"

echo "========================================="
echo "Generating NCPS Test Values Files"
echo "========================================="
echo "Image: $IMAGE_REGISTRY/$IMAGE_REPOSITORY:$IMAGE_TAG"
echo ""

# Get cluster info
echo "📋 Retrieving cluster information..."
CLUSTER_INFO=$("$SCRIPT_DIR/k8s-cluster.sh" info 2>/dev/null)

if [ -z "$CLUSTER_INFO" ]; then
  echo "❌ Error: Could not retrieve cluster information"
  echo "   Make sure the Kind cluster is running: ./dev-scripts/k8s-cluster.sh start"
  exit 1
fi

# Parse cluster info
echo "🔍 Parsing cluster credentials..."

# MinIO / S3
S3_ENDPOINT=$(echo "$CLUSTER_INFO" | grep "Endpoint:" | head -1 | awk '{print $2}')
S3_BUCKET=$(echo "$CLUSTER_INFO" | grep "Bucket:" | head -1 | awk '{print $2}')
S3_ACCESS_KEY=$(echo "$CLUSTER_INFO" | grep "Access:" | head -1 | awk '{print $2}')
S3_SECRET_KEY=$(echo "$CLUSTER_INFO" | grep "Secret:" | head -1 | awk '{print $2}')

# PostgreSQL
PG_HOST=$(echo "$CLUSTER_INFO" | grep -A 5 "PostgreSQL" | grep "Host:" | awk '{print $2}')
PG_PORT=$(echo "$CLUSTER_INFO" | grep -A 5 "PostgreSQL" | grep "Port:" | awk '{print $2}')
PG_USER=$(echo "$CLUSTER_INFO" | grep -A 5 "PostgreSQL" | grep "User:" | awk '{print $2}')
PG_PASS=$(echo "$CLUSTER_INFO" | grep -A 5 "PostgreSQL" | grep "Pass:" | awk '{print $2}')
PG_DB=$(echo "$CLUSTER_INFO" | grep -A 5 "PostgreSQL" | grep "DB:" | awk '{print $2}')

# MariaDB
MARIA_HOST=$(echo "$CLUSTER_INFO" | grep -A 5 "MariaDB" | grep "Host:" | awk '{print $2}')
MARIA_PORT=$(echo "$CLUSTER_INFO" | grep -A 5 "MariaDB" | grep "Port:" | awk '{print $2}')
MARIA_USER=$(echo "$CLUSTER_INFO" | grep -A 5 "MariaDB" | grep "User:" | awk '{print $2}')
MARIA_PASS=$(echo "$CLUSTER_INFO" | grep -A 5 "MariaDB" | grep "Pass:" | awk '{print $2}')
MARIA_DB=$(echo "$CLUSTER_INFO" | grep -A 5 "MariaDB" | grep "DB:" | awk '{print $2}')

# URL-encode credentials for database URLs (handles special characters like >, |, @, etc.)
PG_USER_ENCODED=$(printf '%s' "$PG_USER" | jq -sRr '@uri')
PG_PASS_ENCODED=$(printf '%s' "$PG_PASS" | jq -sRr '@uri')
MARIA_USER_ENCODED=$(printf '%s' "$MARIA_USER" | jq -sRr '@uri')
MARIA_PASS_ENCODED=$(printf '%s' "$MARIA_PASS" | jq -sRr '@uri')

# Redis
REDIS_HOST=$(echo "$CLUSTER_INFO" | grep -A 3 "Redis" | grep "Host:" | awk '{print $2}')
REDIS_PORT=$(echo "$CLUSTER_INFO" | grep -A 3 "Redis" | grep "Port:" | awk '{print $2}')

# Validate required values
if [ -z "$S3_ENDPOINT" ] || [ -z "$PG_HOST" ] || [ -z "$MARIA_HOST" ] || [ -z "$REDIS_HOST" ]; then
  echo "❌ Error: Could not parse all required values from cluster info"
  echo ""
  echo "Debug info:"
  echo "  S3_ENDPOINT: $S3_ENDPOINT"
  echo "  PG_HOST: $PG_HOST"
  echo "  MARIA_HOST: $MARIA_HOST"
  echo "  REDIS_HOST: $REDIS_HOST"
  exit 1
fi

echo "✅ Cluster info parsed successfully"
echo "   S3: $S3_ENDPOINT"
echo "   PostgreSQL: $PG_HOST:$PG_PORT"
echo "   MariaDB: $MARIA_HOST:$MARIA_PORT"
echo "   Redis: $REDIS_HOST:$REDIS_PORT"
echo ""

# Create test-values directory
mkdir -p "$TEST_VALUES_DIR"

# Generate values files
echo "📝 Generating values files..."

# 1. Single instance - Local storage + SQLite
cat > "$TEST_VALUES_DIR/single-local-sqlite.yaml" <<EOF
# Single instance with local storage and SQLite
image:
  registry: $IMAGE_REGISTRY
  repository: $IMAGE_REPOSITORY
  tag: "$IMAGE_TAG"

replicaCount: 1

migration:
  enabled: true
  mode: initContainer

config:
  hostname: "ncps-single-local-sqlite.local"

  storage:
    type: local
    local:
      path: /storage
      persistence:
        enabled: true
        size: 5Gi

  database:
    type: sqlite
    sqlite:
      path: /storage/db/ncps.db

  redis:
    enabled: false
EOF

# 2. Single instance - Local storage + PostgreSQL
cat > "$TEST_VALUES_DIR/single-local-postgres.yaml" <<EOF
# Single instance with local storage and PostgreSQL
image:
  registry: $IMAGE_REGISTRY
  repository: $IMAGE_REPOSITORY
  tag: "$IMAGE_TAG"

replicaCount: 1

migration:
  enabled: true
  mode: initContainer

config:
  hostname: "ncps-single-local-postgres.local"

  storage:
    type: local
    local:
      path: /storage
      persistence:
        enabled: true
        size: 5Gi

  database:
    type: postgresql
    postgresql:
      host: $PG_HOST
      port: $PG_PORT
      database: $PG_DB
      username: $PG_USER
      password: $PG_PASS
      sslMode: disable

  redis:
    enabled: false
EOF

# 3. Single instance - Local storage + MariaDB
cat > "$TEST_VALUES_DIR/single-local-mariadb.yaml" <<EOF
# Single instance with local storage and MariaDB
image:
  registry: $IMAGE_REGISTRY
  repository: $IMAGE_REPOSITORY
  tag: "$IMAGE_TAG"

replicaCount: 1

migration:
  enabled: true
  mode: initContainer

config:
  hostname: "ncps-single-local-mariadb.local"

  storage:
    type: local
    local:
      path: /storage
      persistence:
        enabled: true
        size: 5Gi

  database:
    type: mysql
    mysql:
      host: $MARIA_HOST
      port: $MARIA_PORT
      database: $MARIA_DB
      username: $MARIA_USER
      password: "$MARIA_PASS"

  redis:
    enabled: false
EOF

# 4. Single instance - S3 storage + SQLite
cat > "$TEST_VALUES_DIR/single-s3-sqlite.yaml" <<EOF
# Single instance with S3 storage and SQLite
image:
  registry: $IMAGE_REGISTRY
  repository: $IMAGE_REPOSITORY
  tag: "$IMAGE_TAG"

replicaCount: 1
mode: deployment

migration:
  enabled: true
  mode: initContainer

config:
  hostname: "ncps-single-s3-sqlite.local"

  storage:
    type: s3
    s3:
      bucket: $S3_BUCKET
      endpoint: $S3_ENDPOINT
      region: us-east-1
      accessKeyId: $S3_ACCESS_KEY
      secretAccessKey: $S3_SECRET_KEY
    # Even though storage is S3, we need local persistence for SQLite database
    local:
      persistence:
        enabled: true
        size: 1Gi

  database:
    type: sqlite
    sqlite:
      path: /storage/db/ncps.db

  redis:
    enabled: false
EOF

# 5. Single instance - S3 storage + PostgreSQL
cat > "$TEST_VALUES_DIR/single-s3-postgres.yaml" <<EOF
# Single instance with S3 storage and PostgreSQL
image:
  registry: $IMAGE_REGISTRY
  repository: $IMAGE_REPOSITORY
  tag: "$IMAGE_TAG"

replicaCount: 1
mode: deployment

migration:
  enabled: true
  mode: initContainer

config:
  hostname: "ncps-single-s3-postgres.local"

  storage:
    type: s3
    s3:
      bucket: $S3_BUCKET
      endpoint: $S3_ENDPOINT
      region: us-east-1
      accessKeyId: $S3_ACCESS_KEY
      secretAccessKey: $S3_SECRET_KEY

  database:
    type: postgresql
    postgresql:
      host: $PG_HOST
      port: $PG_PORT
      database: $PG_DB
      username: $PG_USER
      password: $PG_PASS
      sslMode: disable

  redis:
    enabled: false
EOF

# 6. Single instance - S3 storage + MariaDB
cat > "$TEST_VALUES_DIR/single-s3-mariadb.yaml" <<EOF
# Single instance with S3 storage and MariaDB
image:
  registry: $IMAGE_REGISTRY
  repository: $IMAGE_REPOSITORY
  tag: "$IMAGE_TAG"

replicaCount: 1
mode: deployment

migration:
  enabled: true
  mode: initContainer

config:
  hostname: "ncps-single-s3-mariadb.local"

  storage:
    type: s3
    s3:
      bucket: $S3_BUCKET
      endpoint: $S3_ENDPOINT
      region: us-east-1
      accessKeyId: $S3_ACCESS_KEY
      secretAccessKey: $S3_SECRET_KEY

  database:
    type: mysql
    mysql:
      host: $MARIA_HOST
      port: $MARIA_PORT
      database: $MARIA_DB
      username: $MARIA_USER
      password: "$MARIA_PASS"

  redis:
    enabled: false
EOF

# 7. Single instance - S3 storage + PostgreSQL (with existing secret)
cat > "$TEST_VALUES_DIR/single-s3-postgres-existing-secret.yaml" <<EOF
# Single instance with S3 storage and PostgreSQL using existing secret
image:
  registry: $IMAGE_REGISTRY
  repository: $IMAGE_REPOSITORY
  tag: "$IMAGE_TAG"

replicaCount: 1
mode: deployment

migration:
  enabled: true
  mode: initContainer

config:
  hostname: "ncps-single-s3-postgres-existing-secret.local"

  storage:
    type: s3
    s3:
      bucket: $S3_BUCKET
      endpoint: $S3_ENDPOINT
      region: us-east-1
      existingSecret: ncps-external-secrets

  database:
    type: postgresql
    postgresql:
      host: $PG_HOST
      port: $PG_PORT
      database: $PG_DB
      username: $PG_USER
      existingSecret: ncps-external-secrets
      sslMode: disable

  redis:
    enabled: false
EOF

# 8. Single instance - S3 storage + MariaDB (with existing secret)
cat > "$TEST_VALUES_DIR/single-s3-mariadb-existing-secret.yaml" <<EOF
# Single instance with S3 storage and MariaDB using existing secret
image:
  registry: $IMAGE_REGISTRY
  repository: $IMAGE_REPOSITORY
  tag: "$IMAGE_TAG"

replicaCount: 1
mode: deployment

migration:
  enabled: true
  mode: initContainer

config:
  hostname: "ncps-single-s3-mariadb-existing-secret.local"

  storage:
    type: s3
    s3:
      bucket: $S3_BUCKET
      endpoint: $S3_ENDPOINT
      region: us-east-1
      existingSecret: ncps-external-secrets

  database:
    type: mysql
    mysql:
      host: $MARIA_HOST
      port: $MARIA_PORT
      database: $MARIA_DB
      username: $MARIA_USER
      existingSecret: ncps-external-secrets

  redis:
    enabled: false
EOF

# 9. HA - S3 storage + PostgreSQL + Redis
cat > "$TEST_VALUES_DIR/ha-s3-postgres.yaml" <<EOF
# High availability with S3 storage and PostgreSQL
image:
  registry: $IMAGE_REGISTRY
  repository: $IMAGE_REPOSITORY
  tag: "$IMAGE_TAG"

replicaCount: 2
mode: deployment

migration:
  enabled: true
  mode: job

config:
  hostname: "ncps-ha-s3-postgres.local"

  storage:
    type: s3
    s3:
      bucket: $S3_BUCKET
      endpoint: $S3_ENDPOINT
      region: us-east-1
      accessKeyId: $S3_ACCESS_KEY
      secretAccessKey: $S3_SECRET_KEY

  database:
    type: postgresql
    postgresql:
      host: $PG_HOST
      port: $PG_PORT
      database: $PG_DB
      username: $PG_USER
      password: $PG_PASS
      sslMode: disable

  redis:
    enabled: true
    addresses:
      - $REDIS_HOST:$REDIS_PORT
    db: 0
    useTLS: false

podDisruptionBudget:
  enabled: true
  minAvailable: 1

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

# 10. HA - S3 storage + MariaDB + Redis
cat > "$TEST_VALUES_DIR/ha-s3-mariadb.yaml" <<EOF
# High availability with S3 storage and MariaDB
image:
  registry: $IMAGE_REGISTRY
  repository: $IMAGE_REPOSITORY
  tag: "$IMAGE_TAG"

replicaCount: 2
mode: deployment

migration:
  enabled: true
  mode: job

config:
  hostname: "ncps-ha-s3-mariadb.local"

  storage:
    type: s3
    s3:
      bucket: $S3_BUCKET
      endpoint: $S3_ENDPOINT
      region: us-east-1
      accessKeyId: $S3_ACCESS_KEY
      secretAccessKey: $S3_SECRET_KEY

  database:
    type: mysql
    mysql:
      host: $MARIA_HOST
      port: $MARIA_PORT
      database: $MARIA_DB
      username: $MARIA_USER
      password: "$MARIA_PASS"

  redis:
    enabled: true
    addresses:
      - $REDIS_HOST:$REDIS_PORT
    db: 0
    useTLS: false

podDisruptionBudget:
  enabled: true
  minAvailable: 1

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

echo "✅ Generated 10 values files"

# Generate QUICK-INSTALL.sh
cat > "$TEST_VALUES_DIR/QUICK-INSTALL.sh" <<'INSTALL_EOF'
#!/usr/bin/env bash
# Quick install script for all NCPS test scenarios

set -e

echo "========================================="
echo "Installing NCPS Test Deployments"
echo "========================================="

# Single Instance Deployments
echo ""
echo "📦 Installing Single Instance Deployments..."
echo ""

echo "1️⃣  Installing ncps-single-local-sqlite..."
helm upgrade --install ncps-single-local-sqlite . \
  -f test-values/single-local-sqlite.yaml \
  --create-namespace \
  --namespace ncps-single-local-sqlite

echo "2️⃣  Installing ncps-single-local-postgres..."
helm upgrade --install ncps-single-local-postgres . \
  -f test-values/single-local-postgres.yaml \
  --create-namespace \
  --namespace ncps-single-local-postgres

echo "3️⃣  Installing ncps-single-local-mariadb..."
helm upgrade --install ncps-single-local-mariadb . \
  -f test-values/single-local-mariadb.yaml \
  --create-namespace \
  --namespace ncps-single-local-mariadb

echo "4️⃣  Installing ncps-single-s3-sqlite..."
helm upgrade --install ncps-single-s3-sqlite . \
  -f test-values/single-s3-sqlite.yaml \
  --create-namespace \
  --namespace ncps-single-s3-sqlite

echo "5️⃣  Installing ncps-single-s3-postgres..."
helm upgrade --install ncps-single-s3-postgres . \
  -f test-values/single-s3-postgres.yaml \
  --create-namespace \
  --namespace ncps-single-s3-postgres

echo "6️⃣  Installing ncps-single-s3-mariadb..."
helm upgrade --install ncps-single-s3-mariadb . \
  -f test-values/single-s3-mariadb.yaml \
  --create-namespace \
  --namespace ncps-single-s3-mariadb

# Single Instance with Existing Secrets
echo ""
echo "🔐 Installing Single Instance Deployments with Existing Secrets..."
echo ""

echo "7️⃣  Installing ncps-single-s3-postgres-existing-secret..."
./test-values/install-single-s3-postgres-existing-secret.sh

echo "8️⃣  Installing ncps-single-s3-mariadb-existing-secret..."
./test-values/install-single-s3-mariadb-existing-secret.sh

# HA Deployments
echo ""
echo "🔺 Installing High Availability Deployments..."
echo ""

echo "9️⃣  Installing ncps-ha-s3-postgres..."
helm upgrade --install ncps-ha-s3-postgres . \
  -f test-values/ha-s3-postgres.yaml \
  --create-namespace \
  --namespace ncps-ha-s3-postgres

echo "🔟  Installing ncps-ha-s3-mariadb..."
helm upgrade --install ncps-ha-s3-mariadb . \
  -f test-values/ha-s3-mariadb.yaml \
  --create-namespace \
  --namespace ncps-ha-s3-mariadb

echo ""
echo "========================================="
echo "✅ All deployments installed!"
echo "========================================="
echo ""
echo "Check status with:"
echo "  kubectl get pods --all-namespaces | grep ncps"
echo ""
echo "Cleanup with:"
echo "  ./test-values/CLEANUP.sh"
INSTALL_EOF

chmod +x "$TEST_VALUES_DIR/QUICK-INSTALL.sh"

# Generate CLEANUP.sh
cat > "$TEST_VALUES_DIR/CLEANUP.sh" <<'CLEANUP_EOF'
#!/usr/bin/env bash
# Cleanup script for all NCPS test deployments

set -e

echo "========================================="
echo "Cleaning up NCPS Test Deployments"
echo "========================================="

namespaces=(
  "ncps-single-local-sqlite"
  "ncps-single-local-postgres"
  "ncps-single-local-mariadb"
  "ncps-single-s3-sqlite"
  "ncps-single-s3-postgres"
  "ncps-single-s3-mariadb"
  "ncps-single-s3-postgres-existing-secret"
  "ncps-single-s3-mariadb-existing-secret"
  "ncps-ha-s3-postgres"
  "ncps-ha-s3-mariadb"
)

for ns in "${namespaces[@]}"; do
  echo ""
  echo "🗑️  Removing $ns..."
  helm uninstall "$ns" -n "$ns" 2>/dev/null || echo "  (already uninstalled)"
  kubectl delete namespace "$ns" 2>/dev/null || echo "  (namespace already deleted)"
done

echo ""
echo "========================================="
echo "✅ All deployments cleaned up!"
echo "========================================="
CLEANUP_EOF

chmod +x "$TEST_VALUES_DIR/CLEANUP.sh"

# Generate TEST.sh
cat > "$TEST_VALUES_DIR/TEST.sh" <<'TEST_EOF'
#!/usr/bin/env bash
# Test script for all NCPS deployments
# Calls the Python test script with the generated test-config.yaml

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
TEST_CONFIG="$SCRIPT_DIR/test-config.yaml"
TEST_SCRIPT="$REPO_ROOT/dev-scripts/test-deployments.py"

echo "========================================="
echo "Testing NCPS Deployments"
echo "========================================="
echo ""

# Check if test-config.yaml exists
if [ ! -f "$TEST_CONFIG" ]; then
  echo "❌ Error: test-config.yaml not found"
  echo "   Run ./dev-scripts/generate-test-values.sh first"
  exit 1
fi

# Check if test script exists
if [ ! -f "$TEST_SCRIPT" ]; then
  echo "❌ Error: test-deployments.py not found at $TEST_SCRIPT"
  exit 1
fi

# Check Python dependencies
echo "🔍 Checking Python dependencies..."
python3 -c "import yaml, requests, psycopg2, pymysql, kubernetes, boto3" 2>/dev/null
if [ $? -ne 0 ]; then
  echo ""
  echo "❌ Missing Python dependencies"
  echo ""
  echo "Please install required packages:"
  echo "  pip3 install pyyaml requests psycopg2-binary pymysql kubernetes boto3"
  echo ""
  exit 1
fi

echo "✅ Dependencies OK"
echo ""

# Parse arguments
VERBOSE=""
DEPLOYMENT=""

while [[ $# -gt 0 ]]; do
  case $1 in
    -v|--verbose)
      VERBOSE="-v"
      shift
      ;;
    -d|--deployment)
      DEPLOYMENT="$2"
      shift 2
      ;;
    -h|--help)
      echo "Usage: $0 [OPTIONS]"
      echo ""
      echo "Options:"
      echo "  -d, --deployment NAME    Test only specific deployment"
      echo "  -v, --verbose           Verbose output"
      echo "  -h, --help              Show this help"
      echo ""
      echo "Examples:"
      echo "  $0                                        # Test all deployments"
      echo "  $0 -v                                     # Test all with verbose output"
      echo "  $0 -d ncps-single-local-sqlite            # Test specific deployment"
      echo "  $0 -d ncps-single-s3-postgres -v          # Test specific with verbose"
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      echo "Use -h for help"
      exit 1
      ;;
  esac
done

# Run tests
if [ -n "$DEPLOYMENT" ]; then
  python3 "$TEST_SCRIPT" "$TEST_CONFIG" -d "$DEPLOYMENT" $VERBOSE
else
  python3 "$TEST_SCRIPT" "$TEST_CONFIG" $VERBOSE
fi
TEST_EOF

chmod +x "$TEST_VALUES_DIR/TEST.sh"

echo "✅ Generated TEST.sh"

# Generate install script for single-s3-postgres-existing-secret
cat > "$TEST_VALUES_DIR/install-single-s3-postgres-existing-secret.sh" <<INSTALL_POSTGRES_EOF
#!/usr/bin/env bash
# Install script for ncps-single-s3-postgres-existing-secret
# This demonstrates using an existing secret for S3 and database credentials

set -e

NAMESPACE="ncps-single-s3-postgres-existing-secret"
SECRET_NAME="ncps-external-secrets"
RELEASE_NAME="ncps-single-s3-postgres-existing-secret"

echo "========================================="
echo "Installing \$RELEASE_NAME"
echo "========================================="
echo ""

# Create namespace
echo "📦 Creating namespace \$NAMESPACE..."
kubectl create namespace "\$NAMESPACE" 2>/dev/null || echo "  (namespace already exists)"

# Create the external secret
echo "🔐 Creating external secret \$SECRET_NAME..."
cat <<SECRET_EOF | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: \$SECRET_NAME
  namespace: \$NAMESPACE
type: Opaque
stringData:
  # S3 credentials
  access-key-id: "$S3_ACCESS_KEY"
  secret-access-key: "$S3_SECRET_KEY"

  # Database URL (with URL-encoded credentials)
  database-url: "postgresql://$PG_USER_ENCODED:$PG_PASS_ENCODED@$PG_HOST:$PG_PORT/$PG_DB?sslmode=disable"

  # Database password (for variable substitution)
  password: "$PG_PASS"
SECRET_EOF

echo "✅ Secret created"
echo ""

# Install helm release
echo "📊 Installing Helm release..."
helm upgrade --install "\$RELEASE_NAME" . \\
  -f test-values/single-s3-postgres-existing-secret.yaml \\
  --namespace "\$NAMESPACE"

echo ""
echo "========================================="
echo "✅ Installation complete!"
echo "========================================="
echo ""
echo "Check status:"
echo "  kubectl get pods -n \$NAMESPACE"
echo "  kubectl logs -n \$NAMESPACE -l app.kubernetes.io/name=ncps -c migration"
echo ""
echo "Cleanup:"
echo "  helm uninstall \$RELEASE_NAME -n \$NAMESPACE"
echo "  kubectl delete namespace \$NAMESPACE"
INSTALL_POSTGRES_EOF

chmod +x "$TEST_VALUES_DIR/install-single-s3-postgres-existing-secret.sh"

# Generate install script for single-s3-mariadb-existing-secret
cat > "$TEST_VALUES_DIR/install-single-s3-mariadb-existing-secret.sh" <<INSTALL_MARIADB_EOF
#!/usr/bin/env bash
# Install script for ncps-single-s3-mariadb-existing-secret
# This demonstrates using an existing secret for S3 and database credentials

set -e

NAMESPACE="ncps-single-s3-mariadb-existing-secret"
SECRET_NAME="ncps-external-secrets"
RELEASE_NAME="ncps-single-s3-mariadb-existing-secret"

echo "========================================="
echo "Installing \$RELEASE_NAME"
echo "========================================="
echo ""

# Create namespace
echo "📦 Creating namespace \$NAMESPACE..."
kubectl create namespace "\$NAMESPACE" 2>/dev/null || echo "  (namespace already exists)"

# Create the external secret
echo "🔐 Creating external secret \$SECRET_NAME..."
cat <<SECRET_EOF | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: \$SECRET_NAME
  namespace: \$NAMESPACE
type: Opaque
stringData:
  # S3 credentials
  access-key-id: "$S3_ACCESS_KEY"
  secret-access-key: "$S3_SECRET_KEY"

  # Database URL (with URL-encoded credentials)
  database-url: "mysql://$MARIA_USER_ENCODED:$MARIA_PASS_ENCODED@$MARIA_HOST:$MARIA_PORT/$MARIA_DB"

  # Database password (for variable substitution)
  password: "$MARIA_PASS"
SECRET_EOF

echo "✅ Secret created"
echo ""

# Install helm release
echo "📊 Installing Helm release..."
helm upgrade --install "\$RELEASE_NAME" . \\
  -f test-values/single-s3-mariadb-existing-secret.yaml \\
  --namespace "\$NAMESPACE"

echo ""
echo "========================================="
echo "✅ Installation complete!"
echo "========================================="
echo ""
echo "Check status:"
echo "  kubectl get pods -n \$NAMESPACE"
echo "  kubectl logs -n \$NAMESPACE -l app.kubernetes.io/name=ncps -c migration"
echo ""
echo "Cleanup:"
echo "  helm uninstall \$RELEASE_NAME -n \$NAMESPACE"
echo "  kubectl delete namespace \$NAMESPACE"
INSTALL_MARIADB_EOF

chmod +x "$TEST_VALUES_DIR/install-single-s3-mariadb-existing-secret.sh"

echo "✅ Generated installation scripts for existing-secret configurations"

# Generate test-config.yaml
cat > "$TEST_VALUES_DIR/test-config.yaml" <<TEST_CONFIG_EOF
# Test configuration for NCPS deployments
# Auto-generated by generate-test-values.sh

cluster:
  s3:
    endpoint: "$S3_ENDPOINT"
    bucket: "$S3_BUCKET"
    access_key: "$S3_ACCESS_KEY"
    secret_key: "$S3_SECRET_KEY"

  postgresql:
    host: "$PG_HOST"
    port: $PG_PORT
    database: "$PG_DB"
    username: "$PG_USER"
    password: "$PG_PASS"

  mariadb:
    host: "$MARIA_HOST"
    port: $MARIA_PORT
    database: "$MARIA_DB"
    username: "$MARIA_USER"
    password: "$MARIA_PASS"

# Test data - narinfo hashes to test with
test_data:
  narinfo_hashes:
    - "n5glp21rsz314qssw9fbvfswgy3kc68f"
    - "3acqrvb06vw0w3s9fa3wci433snbi2bg"
    - "1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl"
    - "jiwdym6f9w6v5jcbqf5wn7fmg11v5q0j"
    - "1gxz5nfzfnhyxjdyzi04r86sh61y4i00"
    - "6lwdzpbig6zz8678blcqr5f5q1caxjw2"

# Deployment configurations
deployments:
  - name: "ncps-single-local-sqlite"
    namespace: "ncps-single-local-sqlite"
    mode: "statefulset"
    replicas: 1
    storage:
      type: "local"
      path: "/storage"
    database:
      type: "sqlite"
      path: "/storage/db/ncps.db"

  - name: "ncps-single-local-postgres"
    namespace: "ncps-single-local-postgres"
    mode: "statefulset"
    replicas: 1
    storage:
      type: "local"
      path: "/storage"
    database:
      type: "postgresql"

  - name: "ncps-single-local-mariadb"
    namespace: "ncps-single-local-mariadb"
    mode: "statefulset"
    replicas: 1
    storage:
      type: "local"
      path: "/storage"
    database:
      type: "mysql"

  - name: "ncps-single-s3-sqlite"
    namespace: "ncps-single-s3-sqlite"
    mode: "deployment"
    replicas: 1
    storage:
      type: "s3"
    database:
      type: "sqlite"
      path: "/storage/db/ncps.db"

  - name: "ncps-single-s3-postgres"
    namespace: "ncps-single-s3-postgres"
    mode: "deployment"
    replicas: 1
    storage:
      type: "s3"
    database:
      type: "postgresql"

  - name: "ncps-single-s3-mariadb"
    namespace: "ncps-single-s3-mariadb"
    mode: "deployment"
    replicas: 1
    storage:
      type: "s3"
    database:
      type: "mysql"

  - name: "ncps-single-s3-postgres-existing-secret"
    namespace: "ncps-single-s3-postgres-existing-secret"
    mode: "deployment"
    replicas: 1
    storage:
      type: "s3"
    database:
      type: "postgresql"

  - name: "ncps-single-s3-mariadb-existing-secret"
    namespace: "ncps-single-s3-mariadb-existing-secret"
    mode: "deployment"
    replicas: 1
    storage:
      type: "s3"
    database:
      type: "mysql"

  - name: "ncps-ha-s3-postgres"
    namespace: "ncps-ha-s3-postgres"
    mode: "deployment"
    replicas: 2
    storage:
      type: "s3"
    database:
      type: "postgresql"

  - name: "ncps-ha-s3-mariadb"
    namespace: "ncps-ha-s3-mariadb"
    mode: "deployment"
    replicas: 2
    storage:
      type: "s3"
    database:
      type: "mysql"
TEST_CONFIG_EOF

echo "✅ Generated test-config.yaml"

# Generate README.md
cat > "$TEST_VALUES_DIR/README.md" <<'README_EOF'
# NCPS Helm Chart Test Values

This directory contains test values files for various NCPS deployment scenarios using the Kind cluster.

## Structure

### Single Instance Deployments (No Redis, Local Locking)
- **single-local-sqlite.yaml** - Local storage + SQLite (default configuration)
- **single-local-postgres.yaml** - Local storage + PostgreSQL
- **single-local-mariadb.yaml** - Local storage + MariaDB
- **single-s3-sqlite.yaml** - S3 storage + SQLite
- **single-s3-postgres.yaml** - S3 storage + PostgreSQL
- **single-s3-mariadb.yaml** - S3 storage + MariaDB

### Single Instance with Existing Secrets (Testing External Secret Management)
- **single-s3-postgres-existing-secret.yaml** - S3 storage + PostgreSQL + Existing Secret
- **single-s3-mariadb-existing-secret.yaml** - S3 storage + MariaDB + Existing Secret

### High Availability Deployments (With Redis, Distributed Locking)
- **ha-s3-postgres.yaml** - 2 replicas + S3 storage + PostgreSQL + Redis
- **ha-s3-mariadb.yaml** - 2 replicas + S3 storage + MariaDB + Redis

## Quick Start

### Install All Deployments
```bash
cd charts/ncps
./test-values/QUICK-INSTALL.sh
```

### Install Individual Deployment
```bash
# Example: Install single instance with local storage and PostgreSQL
helm upgrade --install ncps-single-local-postgres . \
  -f test-values/single-local-postgres.yaml \
  --create-namespace \
  --namespace ncps-single-local-postgres

# Example: Install with existing secret (includes secret creation)
cd charts/ncps
./test-values/install-single-s3-postgres-existing-secret.sh
```

### Test All Deployments
```bash
./test-values/TEST.sh
```

### Test Specific Deployment
```bash
# Test with verbose output
./test-values/TEST.sh -v

# Test specific deployment
./test-values/TEST.sh -d ncps-single-local-sqlite

# Test specific deployment with verbose output
./test-values/TEST.sh -d ncps-single-s3-postgres -v
```

### Cleanup All Deployments
```bash
./test-values/CLEANUP.sh
```

## Regenerating Test Values

These files are auto-generated. To regenerate with a new image tag:

```bash
# From repository root
./dev-scripts/generate-test-values.sh <image-tag> [registry] [repository]

# Examples:
./dev-scripts/generate-test-values.sh sha-cf09394
./dev-scripts/generate-test-values.sh 0.5.1 docker.io kalbasit/ncps
./dev-scripts/generate-test-values.sh sha3eb1fe1-aarch64-darwin zot.nasreddine.com ncps
```

This will:
1. Query the Kind cluster for current credentials
2. Generate all 10 values files with updated image and credentials
3. Generate test-config.yaml with cluster credentials and test data
4. Regenerate install/cleanup/test scripts

## Key Configuration

All test deployments use:
- **Migration**: Enabled (initContainer for single, job for HA)
- **Namespace**: Each deployment gets its own namespace (same as release name)
- **Image**: Set via generate-test-values.sh script

### Cluster Resources
Cluster credentials are automatically discovered from the running Kind cluster.

## Testing Matrix

| Scenario | Storage | Database | Redis | Replicas | Mode | Existing Secret |
|----------|---------|----------|-------|----------|------|-----------------|
| single-local-sqlite | Local | SQLite | No | 1 | StatefulSet | No |
| single-local-postgres | Local | PostgreSQL | No | 1 | StatefulSet | No |
| single-local-mariadb | Local | MariaDB | No | 1 | StatefulSet | No |
| single-s3-sqlite | S3 | SQLite | No | 1 | Deployment | No |
| single-s3-postgres | S3 | PostgreSQL | No | 1 | Deployment | No |
| single-s3-mariadb | S3 | MariaDB | No | 1 | Deployment | No |
| single-s3-postgres-existing-secret | S3 | PostgreSQL | No | 1 | Deployment | Yes |
| single-s3-mariadb-existing-secret | S3 | MariaDB | No | 1 | Deployment | Yes |
| ha-s3-postgres | S3 | PostgreSQL | Yes | 2 | Deployment | No |
| ha-s3-mariadb | S3 | MariaDB | Yes | 2 | Deployment | No |

## Verification

### Check All Pods
```bash
kubectl get pods --all-namespaces | grep ncps
```

### Check Migration Status
```bash
# For single instance (init container)
kubectl logs -n ncps-single-local-postgres \
  -l app.kubernetes.io/name=ncps -c migration

# For HA (job)
kubectl logs -n ncps-ha-s3-postgres \
  job/ncps-ha-s3-postgres-migration
```

### Test Service
```bash
# Port forward
kubectl port-forward -n ncps-single-local-sqlite \
  svc/ncps-single-local-sqlite 8501:8501

# Health check
curl http://localhost:8501/healthz
```

## Documentation

See [INSTALL.md](./INSTALL.md) for detailed installation instructions and troubleshooting.
README_EOF

# Generate INSTALL.md
cat > "$TEST_VALUES_DIR/INSTALL.md" <<'INSTALL_MD_EOF'
# NCPS Helm Chart Test Installation Commands

## Prerequisites

Ensure you have the Kind cluster running with all dependencies:
```bash
./dev-scripts/k8s-cluster.sh info
```

## Single Instance Deployments

### 1. Single Instance - Local Storage + SQLite
```bash
helm upgrade --install ncps-single-local-sqlite . \
  -f test-values/single-local-sqlite.yaml \
  --create-namespace \
  --namespace ncps-single-local-sqlite
```

### 2. Single Instance - Local Storage + PostgreSQL
```bash
helm upgrade --install ncps-single-local-postgres . \
  -f test-values/single-local-postgres.yaml \
  --create-namespace \
  --namespace ncps-single-local-postgres
```

### 3. Single Instance - Local Storage + MariaDB
```bash
helm upgrade --install ncps-single-local-mariadb . \
  -f test-values/single-local-mariadb.yaml \
  --create-namespace \
  --namespace ncps-single-local-mariadb
```

### 4. Single Instance - S3 Storage + SQLite
```bash
helm upgrade --install ncps-single-s3-sqlite . \
  -f test-values/single-s3-sqlite.yaml \
  --create-namespace \
  --namespace ncps-single-s3-sqlite
```

### 5. Single Instance - S3 Storage + PostgreSQL
```bash
helm upgrade --install ncps-single-s3-postgres . \
  -f test-values/single-s3-postgres.yaml \
  --create-namespace \
  --namespace ncps-single-s3-postgres
```

### 6. Single Instance - S3 Storage + MariaDB
```bash
helm upgrade --install ncps-single-s3-mariadb . \
  -f test-values/single-s3-mariadb.yaml \
  --create-namespace \
  --namespace ncps-single-s3-mariadb
```

## High Availability Deployments

### 7. HA - S3 Storage + PostgreSQL + Redis
```bash
helm upgrade --install ncps-ha-s3-postgres . \
  -f test-values/ha-s3-postgres.yaml \
  --create-namespace \
  --namespace ncps-ha-s3-postgres
```

### 8. HA - S3 Storage + MariaDB + Redis
```bash
helm upgrade --install ncps-ha-s3-mariadb . \
  -f test-values/ha-s3-mariadb.yaml \
  --create-namespace \
  --namespace ncps-ha-s3-mariadb
```

## Verification Commands

### Check deployment status
```bash
# For a specific release
kubectl get pods -n ncps-single-local-sqlite

# Check all releases
kubectl get pods --all-namespaces | grep ncps
```

### Check migration job status (for HA deployments)
```bash
kubectl get jobs -n ncps-ha-s3-postgres
kubectl logs -n ncps-ha-s3-postgres job/ncps-ha-s3-postgres-migration
```

### Check init container logs (for single instance deployments)
```bash
kubectl logs -n ncps-single-local-postgres -l app.kubernetes.io/name=ncps -c migration
```

### Test the service
```bash
# Port forward to test locally
kubectl port-forward -n ncps-single-local-sqlite svc/ncps-single-local-sqlite 8501:8501

# In another terminal
curl http://localhost:8501/healthz
```

## Cleanup

### Uninstall a specific release
```bash
helm uninstall ncps-single-local-sqlite -n ncps-single-local-sqlite
kubectl delete namespace ncps-single-local-sqlite
```

### Uninstall all releases
```bash
./test-values/CLEANUP.sh
```

## Install All at Once

```bash
./test-values/QUICK-INSTALL.sh
```

## Troubleshooting

### Check events
```bash
kubectl get events -n ncps-single-local-sqlite --sort-by='.lastTimestamp'
```

### Describe pod
```bash
kubectl describe pod -n ncps-single-local-sqlite -l app.kubernetes.io/name=ncps
```

### Check secret creation
```bash
kubectl get secret -n ncps-single-local-postgres
kubectl describe secret -n ncps-single-local-postgres ncps-single-local-postgres
```

### Check configmap
```bash
kubectl get configmap -n ncps-single-local-sqlite
kubectl describe configmap -n ncps-single-local-sqlite ncps-single-local-sqlite
```
INSTALL_MD_EOF

echo "✅ Generated helper scripts (QUICK-INSTALL.sh, CLEANUP.sh, TEST.sh)"
echo "✅ Generated documentation (README.md, INSTALL.md)"

echo ""
echo "========================================="
echo "✅ Generation Complete!"
echo "========================================="
echo ""
echo "Generated files in: $TEST_VALUES_DIR"
echo ""
echo "Next steps:"
echo "  cd charts/ncps"
echo "  ./test-values/QUICK-INSTALL.sh        # Install all deployments"
echo "  ./test-values/TEST.sh                 # Test all deployments"
echo "  ./test-values/CLEANUP.sh              # Remove all deployments"
echo ""
echo "Or install/test individually:"
echo "  helm upgrade --install ncps-single-local-sqlite . \\"
echo "    -f test-values/single-local-sqlite.yaml \\"
echo "    --create-namespace --namespace ncps-single-local-sqlite"
echo ""
echo "  ./test-values/TEST.sh -d ncps-single-local-sqlite -v"
echo ""
echo "Python dependencies (for testing):"
echo "  pip3 install pyyaml requests psycopg2-binary pymysql kubernetes boto3"
