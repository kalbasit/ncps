## 1. Fix migration-job.yaml template

- [x] 1.1 Remove the `tmp` volumeMount from the migration container (unconditional removal)
- [x] 1.2 Remove the `tmp` volume from the Job's volumes list (unconditional removal)
- [x] 1.3 Change the `storage` volumeMount condition in the migration container from `or (eq .Values.config.storage.type "local") (eq .Values.config.database.type "sqlite")` to `eq .Values.config.database.type "sqlite"` only
- [x] 1.4 Apply the same narrowed condition to the `storage` volume definition in the Job's volumes list
- [x] 1.5 Apply the same narrowed condition to the storage volumeMount inside the `create-db-dir` initContainer

## 2. Update Helm unit tests

- [x] 2.1 Add or update a test case in `charts/ncps/tests/migration_test.yaml` asserting that, for a PostgreSQL + local-storage configuration, the migration Job has no volume named `tmp`
- [x] 2.2 Add or update a test case asserting that, for a PostgreSQL + local-storage configuration, the migration container has no volumeMount at the cache temp path
- [x] 2.3 Add or update a test case asserting that, for a PostgreSQL + local-storage configuration, the migration Job has no volume named `storage` and no volumeMount at `/storage`
- [x] 2.4 Add a test case asserting that, for an SQLite + local-storage configuration, the migration Job still includes the `storage` volume and volumeMount (regression guard)
- [x] 2.5 Run `helm unittest charts/ncps` and confirm all tests pass

## 3. Verify

- [x] 3.1 Run `nix flake check` (includes helm-unittest-check) and confirm it passes
