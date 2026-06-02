# Spec: Local Store Initialization

## Purpose

Defines requirements for initializing the local store directory structure on startup, ensuring the setup process is safe and idempotent across pod restarts and rolling updates on shared persistent volumes.

## Requirements

### Requirement: Local store directory setup is idempotent

`setupDirs()` must succeed regardless of whether the `store/tmp` directory already exists on disk, so that rolling updates on shared PVCs do not crash the incoming pod.

#### Scenario: First startup (no existing directories)
- **WHEN** `setupDirs()` is called and no store directories exist yet
- **THEN** all required directories (`store/`, `store/nar/`, `store/tmp/`) are created and the call returns nil

#### Scenario: Restart or rolling update (tmp directory already exists)
- **WHEN** `setupDirs()` is called and `store/tmp` already exists (created by a prior pod on a shared PVC)
- **THEN** the call succeeds without error and the tmp directory remains intact

#### Scenario: Existing tmp files are not removed on startup
- **WHEN** `setupDirs()` is called and `store/tmp` contains partial download files left by a previous process
- **THEN** those files are left in place and the call returns nil (cleanup is not performed at startup)
