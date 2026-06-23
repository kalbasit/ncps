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
      inflightStaging.enabled = true;
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
      inflightStaging.enabled = true;
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

    # ====================================================================
    # CDC Lifecycle (1 scenario)
    # ====================================================================

    # 13. HA - S3 + PostgreSQL + Redis + CDC, exercised through the full
    #     non-CDC -> CDC -> drain -> non-CDC lifecycle. Deployment shape is
    #     identical to ha-s3-postgres-cdc; the difference is the test body,
    #     gated on the "cdc-lifecycle" marker feature (see k8s_tests_tester
    #     ._test_cdc_lifecycle). Topology behaviors (drain auto-exit on pod
    #     restart, multi-replica shared-DB presence) need >1 replica.
    {
      name = "ha-s3-postgres-cdc-lifecycle";
      description = "HA S3 + PostgreSQL + Redis driven through the CDC lifecycle (non-CDC->CDC->drain->non-CDC)";
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
        "cdc-lifecycle"
      ];
    }

    # ====================================================================
    # Local-only phase scenarios (driven by the unified e2e harness)
    # ====================================================================

    # 14. Single instance driven through the CDC lifecycle. `phase` and `modes`
    #     are harness-only keys (ignored by generateValues); CDC boots OFF and
    #     the phase driver toggles it across the lifecycle. Runs in both modes:
    #     the KubernetesDeployment adapter runs the same driver on Kind, reading
    #     the in-pod sqlite DB via a `kubectl debug` sidecar (the image is
    #     shell-less). See change e2e-kubernetes-deployment-adapter.
    {
      name = "cdc-lifecycle";
      description = "Single instance driven through the CDC lifecycle (non-CDC->CDC->drain->non-CDC)";
      replicas = 1;
      mode = null;
      migration.mode = "initContainer";
      phase = "cdc-lifecycle";
      modes = [
        "local"
        "kubernetes"
      ];
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

    # 15. Multi-replica contention driving in-flight NAR staging (download +
    #     chunking windows). Local-only: in-flight staging *activation* is a
    #     single-shot timing event (a cross-pod waiter must hit ncps while the
    #     lock-holder is mid-download, before the NAR is cached). The
    #     KubernetesDeployment adapter reaches replicas via `kubectl
    #     port-forward`, whose per-request latency jitter de-synchronizes the
    #     thundering-herd race so the holder caches the NAR before waiters
    #     contend — activation does not reliably fire on Kind. Serving
    #     correctness IS covered (k8s permutations + cdc-lifecycle); the
    #     contention/activation assertion needs the tight localhost timing, so
    #     this scenario stays local-only. (The adapter still implements every
    #     seam this scenario uses — per-pod addressing, read_state with
    #     inflight_staging, clean_restart — so it can be lifted later if the race
    #     is made deterministic.) See change e2e-kubernetes-deployment-adapter.
    {
      name = "staging-contention";
      description = "Multi-replica contention driving in-flight NAR staging (download + chunking windows)";
      replicas = 2;
      inflightStaging.enabled = true;
      mode = "deployment";
      migration.mode = "job";
      phase = "staging-contention";
      modes = [ "local" ];
      storage = {
        type = "s3";
      };
      database = {
        type = "postgresql";
      };
      redis.enabled = true;
      features = [ ];
    }

    # 16. Single instance, eager CDC, exercising the #1398 compressed-request
    #     mislabel. `phase`/`modes` are harness-only keys (ignored by
    #     generateValues). Single replica + local locker (in-flight staging OFF)
    #     is the reporter's topology. Local-only: the in-flight chunking window is
    #     a timing event (same rationale as staging-contention), so a `.nar.xz`
    #     request must land mid-pull to catch the mislabel; the cross-pod port
    #     forward jitter on Kind de-synchronizes that race. See GitHub issue #1398.
    {
      name = "input-compression";
      description = "Eager-CDC compressed (.nar.xz) request mislabel during the in-flight chunking window (#1398)";
      replicas = 1;
      mode = null;
      migration.mode = "initContainer";
      phase = "input-compression";
      modes = [ "local" ];
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

    cdc-lifecycle = {
      # Marker feature: the deployment shape is unchanged (it composes with
      # "cdc"); this flag drives the lifecycle test body in k8s_tests_tester.
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
                      # Per-scenario bucket so no scenario observes objects
                      # written by another (mirrors the per-scenario ncps_<name>
                      # database). Must match harness_config.scenario_bucket_name
                      # and the buckets created in k8s_tests.py garage setup.
                      # Scenario names are kebab-case by the catalog contract, so
                      # this is already a valid S3 bucket name; kept verbatim (not
                      # normalized) to stay byte-for-byte in sync with the Python
                      # side (plain Nix has no toLower builtin).
                      bucket = "ncps-${perm.name}";
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

              # In-flight NAR staging: an HA-safe alternative to CDC. Enabled per
              # permutation — the HA permutations that previously set
              # cdc.iLoveTimeouts now exercise real staging instead.
              inflightStaging = {
                enabled = perm.inflightStaging.enabled or false;
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
            cdc-lifecycle = { };
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
