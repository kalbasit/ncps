#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="ncps-kind"

# --- Helper Functions ---
check_command() {
    if ! command -v "$1" &> /dev/null; then
        echo "❌ Error: '$1' is not installed. Please install it first."
        exit 1
    fi
}

echo "🚀 Initializing NCPS Lab (Kind Edition)..."

# 1. Pre-flight Checks
check_command docker
check_command kind
check_command kubectl
check_command helm

if ! docker info > /dev/null 2>&1; then
    echo "❌ Error: Docker is not running."
    exit 1
fi

# 2. Create Kind Cluster
# We use a config to ensure ports are exposed if we ever need Ingress (optional but good practice)
if ! kind get clusters | grep -q "^$CLUSTER_NAME$"; then
    echo "📦 Creating Kind cluster..."
    cat <<EOF | kind create cluster --name "$CLUSTER_NAME" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 30000
    hostPort: 30000
    listenAddress: "127.0.0.1"
    protocol: TCP
EOF
else
    echo "✅ Kind cluster '$CLUSTER_NAME' is already running."
fi

# 3. Context & Storage
kubectl config use-context "kind-$CLUSTER_NAME"

# Kind comes with a 'standard' storage class, but let's ensure it's default
echo "💾 Verifying Storage Class..."
if ! kubectl get sc standard > /dev/null 2>&1; then
    # Fallback if standard is missing (rare in new kind versions)
    kubectl apply -f https://raw.githubusercontent.com/rancher/local-path-provisioner/master/deploy/local-path-storage.yaml
    kubectl patch storageclass local-path -p '{"metadata": {"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}'
fi

# 4. Install Helm Repos
echo "📦 Updating Helm Repos..."
helm repo add minio https://charts.min.io/
helm repo add cnpg https://cloudnative-pg.io/charts/
helm repo add mariadb-operator https://mariadb-operator.github.io/mariadb-operator/
helm repo add ot-helm https://ot-container-kit.github.io/helm-charts/
helm repo update > /dev/null

# 5. Install Infrastructure
echo "🏗️  Installing Infrastructure..."

# MinIO
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
echo "   - CNPG (Postgres)..."
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

# 6. Deploy Databases
echo "🔥 Deploying Database Instances..."
kubectl create namespace data --dry-run=client -o yaml | kubectl apply -f -

# Postgres 17
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

# MariaDB
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

# Redis
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

echo "⏳ Waiting for databases..."
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=postgresql -n data --timeout=180s
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=mariadb -n data --timeout=180s
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=redis -n data --timeout=180s

# --- EXTRACT CREDENTIALS ---
MINIO_ENDPOINT="http://minio.minio.svc.cluster.local:9000"
PG_PASS=$(kubectl get secret -n data pg17-ncps-app -o jsonpath="{.data.password}" | base64 -d)
MARIA_PASS=$(kubectl get secret -n data mariadb-ncps-password -o jsonpath="{.data.password}" | base64 -d)
REDIS_PASS=$(kubectl get secret -n data redis-ncps -o jsonpath="{.data.password}" | base64 -d)

echo ""
echo "========================================================"
echo "✅ Kind Environment Ready!"
echo "========================================================"
echo "Cluster Name: ncps-kind"
echo ""
echo "--- 🪣 S3 (MinIO) ---"
echo "  Endpoint: $MINIO_ENDPOINT"
echo "  User: admin"
echo "  Pass: password123"
echo "  (Port forward: kubectl port-forward -n minio svc/minio 9000:9000)"
echo ""
echo "--- 🐘 Postgres ---"
echo "  Host: pg17-ncps-rw.data.svc.cluster.local"
echo "  User: ncps"
echo "  Pass: $PG_PASS"
echo ""
echo "--- 🐬 MariaDB ---"
echo "  Host: mariadb-ncps.data.svc.cluster.local"
echo "  User: ncps"
echo "  Pass: $MARIA_PASS"
echo ""
echo "--- 🔺 Redis ---"
echo "  Host: redis-ncps-master.data.svc.cluster.local"
echo "  Pass: $REDIS_PASS"
echo "========================================================"
