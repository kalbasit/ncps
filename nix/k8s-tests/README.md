# NCPS Kubernetes Integration Testing

A Nix-native tool for comprehensive Kubernetes integration testing of the NCPS Helm chart. Tests 13 different deployment permutations across multiple storage backends, database engines, and high-availability configurations.

## Overview

This tool provides a unified CLI (`k8s-tests`) for:

- **Cluster Management**: Create/destroy Kind clusters with all dependencies (MinIO, PostgreSQL, MariaDB, Redis)
- **Test Generation**: Generate Helm values files from declarative Nix configuration
- **Deployment Testing**: Install, test, and cleanup test deployments
- **Complete Workflows**: Run end-to-end integration tests with a single command

## Architecture

```
nix/k8s-tests/
├── config.nix              # Declarative test permutation definitions (13 scenarios)
├── flake-module.nix        # Nix package definition (writeShellApplication)
├── src/
│   ├── k8s-tests.sh        # Main CLI implementation
│   ├── cluster.sh          # Kind cluster lifecycle management
│   └── lib.sh              # Template rendering functions (shell + heredocs)
└── README.md               # This file
```

**Key Design Decisions:**

- **Nix configuration**: Test permutations defined as Nix attribute sets (type-safe, composable)
- **Shell generation**: Templates rendered using shell functions + heredocs (simple, maintainable)
- **Packaged with Nix**: Uses `writeShellApplication` for dependency management
- **Dev shell integration**: Automatically available in PATH via `nix develop`

## Test Permutations

The tool generates 13 test deployment configurations:

### Single Instance (7 scenarios)

1. **single-local-sqlite** - Local storage + SQLite (default config)
1. **single-local-postgres** - Local storage + PostgreSQL
1. **single-local-mariadb** - Local storage + MariaDB
1. **single-s3-sqlite** - S3 storage + SQLite
1. **single-s3-postgres** - S3 storage + PostgreSQL
1. **single-s3-mariadb** - S3 storage + MariaDB
1. **single-s3-postgres-cdc** - S3 + PostgreSQL + CDC enabled

### External Secrets (2 scenarios)

8. **single-s3-postgres-existing-secret** - S3 + PostgreSQL with external secret management
1. **single-s3-mariadb-existing-secret** - S3 + MariaDB with external secret management

### High Availability (4 scenarios)

10. **ha-s3-postgres** - 2 replicas + S3 + PostgreSQL + Redis locks
01. **ha-s3-mariadb** - 2 replicas + S3 + MariaDB + Redis locks
01. **ha-s3-postgres-lock** - 2 replicas + S3 + PostgreSQL advisory locks
01. **ha-s3-postgres-cdc** - 2 replicas + S3 + PostgreSQL + Redis + CDC

## Database Isolation

Each test permutation uses isolated backend resources to prevent data interference:

- **PostgreSQL**: Unique database per test (e.g., `ncps_single_s3_postgres`, `ncps_ha_s3_postgres_redis`)
- **MariaDB**: Unique database per test (e.g., `ncps_single_s3_mariadb`, `ncps_ha_s3_mariadb`)
- **Redis**: Unique database number per test for distributed locking (automatically assigned based on permutation index, scales with new permutations)
- **S3**: Unique object key prefix per test (e.g., `single-s3-postgres/`, `ha-s3-mariadb/`)

This ensures:

- **No data interference**: Test data from different permutations never collides
- **Clean state**: Each test starts with an empty database
- **Concurrent testing**: Tests can run in parallel without race conditions (future enhancement)
- **Easier debugging**: Each test's data is isolated and easily inspectable
- **Auto-scaling**: Adding new permutations automatically gets unique Redis database numbers

Databases are automatically:

- **Created** during `k8s-tests cluster create` (after deploying PostgreSQL/MariaDB)
- **Dropped** during `k8s-tests cleanup` (after removing namespaces)

## Usage

### Prerequisites

The tool is automatically available in the development shell:

```bash
nix develop
```

Or build it directly:

```bash
nix build .#k8s-tests
./result/bin/k8s-tests --help
```

### Complete Workflow (5 steps)

```bash
# Run everything in one command
k8s-tests all
```

This executes:

1. Creates Kind cluster with dependencies
1. Builds and pushes Docker image
1. Generates 13 test values files
1. Installs all 13 deployments
1. Runs comprehensive tests

### Individual Steps

```bash
# Step 1: Create Kind cluster
k8s-tests cluster create

# Step 2: Generate test values (build and push image)
k8s-tests generate --push

# Step 3: Install all test deployments
k8s-tests install

# Step 4: Run tests across all deployments
k8s-tests test

# Step 5: Cleanup (optional)
k8s-tests cleanup
```

### Working with Specific Deployments

```bash
# Install a single deployment
k8s-tests install single-local-sqlite

# Test a specific deployment (verbose)
k8s-tests test single-local-sqlite -v

# Cleanup a specific deployment
k8s-tests cleanup single-local-sqlite
```

### Using External Images

If you've already built and pushed an image:

```bash
# Using a specific tag
k8s-tests generate sha-cf09394

# Using custom registry and repository
k8s-tests generate 0.5.1 docker.io kalbasit/ncps
```

### Cluster Management

```bash
# Show cluster connection information
k8s-tests cluster info

# Destroy the Kind cluster
k8s-tests cluster destroy
```

## Generated Files

