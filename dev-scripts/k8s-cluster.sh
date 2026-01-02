#!/usr/bin/env bash
#
# k8s-cluster.sh - NCPS Kubernetes Development Environment
#
# This script sets up a local Kind cluster with MinIO, PostgreSQL, MariaDB, and Redis
# for testing NCPS in a Kubernetes environment.
#
# SECURITY WARNING: This script uses hardcoded credentials and is intended for
# development/testing purposes ONLY. Do NOT use in production environments.
#
# Usage:
#   ./dev-scripts/k8s-cluster.sh create   - Create and configure the cluster
#   ./dev-scripts/k8s-cluster.sh destroy  - Destroy the cluster
#   ./dev-scripts/k8s-cluster.sh info     - Show connection information
#   ./dev-scripts/k8s-cluster.sh help     - Show this help message
#

set -euo pipefail

CLUSTER_NAME="ncps-kind"

# --- Helper Functions ---
check_command() {
    if ! command -v "$1" &> /dev/null; then
        echo "❌ Error: '$1' is not installed. Please install it first." >&2
        exit 1
    fi
}

# Function to safely wait for pods to appear before waiting for them to be ready
wait_for_pods() {
    local label="$1"
    local ns="$2"
    local creation_timeout=120
    local ready_timeout=180s

    echo -n "   - Waiting for pods ($label) to be created..."
    local count=0
    until kubectl get pod -n "$ns" -l "$label" -o name 2>/dev/null | grep -q .; do
        if [ "$count" -ge "$creation_timeout" ]; then
            echo ""
            echo " ⚠️  Timed out waiting for pods to be created by the operator." >&2
            return 1
        fi
        sleep 2
        count=$((count + 2))
    done
    echo " ✅ Found."

    echo -n "   - Waiting for pods ($label) to become Ready..."
    if kubectl wait --for=condition=Ready pod -l "$label" -n "$ns" --timeout="$ready_timeout" >/dev/null 2>&1; then
        echo " ✅ Ready."
    else
        echo "" # Newline for better formatting
        echo " ⚠️  Timed out waiting for pods to become Ready. Please check their status:" >&2
        kubectl get pods -n "$ns" -l "$label" >&2
        return 1
    fi
}

# Extract and display connection information
show_info() {
    if ! kind get clusters 2>/dev/null | grep -q "^$CLUSTER_NAME$"; then
        echo "❌ Error: Cluster '$CLUSTER_NAME' does not exist." >&2
        echo "Run './dev-scripts/k8s-cluster.sh create' first." >&2
        exit 1
    fi

    # Switch to cluster context
    kubectl config use-context "kind-$CLUSTER_NAME" >/dev/null

    # Extract credentials
    local MINIO_ENDPOINT="http://minio.minio.svc.cluster.local:9000"

    local PG_PASS=""
    if kubectl get secret -n data pg17-ncps-app >/dev/null 2>&1; then
        PG_PASS=$(kubectl get secret -n data pg17-ncps-app -o jsonpath="{.data.password}" 2>/dev/null | base64 -d)
    fi

    local MARIA_PASS=""
    if kubectl get secret -n data mariadb-ncps-password >/dev/null 2>&1; then
        MARIA_PASS=$(kubectl get secret -n data mariadb-ncps-password -o jsonpath="{.data.password}" 2>/dev/null | base64 -d)
    fi

    local REDIS_AUTH_MSG="Password: <none> (No Auth)"
    if kubectl get secret -n data redis-ncps >/dev/null 2>&1; then
        local REDIS_PASS=$(kubectl get secret -n data redis-ncps -o jsonpath="{.data.password}" 2>/dev/null | base64 -d)
        REDIS_AUTH_MSG="Password: $REDIS_PASS"
    fi

    echo ""
    echo "========================================================"
    echo "✅ NCPS Kubernetes Development Environment"
    echo "========================================================"
    echo "Cluster: $CLUSTER_NAME"
    echo "Context: kind-$CLUSTER_NAME"
    echo ""
    echo "--- 🪣 S3 (MinIO) ---"
    echo "  Endpoint: $MINIO_ENDPOINT"
    echo "  Bucket:   ncps-bucket"
    echo "  Access:   ncps-access-key"
    echo "  Secret:   ncps-secret-key"
    echo "  Port forward: kubectl port-forward -n minio svc/minio 9000:9000"
    echo ""
    echo "--- 🐘 PostgreSQL 17 ---"
    echo "  Host: pg17-ncps-rw.data.svc.cluster.local"
    echo "  Port: 5432"
    echo "  User: ncps"
    echo "  Pass: $PG_PASS"
    echo "  DB:   ncps"
    echo ""
    echo "--- 🐬 MariaDB ---"
    echo "  Host: mariadb-ncps.data.svc.cluster.local"
    echo "  Port: 3306"
    echo "  User: ncps"
    echo "  Pass: $MARIA_PASS"
    echo "  DB:   ncps"
    echo ""
    echo "--- 🔺 Redis ---"
    echo "  Host: redis-ncps.data.svc.cluster.local"
    echo "  Port: 6379"
    echo "  $REDIS_AUTH_MSG"
    echo "========================================================"
    echo ""
}

