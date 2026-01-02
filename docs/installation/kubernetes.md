[Home](../../README.md) > [Documentation](../README.md) > [Installation](README.md) > Kubernetes

# Kubernetes Installation

Deploy ncps on Kubernetes for production-ready, scalable deployments with manual control over resources.

## Prerequisites

- Kubernetes cluster (version 1.20+)
- kubectl configured and connected to your cluster
- PersistentVolume provisioner available
- 2GB+ available storage

## Quick Start

For production deployments, we recommend using the [Helm Chart](helm.md) for simplified management. This guide shows manual Kubernetes deployment for users who need fine-grained control.

## Basic Deployment (Single Instance)

### Step 1: Create Namespace (Optional)

```bash
kubectl create namespace ncps
```

### Step 2: Create PersistentVolumeClaim

Create `pvc.yaml`:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ncps
  namespace: ncps
  labels:
    app: ncps
    tier: proxy
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 20Gi
```

Apply:

```bash
kubectl apply -f pvc.yaml
```

### Step 3: Create StatefulSet

Create `statefulset.yaml`:

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: ncps
  namespace: ncps
  labels:
    app: ncps
    tier: proxy
spec:
  replicas: 1
  serviceName: ncps
  selector:
    matchLabels:
      app: ncps
      tier: proxy
  template:
    metadata:
      labels:
        app: ncps
        tier: proxy
    spec:
      initContainers:
        # Create directories
        - name: create-directories
          image: alpine:latest
          command:
            - /bin/sh
            - -c
            - "mkdir -m 0755 -p /storage/var && mkdir -m 0700 -p /storage/var/ncps && mkdir -m 0700 -p /storage/var/ncps/db"
          volumeMounts:
            - name: ncps-persistent-storage
              mountPath: /storage

        # Run database migrations
        - name: migrate-database
          image: kalbasit/ncps:latest
          command:
            - /bin/dbmate
            - --url=sqlite:/storage/var/ncps/db/db.sqlite
            - migrate
            - up
          volumeMounts:
            - name: ncps-persistent-storage
              mountPath: /storage

      containers:
        - name: ncps
          image: kalbasit/ncps:latest
          args:
            - /bin/ncps
            - serve
            - --cache-hostname=ncps.yournetwork.local  # TODO: Replace
            - --cache-storage-local=/storage
            - --cache-temp-path=/nar-temp-dir
            - --cache-database-url=sqlite:/storage/var/ncps/db/db.sqlite
            - --cache-upstream-url=https://cache.nixos.org
            - --cache-upstream-url=https://nix-community.cachix.org
            - --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
            - --cache-upstream-public-key=nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs=
          ports:
            - containerPort: 8501
              name: http-web
          volumeMounts:
            - name: ncps-persistent-storage
              mountPath: /storage
            - name: nar-temp-dir
              mountPath: /nar-temp-dir
          resources:
            requests:
              memory: "256Mi"
              cpu: "100m"
            limits:
              memory: "1Gi"
              cpu: "1000m"

      volumes:
        - name: ncps-persistent-storage
          persistentVolumeClaim:
            claimName: ncps
        - name: nar-temp-dir
          emptyDir:
            sizeLimit: 5Gi
```

Apply:

```bash
kubectl apply -f statefulset.yaml
```

### Step 4: Create Service

Create `service.yaml`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: ncps
  namespace: ncps
  labels:
    app: ncps
    tier: proxy
spec:
  type: ClusterIP
  ports:
    - name: http-web
      port: 8501
      targetPort: 8501
  selector:
    app: ncps
    tier: proxy
```

Apply:

```bash
kubectl apply -f service.yaml
```

### Step 5: Create Ingress (Optional)

For external access, create `ingress.yaml`:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ncps
  namespace: ncps
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod  # If using cert-manager
spec:
  ingressClassName: nginx  # Or your ingress class
  tls:
    - hosts:
        - cache.example.com
      secretName: ncps-tls
  rules:
    - host: cache.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: ncps
                port:
                  number: 8501
```

Apply:

```bash
kubectl apply -f ingress.yaml
```

### Step 6: Verify Deployment

```bash
# Check pods
kubectl -n ncps get pods

# Check service
kubectl -n ncps get svc

# View logs
kubectl -n ncps logs -l app=ncps

# Test from within cluster
kubectl -n ncps run test --rm -it --image=curlimages/curl -- \
  curl http://ncps:8501/nix-cache-info
```

## Production Deployment (S3 + PostgreSQL)

For production with S3 storage and PostgreSQL database:

### Create ConfigMap

Create `configmap.yaml`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: ncps-config
  namespace: ncps
data:
  config.yaml: |
    cache:
      hostname: cache.example.com
      storage:
        s3:
          bucket: ncps-cache
          endpoint: https://s3.amazonaws.com  # Scheme (https://) is required
          region: us-east-1
          force-path-style: false  # Set to true for MinIO
      database-url: postgresql://ncps:PASSWORD@postgres:5432/ncps?sslmode=require
      upstream:
        urls:
          - https://cache.nixos.org
        public-keys:
          - cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
    prometheus:
      enabled: true