The `generate` command creates files in `charts/ncps/test-values/`:

```
charts/ncps/test-values/
├── single-local-sqlite.yaml       # 13 values files
├── single-local-postgres.yaml
├── ...
├── ha-s3-postgres-cdc.yaml
├── test-config.yaml               # Test configuration (credentials, hashes)
├── QUICK-INSTALL.sh               # Helper: Install all deployments
├── TEST.sh                        # Helper: Run all tests
└── CLEANUP.sh                     # Helper: Remove all deployments
```

## Adding New Permutations

To add a new test scenario:

1. **Edit `nix/k8s-tests/config.nix`**:

```nix
{
  permutations = [
    # ... existing permutations

    # Add new permutation
    {
      name = "my-new-scenario";
      description = "My custom test scenario";
      replicas = 1;
      mode = "deployment";
      migration.mode = "initContainer";
      storage = {
        type = "s3";
        # S3 config...
      };
      database = {
        type = "postgresql";
        # DB config...
      };
      redis.enabled = false;
      features = [];  # Optional: ["cdc", "ha", "pod-disruption-budget", "anti-affinity"]
    }
  ];
}
```

2. **Regenerate test values**:

```bash
k8s-tests generate --push
```

3. **Test the new scenario**:

```bash
k8s-tests install my-new-scenario
k8s-tests test my-new-scenario -v
```

## Modifying Templates

Template rendering logic is in `src/lib.sh`. Key functions:

- `render_values_file()` - Main template (calls sub-functions)
- `render_storage_config()` - Storage section (local/S3)
- `render_database_config()` - Database section (sqlite/postgres/mysql)
- `render_redis_config()` - Redis configuration
- `render_cdc_config()` - CDC feature
- `render_pod_disruption_budget()` - PDB configuration
- `render_affinity()` - Anti-affinity rules

Example modification:

```bash
# Edit lib.sh
vim nix/k8s-tests/src/lib.sh

# Rebuild and test
nix develop
k8s-tests generate --push
```

## Troubleshooting

### Cluster Creation Fails

```bash
# Check if Docker is running
docker ps

# Destroy and recreate cluster
k8s-tests cluster destroy
k8s-tests cluster create
```

### Generation Fails

```bash
# Verify Nix config is valid
nix eval --file nix/k8s-tests/config.nix permutations --json | jq 'length'
# Should output: 13

# Check cluster is running
k8s-tests cluster info
```

### Tests Fail

```bash
# Check pod status
kubectl get pods -A | grep ncps

# View logs for specific deployment
kubectl logs -n ncps-single-local-sqlite -l app.kubernetes.io/name=ncps

# Test with verbose output
k8s-tests test single-local-sqlite -v
```

### Image Build Issues

```bash
# Build image manually
nix build .#docker

# Verify image loaded
docker images | grep ncps

# Push manually if needed
skopeo copy docker-daemon:ncps:latest docker://127.0.0.1:30000/ncps:test
```

## Implementation Details

### Nix Configuration (`config.nix`)

- Defines 13 test permutations as Nix attribute set
- Includes composable feature definitions
- Type-safe via Nix evaluation
- Easy to query: `nix eval --json --file config.nix permutations`

### Template Rendering (`lib.sh`)

- Shell functions for composable template generation
- Uses heredocs for YAML generation (readable, maintainable)
- Parses cluster credentials from `cluster.sh info`
- Handles conditional logic (existing secrets, CDC, HA features)

### CLI Script (`k8s-tests.sh`)

- Subcommand-based interface (cluster, generate, install, test, cleanup, all)
- Loads config via `nix eval`
- Delegates cluster management to `cluster.sh`
- Generates helper scripts dynamically

### Nix Package (`flake-module.nix`)

- Uses `writeShellApplication` for automatic dependency management
- Runtime inputs: `jq`, `yq-go`, `kubectl`, `kubernetes-helm`, `kind`, `skopeo`, `git`, `docker`
- Substitutes paths at build time (`CONFIG_FILE`, `LIB_FILE`, `CLUSTER_SCRIPT`)
- Includes ShellCheck linting during build

## Comparison with Old Approach

| Aspect | Before | After |
|--------|--------|-------|
| **Permutation Definition** | 1804-line bash script with heredocs | Nix attribute set (declarative) |
| **Adding Permutations** | Copy/paste 60-line heredoc block | Add Nix attribute |
| **Script Organization** | Scattered in `dev-scripts/` | Consolidated in `nix/k8s-tests/` |
| **CLI Interface** | 5 separate scripts | Single unified command |
| **Dependencies** | System-dependent | Nix-managed (reproducible) |
| **Availability** | Manual PATH management | Automatic in dev shell |

## Related Documentation

- [CLAUDE.md](../../CLAUDE.md) - Development workflow guide
- [docs/docs/Developer Guide/Contributing.md](../../docs/docs/Developer%20Guide/Contributing.md) - Contribution guidelines
- [charts/ncps/tests/validation/](../../charts/ncps/tests/validation/) - Helm validation tests (static)

## Future Enhancements

1. **CI Integration**: Add GitHub workflow to run tests on PRs
1. **Parallel Testing**: Run tests across permutations concurrently
1. **Test Matrix Expansion**: Add more storage/database combinations
1. **Performance Testing**: Integrate load testing
1. **Chaos Testing**: Add failure injection scenarios
