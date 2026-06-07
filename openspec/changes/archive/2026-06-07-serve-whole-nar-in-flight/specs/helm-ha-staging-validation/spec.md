## ADDED Requirements

### Requirement: HA validation is satisfied by CDC or in-flight staging

The Helm chart's high-availability guard (triggered when `replicaCount > 1`) SHALL pass when **either** `config.cdc.enabled` is true **or** `config.inflightStaging.enabled` is true. CDC SHALL no longer be the only configuration that satisfies HA validation.

#### Scenario: HA with in-flight staging and no CDC

- **WHEN** `replicaCount > 1`, `config.cdc.enabled=false`, and `config.inflightStaging.enabled=true`
- **THEN** chart rendering SHALL succeed without a validation failure

#### Scenario: HA with CDC and no staging

- **WHEN** `replicaCount > 1`, `config.cdc.enabled=true`, and `config.inflightStaging.enabled=false`
- **THEN** chart rendering SHALL succeed without a validation failure

### Requirement: HA validation fails when neither CDC nor staging is enabled, with no bypass

When `replicaCount > 1` and **both** `config.cdc.enabled` and `config.inflightStaging.enabled` are false, chart rendering SHALL fail with a message that names both options as the ways to satisfy HA and references issue #660. There SHALL be no bypass flag: the only resolutions are enabling CDC or enabling in-flight staging.

#### Scenario: Neither enabled — fails

- **WHEN** `replicaCount > 1`, `config.cdc.enabled=false`, and `config.inflightStaging.enabled=false`
- **THEN** chart rendering SHALL fail
- **AND** the error message SHALL name both CDC and in-flight staging as the ways to satisfy HA

### Requirement: The iLoveTimeouts bypass flag is removed

The `config.cdc.iLoveTimeouts` value SHALL be removed from the chart. Setting it SHALL have no effect on HA validation, which SHALL be satisfied only by enabling CDC or in-flight staging. This is a breaking change for any installation that relied on `iLoveTimeouts` to run HA without an HA-safe mechanism; the migration is to set `config.inflightStaging.enabled=true` (or `config.cdc.enabled=true`).

#### Scenario: iLoveTimeouts no longer bypasses the guard

- **WHEN** `replicaCount > 1`, `config.cdc.enabled=false`, and `config.inflightStaging.enabled=false`
- **AND** a (now-unrecognized) `config.cdc.iLoveTimeouts=true` value is supplied
- **THEN** chart rendering SHALL still fail the HA validation