# Destroy the cluster
destroy_cluster() {
    if ! kind get clusters 2>/dev/null | grep -q "^$CLUSTER_NAME$"; then
        echo "✅ Cluster '$CLUSTER_NAME' does not exist. Nothing to destroy."
        exit 0
    fi

    echo "🗑️  Destroying Kind cluster '$CLUSTER_NAME'..."
    kind delete cluster --name "$CLUSTER_NAME"
    echo "✅ Cluster destroyed successfully."
}

# Show help message
show_help() {
    cat <<EOF
NCPS Kubernetes Development Environment

Usage: $0 <command>

Commands:
  create   - Create and configure the Kind cluster with all dependencies
  destroy  - Destroy the Kind cluster and all resources
  info     - Display connection information for the cluster
  help     - Show this help message

Examples:
  # Create a new cluster
  $0 create

  # Show connection information
  $0 info

  # Destroy the cluster when done testing
  $0 destroy

Note: This environment is for DEVELOPMENT/TESTING only. It uses hardcoded
credentials and should never be used in production.
EOF
}

# Create the cluster
create_cluster() {
    echo "🚀 Initializing NCPS Kubernetes Development Environment..."
    echo ""

    # 1. Pre-flight Checks
    echo "🔍 Checking prerequisites..."
    check_command docker
    check_command kind
    check_command kubectl
    check_command helm

    if ! docker info > /dev/null 2>&1; then
        echo "❌ Error: Docker is not running." >&2
        exit 1
    fi
    echo "✅ All prerequisites met."
    echo ""

    # 2. Create Kind Cluster
    if ! kind get clusters 2>/dev/null | grep -q "^$CLUSTER_NAME$"; then
        echo "📦 Creating Kind cluster..."
        cat <<EOF | kind create cluster --name "$CLUSTER_NAME" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
# Disable DNS search path inheritance from the host to prevent resolution
# issues, particularly on systems like NixOS.
networking:
  dnsSearch: []
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 30000
    hostPort: 30000
    listenAddress: "127.0.0.1"
    protocol: TCP
EOF
        echo "✅ Cluster created."
    else
        echo "✅ Cluster '$CLUSTER_NAME' already exists. Skipping creation."
    fi
    echo ""

    # 3. Context & Storage
    kubectl config use-context "kind-$CLUSTER_NAME"

    echo "💾 Verifying storage class..."
    if ! kubectl get sc standard > /dev/null 2>&1; then
        # Fallback if standard is missing (rare in new kind versions)
        kubectl apply -f https://raw.githubusercontent.com/rancher/local-path-provisioner/master/deploy/local-path-storage.yaml
        kubectl patch storageclass local-path -p '{"metadata": {"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}'
    fi
    echo ""

    # 4. Install Helm Repos
    echo "📦 Updating Helm repositories..."
    helm repo add minio https://charts.min.io/ --force-update >/dev/null 2>&1
    helm repo add cnpg https://cloudnative-pg.io/charts/ --force-update >/dev/null 2>&1
    helm repo add mariadb-operator https://mariadb-operator.github.io/mariadb-operator/ --force-update >/dev/null 2>&1
    helm repo add ot-helm https://ot-container-kit.github.io/helm-charts/ --force-update >/dev/null 2>&1
    helm repo update > /dev/null
    echo "✅ Helm repositories updated."
    echo ""

    # 5. Install Infrastructure
    echo "🏗️  Installing infrastructure components..."
    echo ""

    # MinIO
    echo "   - Installing MinIO..."
    helm upgrade --install minio minio/minio \
        --namespace minio --create-namespace \
        --set resources.requests.memory=256Mi \
        --set mode=standalone \
        --set rootUser=admin \
        --set rootPassword=password123 \
        --set persistence.enabled=true \
        --set persistence.size=5Gi \
        --wait

    # Configure MinIO (Bucket & Keys)
    echo "   ⚙️  Configuring MinIO (bucket and access keys)..."
    kubectl run minio-configurator \
        --namespace minio \
        --image=minio/mc \
        --restart=Never \
        --rm -i \
        --command -- /bin/sh -c "
            set -e
            echo '--> Waiting for MinIO service...'
            until mc alias set internal http://minio.minio.svc.cluster.local:9000 admin password123; do
                echo '    MinIO not ready yet, retrying in 2s...'
                sleep 2
            done
            echo '--> MinIO is ready. Configuring...'

            echo '    Creating bucket ncps-bucket...'
            mc mb internal/ncps-bucket

            echo '    Creating access keys...'
            mc admin user svcacct add \
                --access-key 'ncps-access-key' \
                --secret-key 'ncps-secret-key' \
                internal admin
        "

    # Operators
    echo "   - Installing CNPG (PostgreSQL Operator)..."
    helm upgrade --install cnpg cnpg/cloudnative-pg \
        --namespace cnpg-system --create-namespace \
        --wait

    echo "   - Installing MariaDB Operator..."
    helm upgrade --install mariadb-operator-crds mariadb-operator/mariadb-operator-crds \
        --namespace mariadb-system --create-namespace \
        --wait >/dev/null 2>&1
    helm upgrade --install mariadb-operator mariadb-operator/mariadb-operator \
        --namespace mariadb-system --create-namespace \
        --set webhook.cert.certManager.enabled=false \
        --wait

    echo "   - Installing Redis Operator..."
    helm upgrade --install redis-operator ot-helm/redis-operator \
        --namespace redis-system --create-namespace \
        --wait
    echo ""

    # 6. Deploy Databases
    echo "🔥 Deploying database instances..."
    kubectl create namespace data --dry-run=client -o yaml | kubectl apply -f - >/dev/null

    # Postgres 17
    echo "   - PostgreSQL 17..."
    cat <<EOF | kubectl apply -f - >/dev/null
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
    echo "   - MariaDB..."
    cat <<EOF | kubectl apply -f - >/dev/null
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
    echo "   - Redis..."
    cat <<EOF | kubectl apply -f - >/dev/null
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
    echo ""

    # 7. Wait for databases to be ready
    echo "⏳ Waiting for databases to initialize (this may take a few minutes)..."
    wait_for_pods "cnpg.io/cluster=pg17-ncps" "data"
    wait_for_pods "app.kubernetes.io/instance=mariadb-ncps" "data"
    wait_for_pods "app=redis-ncps" "data"
    echo ""

    echo "========================================================"
    echo "✅ Cluster created successfully!"
    echo "========================================================"
    echo ""

    # Show connection information
    show_info

    echo ""
    echo "📋 NEXT STEPS:"
    echo ""
    echo "1. Test NCPS with different storage backends and databases"
    echo "2. Deploy NCPS Helm chart: helm install ncps ./helm/ncps"
    echo "3. View cluster info: $0 info"
    echo ""
    echo "🧹 CLEANUP:"
    echo "When you're done testing, destroy the cluster to free resources:"
    echo "  $0 destroy"
    echo ""
}

# --- Main Command Router ---
case "${1:-}" in
    create)
        create_cluster
        ;;
    destroy)
        destroy_cluster
        ;;
    info)
        show_info
        ;;
    help|--help|-h|"")
        show_help
        ;;
    *)
        echo "❌ Error: Unknown command '$1'" >&2
        echo ""
        show_help
        exit 1
        ;;
esac
