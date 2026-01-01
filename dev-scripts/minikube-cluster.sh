#!/usr/bin/env bash
set -euo pipefail

# --- Configuration ---
CLUSTER_NAME="ncps-cluster"
CPUS=4
MEMORY="8192mb"

# --- Helper Functions ---
check_command() {
    if ! command -v "$1" &> /dev/null; then
        echo "❌ Error: '$1' is not installed. Please install it first."
        exit 1
    fi
}

echo "🚀 Initializing NCPS Lab Environment..."

# 1. Pre-flight Checks
echo "🔍 Checking prerequisites..."
check_command docker
check_command minikube
check_command kubectl
check_command helm

if ! docker info > /dev/null 2>&1; then
    echo "❌ Error: Docker is installed but not running. Please start Docker."
    exit 1
fi
echo "✅ Prerequisites met."

# 2. Start Minikube
if ! minikube status -p "$CLUSTER_NAME" | grep -q "Running"; then
    echo "Starting Minikube ($CLUSTER_NAME)..."
    minikube start -p "$CLUSTER_NAME" \
        --driver=docker \
        --cpus="$CPUS" \
        --memory="$MEMORY" \
        --addons=storage-provisioner,default-storageclass
else
    echo "✅ Minikube cluster '$CLUSTER_NAME' is already running."
fi

# Ensure kubectl context is set
minikube profile "$CLUSTER_NAME"

# 3. Add Helm Repositories
echo "📦 Updating Helm Repositories..."
helm repo add minio https://charts.min.io/
helm repo add cnpg https://cloudnative-pg.io/charts/
helm repo add mariadb-operator https://mariadb-operator.github.io/mariadb-operator/
helm repo add ot-helm https://ot-container-kit.github.io/helm-charts/
helm repo update > /dev/null

# 4. Install Infrastructure
echo "🏗️  Installing Infrastructure Components..."

# MinIO (S3)
echo "   - MinIO..."
helm upgrade --install minio minio/minio \
    --namespace minio --create-namespace \
    --set resources.requests.memory=256Mi \
    --set mode=standalone \
    --set rootUser=admin \
    --set rootPassword=password123 \
    --set persistence.enabled=true \
    --set persistence.size=5Gi \
    --wait

# Operators
echo "   - CloudNativePG Operator..."
helm upgrade --install cnpg cnpg/cloudnative-pg \
    --namespace cnpg-system --create-namespace \
    --wait

echo "   - MariaDB Operator..."
helm upgrade --install mariadb-operator mariadb-operator/mariadb-operator \
    --namespace mariadb-system --create-namespace \
    --set webhook.cert.certManager.enabled=false \
    --wait

echo "   - Redis Operator..."
helm upgrade --install redis-operator ot-helm/redis-operator \
    --namespace redis-system --create-namespace \
    --wait

# 5. Deploy Database Instances
echo "🔥 Deploying Database Instances..."
kubectl create namespace data --dry-run=client -o yaml | kubectl apply -f -

# Postgres 17 (CNPG)
cat <<EOF | kubectl apply -f -
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: pg17-ncps
  namespace: data
spec:
  instances: 1
  imageName: ghcr.io/cloudnative-pg/postgresql:17
  storage:
    size: 1Gi
  bootstrap:
    initdb:
      database: ncps
      owner: ncps
EOF

# MariaDB (MariaDB Operator)
cat <<EOF | kubectl apply -f -
apiVersion: k8s.mariadb.com/v1alpha1
kind: MariaDB
metadata:
  name: mariadb-ncps
  namespace: data
spec:
  rootPasswordSecretKeyRef:
    name: mariadb-root-password
    key: password
    generate: true
  username: ncps
  passwordSecretKeyRef:
    name: mariadb-ncps-password
    key: password
    generate: true
  database: ncps
  storage:
    size: 1Gi
    storageClassName: standard
  replicas: 1
EOF

# Redis (OT-Container-Kit)
cat <<EOF | kubectl apply -f -
apiVersion: redis.redis.opstreelabs.in/v1beta2
kind: Redis
metadata:
  name: redis-ncps
  namespace: data
spec:
  kubernetesConfig:
    image: redis:7.0
    imagePullPolicy: IfNotPresent
  storage:
    volumeClaimTemplate:
      spec:
        storageClassName: standard
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 1Gi
EOF

echo "⏳ Waiting for databases to become ready (this may take 1-2 mins)..."
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=postgresql -n data --timeout=120s
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=mariadb -n data --timeout=120s
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=redis -n data --timeout=120s

# --- EXTRACT CREDENTIALS ---

# MinIO
MINIO_ENDPOINT="http://minio.minio.svc.cluster.local:9000"
MINIO_ACCESS="admin"
MINIO_SECRET="password123"

# Postgres
PG_HOST="pg17-ncps-rw.data.svc.cluster.local"
PG_USER="ncps"
# CNPG creates a secret named <cluster-name>-app
PG_PASS=$(kubectl get secret -n data pg17-ncps-app -o jsonpath="{.data.password}" | base64 -d)

# MariaDB
MARIA_HOST="mariadb-ncps.data.svc.cluster.local"
MARIA_USER="ncps"
MARIA_PASS=$(kubectl get secret -n data mariadb-ncps-password -o jsonpath="{.data.password}" | base64 -d)

# Redis
REDIS_HOST="redis-ncps-master.data.svc.cluster.local"
# Redis operator often uses the default user or just a password.
# Checking the standard secret pattern for this operator:
REDIS_PASS=$(kubectl get secret -n data redis-ncps -o jsonpath="{.data.password}" | base64 -d)


echo ""
echo "========================================================"
echo "✅ NCPS Lab Environment Ready!"
echo "========================================================"
echo ""
echo "--- 🪣 S3 Storage (MinIO) ---"
echo "config.storage.s3:"
echo "  endpoint: \"$MINIO_ENDPOINT\""
echo "  accessKeyId: \"$MINIO_ACCESS\""
echo "  secretAccessKey: \"$MINIO_SECRET\""
echo "  bucket: \"ncps-bucket\" (You need to create this via MinIO Console or MC)"
echo "  region: \"us-east-1\""
echo "  useSSL: false"
echo ""
echo "--- 🐘 PostgreSQL ---"
echo "config.database.postgresql:"
echo "  host: \"$PG_HOST\""
echo "  port: 5432"
echo "  username: \"$PG_USER\""
echo "  password: \"$PG_PASS\""
echo "  database: \"ncps\""
echo ""
echo "--- 🐬 MariaDB ---"
echo "config.database.mysql:"
echo "  host: \"$MARIA_HOST\""
echo "  port: 3306"
echo "  username: \"$MARIA_USER\""
echo "  password: \"$MARIA_PASS\""
echo "  database: \"ncps\""
echo ""
echo "--- 🔺 Redis ---"
echo "config.redis:"
echo "  addresses:"
echo "    - \"$REDIS_HOST:6379\""
echo "  password: \"$REDIS_PASS\""
echo ""
echo "========================================================"
echo "To access MinIO Console locally (to create buckets):"
echo "  kubectl port-forward -n minio svc/minio-console 9001:9001"
echo "  Open http://localhost:9001 (User: admin, Pass: password123)"
echo "========================================================"
