## MODIFIED Requirements

### Requirement: A lock-losing replica MUST NOT return HTTP 500 from download coordination

A lock-losing replica MUST NOT return HTTP 500 from download coordination. When a replica fails to acquire the distributed download lock for a NAR hash (because another replica holds it), it SHALL instead serve the NAR (if the holder produces it, or via in-flight staging while the holder is still downloading), take over the download (if the holder finishes without producing it), or return a clean cache miss (HTTP 404) if the NAR is genuinely unavailable.

#### Scenario: Holder completes successfully — waiter serves the NAR

- **WHEN** replica B fails to acquire the download lock for hash `H` held by replica A
- **AND** replica A completes the download and the NAR becomes present in shared storage
- **THEN** replica B SHALL detect the asset and serve the NAR with HTTP 200
- **AND** replica B SHALL NOT return HTTP 500

#### Scenario: Holder fails and releases the lock — waiter takes over

- **WHEN** replica B fails to acquire the download lock for hash `H` held by replica A
- **AND** replica A's download fails (e.g. upstream stream reset) and replica A releases the lock without the asset appearing in storage
- **THEN** replica B SHALL re-acquire the download lock and perform the download itself
- **AND** replica B SHALL NOT return HTTP 500 as a result of the original lock-acquisition failure

#### Scenario: NAR genuinely absent upstream — waiter returns 404 not 500

- **WHEN** replica B fails to acquire the download lock for hash `H`
- **AND** the NAR for `H` does not exist upstream (the holder, or B after take-over, observes a 404)
- **THEN** the coordination path SHALL surface `storage.ErrNotFound`
- **AND** the server SHALL return HTTP 404
- **AND** the server SHALL NOT return HTTP 500

#### Scenario: Holder still legitimately downloading past the poll window — waiter does not 500

- **WHEN** replica B fails to acquire the download lock for hash `H` held by replica A
- **AND** replica A is still actively downloading a large NAR and continues to refresh its lock TTL beyond the previous fixed poll timeout
- **THEN** replica B SHALL continue waiting up to the lock TTL bound rather than returning HTTP 500
- **AND** replica B SHALL serve the NAR once it appears, or return HTTP 404 on terminal give-up — never HTTP 500

#### Scenario: Waiter serves from in-flight staging during the holder's download

- **WHEN** replica B fails to acquire the download lock for hash `H` held by replica A
- **AND** in-flight staging is enabled and active so staging parts for `H` are progressively available in shared storage
- **THEN** replica B SHALL serve the NAR by tailing the staging parts with HTTP 200 rather than waiting for the holder to fully complete
- **AND** replica B SHALL NOT return HTTP 500
