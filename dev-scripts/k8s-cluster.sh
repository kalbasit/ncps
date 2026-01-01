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

# Function to safely wait for pods to appear before waiting for them to be ready
wait_for_pods() {
    local label="$1"
    local ns="$2"
    local timeout=120
    local count=0

    echo -n "   - Waiting for pods ($label)..."

    # grep -q ensures we actually found output, not just a successful empty return
    until kubectl get pod -n "$ns" -l "$label" 2>/dev/null | grep -q "Running"; do
        if [ "$count" -ge "$timeout" ]; then
            echo ""
            echo " ⚠️  Timed out waiting for Running status. Checking if pods exist..."
            kubectl get pods -n "$ns" -l "$label"
            # We don't exit here because sometimes they are just slow
            break
        fi
        sleep 2
        count=$((count + 2))
    done

    echo " ✅ Found."
    # Now we can safely wait for the Ready condition
    kubectl wait --for=condition=Ready pod -l "$label" -n "$ns" --timeout=60s > /dev/null 2>&1 || true
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
helm repo add minio https://charts.min.io/ --force-update
helm repo add cnpg https://cloudnative-pg.io/charts/ --force-update
helm repo add mariadb-operator https://mariadb-operator.github.io/mariadb-operator/ --force-update
helm repo add ot-helm https://ot-container-kit.github.io/helm-charts/ --force-update
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

# ---  Configure MinIO (Bucket & Keys) ---
echo "⚙️  Configuring MinIO (Bucket & Access Keys)..."
# We run a one-off pod with 'mc' to configure the internal MinIO instance
kubectl run minio-configurator \
    --namespace minio \
    --image=minio/mc \
    --restart=Never \
    --rm -i \
    --command -- /bin/sh -c "
        # Wait a moment for service DNS
        sleep 5;
        # 1. Alias the internal service
        mc alias set internal http://minio.minio.svc.cluster.local:9000 admin password123;

        # 2. Create Bucket (ignore if exists)
        echo 'Creating bucket ncps-bucket...'
        mc mb internal/ncps-bucket || true;

        # 3. Create Service Account (Access/Secret Keys)
        # We try to add it; if it fails (already exists), we ignore
        echo 'Creating access keys...'
        mc admin user svcacct add \
            --access-key 'ncps-access-key' \
            --secret-key 'ncps-secret-key' \
            internal admin || echo 'Key probably exists, skipping creation.'
    " || true

# Operators
echo "   - CNPG (Postgres)..."
helm upgrade --install cnpg cnpg/cloudnative-pg \
    --namespace cnpg-system --create-namespace \
    --wait

echo "   - MariaDB Operator (CRDs + Controller)..."
helm upgrade --install mariadb-operator-crds mariadb-operator/mariadb-operator-crds \
    --namespace mariadb-system --create-namespace \
    --wait
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

# 7. Robust Wait Logic
echo "⏳ Waiting for databases to initialize..."
wait_for_pods "cnpg.io/cluster=pg17-ncps" "data"
wait_for_pods "app.kubernetes.io/instance=mariadb-ncps" "data"
wait_for_pods "app=redis-ncps" "data"

# --- EXTRACT CREDENTIALS ---
MINIO_ENDPOINT="http://minio.minio.svc.cluster.local:9000"

# Postgres (CNPG creates a secret ending in -app for the app user)
PG_PASS=$(kubectl get secret -n data pg17-ncps-app -o jsonpath="{.data.password}" | base64 -d)

# MariaDB (We defined this secret name in the YAML)
MARIA_PASS=$(kubectl get secret -n data mariadb-ncps-password -o jsonpath="{.data.password}" | base64 -d)

# Redis
# Check if secret exists; if not, assume No Auth (default for this operator)
if kubectl get secret -n data redis-ncps >/dev/null 2>&1; then
    REDIS_PASS=$(kubectl get secret -n data redis-ncps -o jsonpath="{.data.password}" | base64 -d)
    REDIS_AUTH_MSG="Password: $REDIS_PASS"
else
    REDIS_PASS=""
    REDIS_AUTH_MSG="Password: <none> (Default: No Auth)"
fi

echo ""
echo "========================================================"
echo "✅ Kind Environment Ready!"
echo "========================================================"
echo "Cluster Name: ncps-kind"
echo ""
echo "--- 🪣 S3 (MinIO) ---"
echo "  Endpoint: $MINIO_ENDPOINT"
echo "  Bucket:   ncps-bucket"
echo "  Access:   ncps-access-key"
echo "  Secret:   ncps-secret-key"
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
echo "  $REDIS_AUTH_MSG"
echo "========================================================"
