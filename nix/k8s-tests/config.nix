# NCPS Kind Integration Test Permutations
# This file defines all test scenarios for Helm chart validation
rec {
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
    # High Availability Deployments (3 scenarios)
    # ====================================================================

    # 10. HA - S3 Storage + PostgreSQL + Redis
    {
      name = "ha-s3-postgres";
      description = "High availability with S3 storage, PostgreSQL, and Redis locks";
      replicas = 2;
      cdc.iLoveTimeouts = true;
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
      cdc.iLoveTimeouts = true;
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

    # 12. HA - S3 Storage + PostgreSQL + Redis + CDC
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
      config = {
        cdc = {
          enabled = true;
          min = 16384;
          avg = 65536;
          max = 262144;
        };
      };
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
      "1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl"
      "jiwdym6f9w6v5jcbqf5wn7fmg11v5q0j"
      "1gxz5nfzfnhyxjdyzi04r86sh61y4i00"
      "6lwdzpbig6zz8678blcqr5f5q1caxjw2"
    ];
  };
  # Recursive update function (simplified version of lib.recursiveUpdate)
  recursiveUpdate =
    let
      recurse =
        lhs: rhs:
        if builtins.isAttrs lhs && builtins.isAttrs rhs then
          lhs
          // builtins.mapAttrs (
            name: value: if builtins.hasAttr name lhs then recurse lhs.${name} value else value
          ) rhs
        else
          rhs;
    in
    recurse;

  # Generate Helm values for all permutations
  generateValues =
    {
      cluster,
      image_registry,
      image_repository,
      image_tag,
    }:
    let
      # Parse cluster JSON
      clusterObj = builtins.fromJSON cluster;

      # Helpers to extract cluster info
      inherit (clusterObj) s3;
      pg = clusterObj.postgresql;
      maria = clusterObj.mariadb;
      redisInfo = clusterObj.redis;

      # Automatically assign Redis database numbers based on permutation index
      # This scales automatically as new permutations are added
      permutationNames = builtins.map (p: p.name) permutations;
      getRedisDbNumber =
        permName:
        let
          findIndex =
            lst: name:
            let
              go =
                idx:
                if idx >= builtins.length lst then
                  -1
                else if builtins.elemAt lst idx == name then
                  idx
                else
                  go (idx + 1);
            in
            go 0;
        in
        findIndex permutationNames permName;

      # Process a single permutation
      processPermutation =
        perm:
        let
          # Base Helm values from permutation
          baseValues = {
            image = {
              registry = image_registry;
              repository = image_repository;
              tag = image_tag;
            };

            replicaCount = perm.replicas;
            mode = if perm.mode != null then perm.mode else "statefulset";

            migration = {
              enabled = true;
              inherit (perm.migration) mode;
            };

            config = {
              analytics.reporting.enabled = false;
              hostname = "ncps-${perm.name}.local";

              # Storage Configuration
              storage =
                if perm.storage.type == "local" then
                  {
                    type = "local";
                    local = {
                      inherit (perm.storage.local) path;
                      persistence = {
                        inherit (perm.storage.local.persistence) enabled;
                        inherit (perm.storage.local.persistence) size;
                      };
                    };
                  }
                else
                  {
                    # s3
                    type = "s3";
                    s3 = {
                      inherit (s3) bucket;
                      inherit (s3) endpoint;
                      region = "us-east-1";
                      prefix = perm.name;
                    }
                    // (
                      if (perm.storage.useExistingSecret or false) then
                        {
                          # existingSecret logic handled if needed, usually just name
                          existingSecret = perm.storage.existingSecretName;
                        }
                      else
                        {
                          accessKeyId = s3.access_key;
                          secretAccessKey = s3.secret_key;
                        }
                    );

                    # Local persistence for SQLite on S3
                    local =
                      if (perm.storage.local.persistence.enabled or false) then
                        {
                          persistence = {
                            enabled = true;
                            inherit (perm.storage.local.persistence) size;
                          };
                        }
                      else
                        { };
                  };

              # Database Configuration
              database =
                if perm.database.type == "sqlite" then
                  {
                    type = "sqlite";
                    sqlite.path = perm.database.sqlite.path;
                  }
                else if perm.database.type == "postgresql" then
                  {
                    type = "postgresql";
                    postgresql = {
                      inherit (pg) host;
                      inherit (pg) port;
                      database = "ncps_${builtins.replaceStrings [ "-" ] [ "_" ] perm.name}";
                      inherit (pg) username;
                      sslMode = "disable";
                    }
                    // (
                      if (perm.database.useExistingSecret or false) then
                        {
                          existingSecret = perm.database.existingSecretName;
                        }
                      else
                        {
                          inherit (pg) password;
                        }
                    );
                  }
                else
                  {
                    # mysql
                    type = "mysql";
                    mysql = {
                      inherit (maria) host;
                      inherit (maria) port;
                      database = "ncps_${builtins.replaceStrings [ "-" ] [ "_" ] perm.name}";
                      inherit (maria) username;
                    }
                    // (
                      if (perm.database.useExistingSecret or false) then
                        {
                          existingSecret = perm.database.existingSecretName;
                        }
                      else
                        {
                          inherit (maria) password;
                        }
                    );
                  };

              # Lock Configuration
              lock =
                if builtins.hasAttr "lock" perm then
                  {
                    inherit (perm.lock) backend;
                  }
                else
                  { };

              # Redis Configuration
              redis =
                if (perm.redis.enabled or false) then
                  {
                    enabled = true;
                    addresses = [ "${redisInfo.host}:${toString redisInfo.port}" ];
                    db = getRedisDbNumber perm.name;
                    useTLS = false;
                  }
                else
                  {
                    enabled = false;
                  };

              # CDC Configuration
              cdc = {
                # Enabled is set by features ("cdc"), but we default strictly here
                # iLoveTimeouts logic:
                iLoveTimeouts = perm.cdc.iLoveTimeouts or false;
              };
            };
          };

          # Feature Definitions (re-declared locally for safety)
          featuresDef = {
            cdc = {
              config = {
                cdc = {
                  enabled = true;
                  min = 16384;
                  avg = 65536;
                  max = 262144;
                };
              };
            };
            ha = { };
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
            existing-secret = { };
          };

          # Merge features
          featuresList = perm.features or [ ];

          mergedValues = builtins.foldl' (
            acc: featureName: recursiveUpdate acc (featuresDef.${featureName} or { })
          ) baseValues featuresList;

        in
        mergedValues;

    in
    # Return a set of { "name" = values; ... }
    builtins.listToAttrs (
      map (perm: {
        inherit (perm) name;
        value = processPermutation perm;
      }) permutations
    );
}
