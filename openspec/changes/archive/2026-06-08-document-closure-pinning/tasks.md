## 1. Confirm the contract from source

- [x] 1.1 Re-read `pkg/server/server.go` pin routes/handlers (`pinClosure`, `unpinClosure`, `listPins`) and confirm methods, paths, and status codes (`200`, `404`, JSON array + `application/json`).
- [x] 1.2 Re-read `pkg/cache/cache.go` (`PinClosure`, `UnpinClosure`, `ListPinnedClosures`, `GetPinnedClosureHashes`) and confirm idempotency, 404-on-unknown-narinfo, transitive-reference protection, and upstream fetch-at-pin behavior.
- [x] 1.3 Open `docs/docs/User Guide/Features/CDC.md` and note the callout style, heading structure, and "Related Documentation" footer to mirror.

## 2. Write the Pinning feature page

- [x] 2.1 Create `docs/docs/User Guide/Features/Pinning.md` with an Overview explaining what pinning protects and why (LRU eviction protection for a narinfo + its transitive closure).
- [x] 2.2 Document the three endpoints (`POST`/`DELETE /pin/{hash}.narinfo`, `GET /pins`) with request/response shapes, status codes, idempotency notes, and the unknown-hash `404` case.
- [x] 2.3 Add worked `curl` examples for pinning, unpinning, and listing pins (including the empty-list response).
- [x] 2.4 Add a "Related Documentation" footer linking to Cache Management and the closure-pinning behavior; note that pins have no TTL/expiry today.

## 3. Register and cross-link the page

- [x] 3.1 Choose a fresh 12-char Trilium `noteId` and grep `docs/!!!meta.json` to confirm it does not already exist.
- [x] 3.2 Add the new note object under the `features` parent in `docs/!!!meta.json`, mirroring the CDC note's attribute shape (`title: "Pinning"`, `dataFileName: "Pinning.md"`, `shareAlias: "pinning"`).
- [x] 3.3 Add a link to the Pinning page from `docs/docs/User Guide/Usage/Cache Management.md` (LRU cleanup section) as the supported way to protect paths from eviction.
- [x] 3.4 Add a link to the Pinning page from `docs/docs/User Guide/Getting Started/Concepts.md` where eviction is introduced.

## 4. Verify

- [x] 4.1 Validate `docs/!!!meta.json` still parses as valid JSON and the new entry is well-formed.
- [x] 4.2 Proofread the page: every documented endpoint, status code, and response shape matches the source confirmed in Group 1.
- [x] 4.3 Run `task fmt` and confirm it exits cleanly (no doc/markdown reformatting left pending).
- [x] 4.4 Confirm issue #1021 is addressed by the new page and note it for the closing comment/PR description.
