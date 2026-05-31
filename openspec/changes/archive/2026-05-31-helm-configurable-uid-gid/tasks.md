## 1. Update values.yaml

- [x] 1.1 Clear `podSecurityContext` defaults — replace body with `{}` (remove `runAsNonRoot`, `runAsUser`, `runAsGroup`, `fsGroup`, `fsGroupChangePolicy`, `seccompProfile`)
- [x] 1.2 Clear `securityContext` defaults — replace body with `{}` (remove `allowPrivilegeEscalation`, `capabilities`, `readOnlyRootFilesystem`)
- [x] 1.3 Clear `migration.securityContext` defaults — strip `runAsNonRoot`, `runAsUser`, `runAsGroup` (keep nothing; leave as `{}`)
- [x] 1.4 Clear `fsck.securityContext` defaults — same
- [x] 1.5 Clear `migrateChunksToNar.securityContext` defaults — same
- [x] 1.6 Clear `migrateNarToChunks.securityContext` defaults — same
- [x] 1.7 Add `containerDefaults.securityContext: {}` section (with explanatory comment) before the existing `securityContext:` key
- [x] 1.8 Add `initImage.securityContext: {}` under `initImage:` (no defaults)

## 2. Wire per-container securityContext to deep-merge with containerDefaults

For each container, replace `{{- toYaml .Values.X.securityContext | nindent N }}` with the deep-merge pattern:
`{{- $ctx := mergeOverwrite (deepCopy (default dict .Values.containerDefaults.securityContext)) (default dict .Values.X.securityContext) }}` then `{{- with $ctx }} securityContext: {{- toYaml . | nindent N }} {{- end }}`

Note: `mergeOverwrite` used instead of `merge` to correctly handle boolean `false` overrides (Go's zero value for bool).

- [x] 2.1 `deployment.yaml` — main ncps container (`securityContext`)
- [x] 2.2 `deployment.yaml` — migration init container (`migration.securityContext`)
- [x] 2.3 `statefulset.yaml` — main ncps container (`securityContext`)
- [x] 2.4 `statefulset.yaml` — migration init container (`migration.securityContext`)
- [x] 2.5 `migration-job.yaml` — migration container (`migration.securityContext`)
- [x] 2.6 `fsck-cronjob.yaml` — fsck container (`fsck.securityContext`)

## 3. Wire create-db-dir init container to initImage.securityContext + containerDefaults

Replace the hardcoded securityContext block with the deep-merge of `initImage.securityContext` over `containerDefaults.securityContext`:

- [x] 3.1 `deployment.yaml` — `create-db-dir` init container
- [x] 3.2 `statefulset.yaml` — `create-db-dir` init container
- [x] 3.3 `migration-job.yaml` — `create-db-dir` init container
- [x] 3.4 `fsck-cronjob.yaml` — `create-db-dir` init container

## 4. Wire migration jobs to deep-merge with containerDefaults

- [x] 4.1 `migrate-chunks-to-nar-job.yaml` — `migrateChunksToNar.securityContext`
- [x] 4.2 `migrate-nar-to-chunks-job.yaml` — `migrateNarToChunks.securityContext`

## 5. Update Helm unit tests

- [x] 5.1 Remove assertions that check for specific default securityContext values (runAsUser, runAsGroup, etc.) across all test files
- [x] 5.2 Add test: `containerDefaults.securityContext` propagates to main container when no per-container override is set
- [x] 5.3 Add test: per-container securityContext overrides `containerDefaults.securityContext`
- [x] 5.4 Add test: `initImage.securityContext` overrides `containerDefaults.securityContext` for `create-db-dir`
- [x] 5.5 Add test: no `securityContext:` key rendered when all security context values are empty

## 6. Verify

- [x] 6.1 Run `helm unittest charts/ncps` — all tests pass
- [x] 6.2 Run `task fmt` — clean exit