```

### Create Secret for S3 Credentials

```bash
kubectl -n ncps create secret generic ncps-s3-credentials \
  --from-literal=access-key-id=YOUR_ACCESS_KEY \
  --from-literal=secret-access-key=YOUR_SECRET_KEY
```

### Update Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ncps
  namespace: ncps
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ncps
  template:
    metadata:
      labels:
        app: ncps
    spec:
      initContainers:
        - name: migrate-database
          image: kalbasit/ncps:latest
          command:
            - /bin/dbmate
            - migrate
            - up
          env:
            - name: DBMATE_MIGRATIONS_DIR
              value: /share/ncps/db/migrations/postgres
          envFrom:
            - configMapRef:
                name: ncps-config
          volumeMounts:
            - name: config
              mountPath: /config.yaml
              subPath: config.yaml

      containers:
        - name: ncps
          image: kalbasit/ncps:latest
          args:
            - /bin/ncps
            - serve
            - --config=/config.yaml
          env:
            - name: CACHE_STORAGE_S3_ACCESS_KEY_ID
              valueFrom:
                secretKeyRef:
                  name: ncps-s3-credentials
                  key: access-key-id
            - name: CACHE_STORAGE_S3_SECRET_ACCESS_KEY
              valueFrom:
                secretKeyRef:
                  name: ncps-s3-credentials
                  key: secret-access-key
          ports:
            - containerPort: 8501
              name: http
          volumeMounts:
            - name: config
              mountPath: /config.yaml
              subPath: config.yaml
            - name: temp
              mountPath: /tmp
          livenessProbe:
            httpGet:
              path: /nix-cache-info
              port: 8501
            initialDelaySeconds: 30
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /nix-cache-info
              port: 8501
            initialDelaySeconds: 5
            periodSeconds: 5
          resources:
            requests:
              memory: "512Mi"
              cpu: "250m"
            limits:
              memory: "2Gi"
              cpu: "2000m"

      volumes:
        - name: config
          configMap:
            name: ncps-config
        - name: temp
          emptyDir:
            sizeLimit: 10Gi
```

## Monitoring with Prometheus

### Create ServiceMonitor (if using Prometheus Operator)

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: ncps
  namespace: ncps
spec:
  selector:
    matchLabels:
      app: ncps
  endpoints:
    - port: http-web
      path: /metrics
      interval: 30s
```

See [Monitoring Guide](../operations/monitoring.md) for more details.

## High Availability Deployment

For HA with multiple replicas:

### Prerequisites

- Redis deployed in cluster
- S3 storage configured
- PostgreSQL or MySQL database

### Create Deployment with Multiple Replicas

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ncps
  namespace: ncps
spec:
  replicas: 3  # Multiple instances
  selector:
    matchLabels:
      app: ncps
  template:
    # ... same as above but add Redis configuration ...
    spec:
      containers:
        - name: ncps
          args:
            - /bin/ncps
            - serve
            - --config=/config.yaml
            - --cache-redis-addrs=redis:6379  # Add Redis
          # ... rest of container spec ...
```

### Create Pod Disruption Budget

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: ncps-pdb
  namespace: ncps
spec:
  minAvailable: 2
  selector:
    matchLabels:
      app: ncps
```

See [High Availability Guide](../deployment/high-availability.md) for complete HA setup.

## Troubleshooting

### Pods Not Starting

```bash
# Check pod status
kubectl -n ncps get pods

# Describe pod for events
kubectl -n ncps describe pod <pod-name>

# Check logs
kubectl -n ncps logs <pod-name>
kubectl -n ncps logs <pod-name> -c migrate-database  # Init container logs
```

### PVC Not Binding

```bash
# Check PVC status
kubectl -n ncps get pvc

# Check available PVs
kubectl get pv

# Ensure storage class exists
kubectl get storageclass
```

### Service Not Accessible

```bash
# Check service
kubectl -n ncps get svc ncps

# Check endpoints
kubectl -n ncps get endpoints ncps

# Port-forward for testing
kubectl -n ncps port-forward svc/ncps 8501:8501
curl http://localhost:8501/nix-cache-info
```

See the [Troubleshooting Guide](../operations/troubleshooting.md) for more help.

## Next Steps

1. **[Configure Clients](../usage/client-setup.md)** - Set up Nix clients
1. **[Configure Monitoring](../operations/monitoring.md)** - Set up observability
1. **[Review Configuration](../configuration/reference.md)** - Explore more options
1. **Consider [Helm Chart](helm.md)** - For simplified management

## Related Documentation

- [Helm Installation](helm.md) - Simplified Kubernetes deployment
- [Docker Compose Installation](docker-compose.md) - For non-K8s environments
- [High Availability Deployment](../deployment/high-availability.md) - HA setup guide
- [Configuration Reference](../configuration/reference.md) - All configuration options
