## MODIFIED Requirements

### Requirement: Staging activates only on cross-pod contention

When the feature is enabled, a replica holding the download lock SHALL begin staging only after it observes a cross-pod waiter for the same NAR hash, recorded as a request marker in the `staging_state` record. A replica using the local (non-distributed) locker SHALL never stage, because no cross-pod waiter can exist. The holder SHALL observe a waiter's request promptly enough to begin staging while the download is still in flight, and if the download has already completed when the request is observed the holder SHALL treat staging as a clean no-op (the NAR is already in shared storage) rather than reporting an error.

#### Scenario: Cross-pod waiter triggers staging

- **WHEN** replica A holds the download lock for hash `H` and is actively downloading
- **AND** replica B fails to acquire the lock and records a staging request for `H`
- **THEN** replica A SHALL observe the request promptly and begin staging the NAR to shared storage while the download is still in flight

#### Scenario: Local locker never stages

- **WHEN** ncps runs with the local (single-instance) locker and the feature is enabled
- **THEN** no staging request can be recorded by another replica
- **AND** the holder SHALL never begin staging

#### Scenario: Request observed after the download completed is a clean no-op

- **WHEN** a staging request for hash `H` is observed by the holder only at or after the download has completed (the in-flight temp file has already been committed to shared storage)
- **THEN** the holder SHALL NOT attempt to stage from the absent temp file
- **AND** it SHALL return without logging an error, because cross-pod waiters serve the completed NAR from shared storage
