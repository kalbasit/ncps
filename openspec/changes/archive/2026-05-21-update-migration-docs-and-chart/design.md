## Context

The dbmate→Ent migration (PR #1229) removed the `dbmate` binary from the
runtime container image — only `/bin/ncps` ships now. Three of the Helm
chart's templates and the `_helpers.tpl` partial still invoke
`/bin/dbmate up` and set the `DATABASE_URL` env var, so Helm deploys of
the new branch fail with `exec: "/bin/dbmate": stat /bin/dbmate: no such
file or directory`. Developer + user docs also reference the old
`dbmate --migrations-dir db/migrations/<dialect>` workflow.

The `database-migrations` spec already declares `ncps migrate up` as the
runtime migration entrypoint (see "Migrations are applied at runtime by
Goose against the `schema_migrations` table"), so this change is
chart + docs alignment, plus one new requirement that makes the runtime
binary contract explicit.

## Goals / Non-Goals

**Goals:**
- Helm deploys of the post-Ent branch succeed without manual intervention.
- Developer + user docs describe the workflow that actually exists.
- The runtime image's migration entrypoint is documented as a single source of truth (`/bin/ncps migrate up`).

**Non-Goals:**
- Re-introducing dbmate to the runtime / docker image.
- Changing any migration behavior or content.
- Touching the dbmate availability in the dev shell (already restored in `chore(nix): restore dbmate to the dev shell only`).
- Modifying `dev-scripts/test-migration-e2e.py` (already exercises `ncps migrate up`).

## Decisions

### D1. Use `CACHE_DATABASE_URL`, not a new env var

`pkg/ncps/migrate.go` already declares the `--cache-database-url` flag with `Sources: flagSources("cache.database.url", "CACHE_DATABASE_URL")`. The Helm chart can therefore set `CACHE_DATABASE_URL` and call `/bin/ncps migrate up` with no extra arguments. No Go changes needed.

**Alternatives considered:**
- Wrap the call in `/bin/sh -c "ncps migrate up --cache-database-url=$DATABASE_URL"`. Rejected: introduces a shell dependency on the runtime image (which is a distroless layered build via `buildLayeredImage`) and adds a process layer for no benefit.
- Add a new `NCPS_DATABASE_URL` env var. Rejected: the existing `CACHE_DATABASE_URL` already works; defining a second name fragments the contract.

### D2. Rename only the env-var name in `ncps.migrationDatabaseURLEnv`

The helper currently emits `- name: DATABASE_URL`. Change it to `- name: CACHE_DATABASE_URL`. Both literal-value and `valueFrom.secretKeyRef` branches keep the same secret key (`database-url`) — only the env-var name changes. Existing secrets do not need to be reissued.

### D3. Keep the helper named `migrationDatabaseURLEnv`

The name still describes what it does (set the migration job's database URL env). Renaming would force an unrelated test/template diff across the chart.

### D4. Docs use the flag form, not the env-var form

`SQLite Configuration.md` currently shows `dbmate --url=… migrate up`. Replace with `ncps migrate up --cache-database-url=…` (flag form). Reason: the flag form is immediately copy-pasteable; the env-var form requires the reader to also know about the env-var contract.

### D5. Contributing.md gets rewritten, not deleted

The dbmate workflow section (lines ~36 + 56 + 144–160 + 229–234 + 582 + 594 + 628–651 per the earlier grep) is large. Rather than excising it surgically, replace the whole "Database migrations" section with a short version that delegates to the existing `/migrate-new`, `/migrate-up`, and `/migrate-down` skills documented under `.agent/skills/`. The skills already encode the Ent + Atlas + Goose workflow; Contributing.md just needs to point at them.

## Risks / Trade-offs

- **[Risk]** Helm consumers of a previously-deployed chart who upgrade to the new chart without also upgrading the image will hit `exec: "/bin/ncps"` errors (if their image still has the old dbmate-only entrypoint). **Mitigation:** This is the same release boundary as the dbmate-removal commit. The chart change must ship in the same release notes; document it as a breaking change in CHANGELOG / NEWS.
- **[Risk]** Operators with custom secrets keyed on `DATABASE_URL` env-var consumption (e.g. external dashboards that grep pod env) get a benign cosmetic break. **Mitigation:** Mention the env-var rename in the chart README's "Upgrading" section.
- **[Trade-off]** Pointing Contributing.md at skills means readers without access to the skills directory get a less-complete doc. **Mitigation:** Inline a one-paragraph summary of the workflow above the skill links.

## Migration Plan

1. Update `_helpers.tpl` to emit `CACHE_DATABASE_URL`.
2. Update the three chart templates to invoke `/bin/ncps` with args `["migrate","up"]`.
3. Update `charts/ncps/tests/deployment_test.yaml` (and any other unit tests that assert the migration command) to match.
4. Run `helm unittest charts/ncps` to confirm green.
5. Rewrite the dbmate sections in the three doc files.
6. Add a one-line note to `charts/ncps/README.md` under "Upgrading" calling out the env-var rename + binary swap.

No rollback strategy required — these are deployment-artifact / doc edits, not runtime data changes. To roll back, revert the chart commit and the docs commit; the runtime image is unchanged.

## Open Questions

- None. The decisions above are all driven by code that already exists (`flagSources("cache.database.url", "CACHE_DATABASE_URL")`).
