#!/usr/bin/env bash
set -euo pipefail

# --- Configuration ---
CLUSTER_NAME="ncps-cluster"
# Minikube resources (adjust based on your Mac's available RAM)
CPUS=4
MEMORY="8192mb"

echo "🚀 Initializing Local K8s ncps Environment..."

# 1. Start Minikube
if ! minikube status -p "$CLUSTER_NAME" | grep -q "Running"; then
    echo "Starting Minikube ($CLUSTER_NAME)..."
    minikube start -p "$CLUSTER_NAME" \
        --driver=docker \
        --cpus="$CPUS" \
        --memory="$MEMORY" \
        --addons=storage-provisioner,default-storageclass,metrics-server
else
    echo "✅ Minikube cluster '$CLUSTER_NAME' is already running."
fi

# Ensure kubectl context is set
minikube profile "$CLUSTER_NAME"

# 2. Add Helm Repositories
echo "📦 Updating Helm Repositories..."
helm repo add minio https://charts.min.io/
helm repo add cnpg https://cloudnative-pg.io/charts/
helm repo add mariadb-operator https://mariadb-operator.github.io/mariadb-operator/
helm repo add ot-helm https://ot-container-kit.github.io/helm-charts/
helm repo update > /dev/null

# 3. Install MinIO (Object Storage)
# Note: We constrain memory because MinIO defaults are aggressive for local labs.
echo "🪣 Installing MinIO..."
helm upgrade --install minio minio/minio \
    --namespace minio --create-namespace \
    --set resources.requests.memory=256Mi \
    --set mode=standalone \
    --set persistence.enabled=true \
    --set persistence.size=5Gi \
    --wait

# 4. Install CloudNativePG (Postgres Operator)
echo "🐘 Installing CloudNativePG Operator..."
helm upgrade --install cnpg cnpg/cloudnative-pg \
    --namespace cnpg-system --create-namespace \
    --wait

# 5. Install MariaDB Operator
echo "🐬 Installing MariaDB Operator..."
helm upgrade --install mariadb-operator mariadb-operator/mariadb-operator \
    --namespace mariadb-system --create-namespace \
    --set webhook.cert.certManager.enabled=false \
    --wait

# 6. Install Redis Operator
echo "🔺 Installing Redis Operator..."
helm upgrade --install redis-operator ot-helm/redis-operator \
    --namespace redis-system --create-namespace \
    --wait

# --- Deploy Database Instances ---

echo "🔥 Deploying Database Instances..."

# Create a namespace for our data workloads
kubectl create namespace data --dry-run=client -o yaml | kubectl apply -f -

# A. Postgres 17 (CNPG)
# Creates a cluster with 1 instance
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
EOF

# B. MariaDB (MariaDB Operator)
# Creates a standalone MariaDB instance
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
  storage:
    size: 1Gi
    storageClassName: standard
  replicas: 1
EOF

# C. Redis (OT-Container-Kit)
# Creates a standalone Redis setup
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

echo "--------------------------------------------------------"
echo "✅ ncps Environment Initialized!"
echo "--------------------------------------------------------"
echo "Storage Class: 'standard' (Minikube default)"
echo "Namespaces:"
echo "  - minio: MinIO Object Storage"
echo "  - data:  Postgres 17, MariaDB, Redis"
echo ""
echo "Monitor status with:"
echo "  kubectl get pods -n data -w"
