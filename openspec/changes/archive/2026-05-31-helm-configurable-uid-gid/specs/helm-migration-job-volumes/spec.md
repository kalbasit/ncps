## MODIFIED Requirements

### Requirement: The `create-db-dir` init container security context MUST be operator-configurable

The `create-db-dir` busybox init container SHALL derive its securityContext from `initImage.securityContext` deep-merged with `containerDefaults.securityContext`, with `initImage.securityContext` taking priority. No UID, GID, or any other security field SHALL be hardcoded in the template. When both values are empty, no `securityContext:` key is rendered and the container inherits from `podSecurityContext`.

#### Scenario: Init container uses containerDefaults when initImage.securityContext is empty

- **WHEN** `containerDefaults.securityContext` is set and `initImage.securityContext` is empty
- **THEN** the `create-db-dir` container's rendered securityContext SHALL match `containerDefaults.securityContext`

#### Scenario: initImage.securityContext overrides containerDefaults for conflicting keys

- **WHEN** `containerDefaults.securityContext.runAsUser: 1000` and `initImage.securityContext.runAsUser: 2000`
- **THEN** the `create-db-dir` container's rendered securityContext SHALL have `runAsUser: 2000`

#### Scenario: Changing podSecurityContext.runAsUser propagates to create-db-dir

- **WHEN** `podSecurityContext.runAsUser: 2000` is set and `initImage.securityContext` and `containerDefaults.securityContext` are both empty
- **THEN** the `create-db-dir` container SHALL run as UID 2000 (inherited from pod level, no container-level override)
- **AND** the rendered container manifest SHALL NOT contain a `securityContext:` key
