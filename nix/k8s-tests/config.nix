# NCPS Kind Integration Test Permutations
# This file defines all test scenarios for Helm chart validation
{
  # Test permutations organized by category
  permutations = [
    # ====================================================================
    # Single Instance Deployments (7 scenarios)
    # ====================================================================

    # 1. Local Storage + SQLite (default configuration)
    {
      name = "single-local-sqlite";
      description = "Single instance with local storage and SQLite";
      replicas = 1;
      mode = null; # Default (statefulset)
      migration.mode = "initContainer";
      storage = {
        type = "local";
        local = {
          path = "/storage";
          persistence = {
            enabled = true;
            size = "5Gi";
          };
        };
      };
      database = {
        type = "sqlite";
        sqlite.path = "/storage/db/ncps.db";
      };
      redis.enabled = false;
      features = [ ];
    }

    # 2. Local Storage + PostgreSQL
    {
      name = "single-local-postgres";
      description = "Single instance with local storage and PostgreSQL";
      replicas = 1;
      mode = null;
      migration.mode = "initContainer";
      storage = {
        type = "local";
        local = {
          path = "/storage";
          persistence = {
            enabled = true;
            size = "5Gi";
          };
        };
      };
      database = {
        type = "postgresql";
        # Credentials injected at generation time from cluster
      };
      redis.enabled = false;
      features = [ ];
    }

    # 3. Local Storage + MariaDB
    {
      name = "single-local-mariadb";
      description = "Single instance with local storage and MariaDB";
      replicas = 1;
      mode = null;
      migration.mode = "initContainer";
      storage = {
        type = "local";
        local = {
          path = "/storage";
          persistence = {
            enabled = true;
            size = "5Gi";
          };
        };
      };
      database = {
        type = "mysql";
        # Credentials injected at generation time from cluster
      };
      redis.enabled = false;
      features = [ ];
    }

    # 4. S3 Storage + SQLite
    {
      name = "single-s3-sqlite";
      description = "Single instance with S3 storage and SQLite";
      replicas = 1;
      mode = "deployment";
      migration.mode = "initContainer";
      storage = {
        type = "s3";
        # S3 credentials injected at generation time from cluster
        # Even though storage is S3, we need local persistence for SQLite database
        local = {
          persistence = {
            enabled = true;
            size = "1Gi";
          };
        };
      };
      database = {
        type = "sqlite";
        sqlite.path = "/storage/db/ncps.db";
      };
      redis.enabled = false;
      features = [ ];
    }

    # 5. S3 Storage + PostgreSQL
    {
      name = "single-s3-postgres";
      description = "Single instance with S3 storage and PostgreSQL";
      replicas = 1;
      mode = "deployment";
      migration.mode = "initContainer";
      storage = {
        type = "s3";
        # S3 credentials injected at generation time from cluster
      };
      database = {
        type = "postgresql";
        # Credentials injected at generation time from cluster
      };
      redis.enabled = false;
      features = [ ];
    }

    # 6. S3 Storage + MariaDB
    {
      name = "single-s3-mariadb";
      description = "Single instance with S3 storage and MariaDB";
      replicas = 1;
      mode = "deployment";
      migration.mode = "initContainer";
      storage = {
        type = "s3";
        # S3 credentials injected at generation time from cluster
      };
      database = {
        type = "mysql";
        # Credentials injected at generation time from cluster
      };
      redis.enabled = false;
      features = [ ];
    }

    # 7. S3 Storage + PostgreSQL + CDC
    {
      name = "single-s3-postgres-cdc";
      description = "Single instance with S3 storage, PostgreSQL, and CDC enabled";
      replicas = 1;
      mode = "deployment";
      migration.mode = "initContainer";
      storage = {
        type = "s3";
        # S3 credentials injected at generation time from cluster
      };
      database = {
        type = "postgresql";
        # Credentials injected at generation time from cluster
      };
      redis.enabled = false;
      features = [ "cdc" ];
    }

    # ====================================================================
    # External Secrets (2 scenarios)
    # ====================================================================

    # 8. S3 Storage + PostgreSQL (with existing secret)
    {
      name = "single-s3-postgres-existing-secret";
      description = "Single instance with S3 storage and PostgreSQL using existing secret";
      replicas = 1;
      mode = "deployment";
      migration.mode = "initContainer";
      storage = {
        type = "s3";
        useExistingSecret = true;
        existingSecretName = "ncps-external-secrets";
      };
      database = {
        type = "postgresql";
        useExistingSecret = true;
        existingSecretName = "ncps-external-secrets";
      };
      redis.enabled = false;
      features = [ "existing-secret" ];
      setupScript = "install-single-s3-postgres-existing-secret.sh";
    }

    # 9. S3 Storage + MariaDB (with existing secret)
    {
      name = "single-s3-mariadb-existing-secret";
      description = "Single instance with S3 storage and MariaDB using existing secret";
      replicas = 1;
      mode = "deployment";
      migration.mode = "initContainer";
      storage = {
        type = "s3";
        useExistingSecret = true;
        existingSecretName = "ncps-external-secrets";
      };
      database = {
        type = "mysql";
        useExistingSecret = true;
        existingSecretName = "ncps-external-secrets";
      };
      redis.enabled = false;
      features = [ "existing-secret" ];
      setupScript = "install-single-s3-mariadb-existing-secret.sh";
    }

    # ====================================================================
    # High Availability Deployments (4 scenarios)
    # ====================================================================

    # 10. HA - S3 Storage + PostgreSQL + Redis
    {
      name = "ha-s3-postgres";
      description = "High availability with S3 storage, PostgreSQL, and Redis locks";
      replicas = 2;
      mode = "deployment";
      migration.mode = "job";
      storage = {
        type = "s3";
        # S3 credentials injected at generation time from cluster
      };
      database = {
        type = "postgresql";
        # Credentials injected at generation time from cluster
      };
      redis = {
        enabled = true;
        # Redis connection injected at generation time from cluster
      };
      features = [
        "ha"
        "pod-disruption-budget"
        "anti-affinity"
      ];
    }

    # 11. HA - S3 Storage + MariaDB + Redis
    {
      name = "ha-s3-mariadb";
      description = "High availability with S3 storage, MariaDB, and Redis locks";
      replicas = 2;
      mode = "deployment";
      migration.mode = "job";
      storage = {
        type = "s3";
        # S3 credentials injected at generation time from cluster
      };
      database = {
        type = "mysql";
        # Credentials injected at generation time from cluster
      };
      redis = {
        enabled = true;
        # Redis connection injected at generation time from cluster
      };
      features = [
        "ha"
        "pod-disruption-budget"
        "anti-affinity"
      ];
    }

    # 12. HA - S3 Storage + PostgreSQL + PostgreSQL Advisory Locks
    {
      name = "ha-s3-postgres-lock";
      description = "High availability with S3 storage and PostgreSQL (using PostgreSQL advisory locks instead of Redis)";
      replicas = 2;
      mode = "deployment";
      migration.mode = "job";
      storage = {
        type = "s3";
        # S3 credentials injected at generation time from cluster
      };
      database = {
        type = "postgresql";
        # Credentials injected at generation time from cluster
      };
      lock.backend = "postgres";
      redis.enabled = false;
      features = [
        "ha"
        "pod-disruption-budget"
        "anti-affinity"
      ];
    }

    # 13. HA - S3 Storage + PostgreSQL + Redis + CDC
    {
      name = "ha-s3-postgres-cdc";
      description = "High availability with S3 storage, PostgreSQL, Redis locks, and CDC enabled";
      replicas = 2;
      mode = "deployment";
      migration.mode = "job";
      storage = {
        type = "s3";
        # S3 credentials injected at generation time from cluster
      };
      database = {
        type = "postgresql";
        # Credentials injected at generation time from cluster
      };
      redis = {
        enabled = true;
        # Redis connection injected at generation time from cluster
      };
      features = [
        "ha"
        "pod-disruption-budget"
        "anti-affinity"
        "cdc"
      ];
    }
  ];

  # Composable feature definitions
  # These get merged into permutation configurations when features are enabled
  features = {
    cdc = {
      config.cdc.enabled = true;
    };

    ha = {
      # High availability configurations
      # (Most HA config is already in the permutation definitions above)
    };

    pod-disruption-budget = {
      podDisruptionBudget = {
        enabled = true;
        minAvailable = 1;
      };
    };

    anti-affinity = {
      affinity = {
        podAntiAffinity = {
          preferredDuringSchedulingIgnoredDuringExecution = [
            {
              weight = 100;
              podAffinityTerm = {
                labelSelector = {
                  matchExpressions = [
                    {
                      key = "app.kubernetes.io/name";
                      operator = "In";
                      values = [ "ncps" ];
                    }
                  ];
                };
                topologyKey = "kubernetes.io/hostname";
              };
            }
          ];
        };
      };
    };

    existing-secret = {
      # Marker feature for permutations using existing secrets
      # Actual configuration is in the permutation definition
    };
  };

  # Test data (narinfo hashes for validation testing)
  testData = {
    narinfo_hashes = [
      "n5glp21rsz314qssw9fbvfswgy3kc68f"
      "3acqrvb06vw0w3s9fa3wci433snbi2bg"
      "wkagr4l8nfpd6wvmfhk5d2rg0xfl9djz"
      "9yxkwfg6p7md4vnsbhj3d1qg8xlb6ckz"
      "7wkjhg4p9fd2wmvsbhk5d3rg0xlb7djz"
      "5xkmhf9p6gd3wnvsbhj4d2qg9xlb8ekz"
    ];
  };
}
