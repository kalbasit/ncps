# Spec: container-defaults-security-context

## Purpose

Defines how `containerDefaults.securityContext` acts as a global fallback for
all containers in the Helm chart, and the policy that all security-context
values default to empty so operators must explicitly opt in to any security
posture.

## Requirements

### Requirement: The chart MUST provide a `containerDefaults.securityContext` key as a global fallback for all containers

`containerDefaults.securityContext` SHALL be deep-merged under every container's per-container securityContext value. Per-container keys win; the global fills missing keys. When both are empty, no `securityContext:` key is rendered.

#### Scenario: Global default applies when no per-container override is set

- **WHEN** `containerDefaults.securityContext` is set and a container has no per-container securityContext
- **THEN** the rendered container manifest SHALL include a `securityContext` block matching `containerDefaults.securityContext`

#### Scenario: Per-container value overrides global for conflicting keys

- **WHEN** `containerDefaults.securityContext.readOnlyRootFilesystem: true` is set globally and `migration.securityContext.readOnlyRootFilesystem: false` is set per-container
- **THEN** the migration container's rendered securityContext SHALL have `readOnlyRootFilesystem: false`
- **AND** all other global keys SHALL still be present

#### Scenario: No securityContext key rendered when both are empty

- **WHEN** `containerDefaults.securityContext` is empty and a container has no per-container securityContext
- **THEN** the rendered container manifest SHALL NOT include a `securityContext:` key

### Requirement: `podSecurityContext`, `securityContext`, and all per-container securityContext values MUST have no default values

All security-context-related values SHALL default to empty (`{}`). Operators explicitly opt into any security posture.

#### Scenario: Bare installation has no identity constraints

- **WHEN** the chart is rendered with no operator-provided security context values
- **THEN** no `runAsUser`, `runAsGroup`, `runAsNonRoot`, or `fsGroup` SHALL appear in any rendered manifest
