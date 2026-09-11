# ipadecrypt integration review

Reviewed and pinned: `6ixfalls/ipadecrypt` fork commit `2e9761dfea6dd0dd61f56de0f5befe2feffd7940` (2026-09-11 UTC), module `v0.0.0-20260911004001-2e9761dfea6d`.

The adapter uses public `Request.OperationID`, `Request.JournalDir`, `DeviceConfig.UnlockPIN`, `Cleanup`, and `LoginWithMACAddress` with fresh runtime credentials and a separate context. Only `github.com/londek/ipadecrypt/pkg/ipadecrypt` is imported; no CLI subprocess or internal package is used. A validated `IPA_NOW_APPLE_MAC_ADDRESS` keeps App Store login, purchase, and download requests on one stable identity. The optional `IPA_NOW_DEVICE_UNLOCK_PIN` is passed only in the in-memory device configuration; it is not journaled, logged, persisted in job data, or returned by the API. See [automated cleanup](automated-cleanup.md) for the implemented lifecycle, retry identities and schema v2 migration.

Structured on-device helper events are written to the operator log at the helper-provided severity. Messages are bounded, normalized to one line, and redacted; only reviewed, allowlisted attributes are included. A bounded asynchronous queue drops excess diagnostics instead of blocking device work. Helper events are never persisted in job records or returned by the API.

Verification is enabled, remote cleanup is enabled, unknown/changed SSH keys are rejected, and `UninstallAuto` is explicit. Installed-app jobs use `SourceInstalled` and preserve the preexisting app. Upload/App Store requests acknowledge replacement and uninstall without restoration of a prior build. The worker independently validates the final archive and requires scanned Mach-O files plus successful verification before atomic publication.

## Remaining compatibility limits

- SpringBoard must successfully launch the app to decrypt embedded frameworks. The helper now rejects a main-only fallback when nested Mach-O files remain encrypted. With `IPA_NOW_DEVICE_UNLOCK_PIN`, the library checks the lock state and invokes RemoteCompanion once when needed; without it, the operator must keep the device unlocked on the Home Screen. The PIN is protected from logs, events and journals, but RemoteCompanion requires it as a device process argument.
- The helper uses private iOS installation/uninstallation APIs and signing capabilities in durable mode. Desktop tests and a matching rebuilt binary do not verify jailbreak compatibility. Unsupported operations fail closed; a receipt missing after interrupted helper execution requires review.
- Before writing `helper.done`, the helper positively reaps ptrace-owned children and gives transient process-table entries one second to settle. Persistent live, uninspectable, or bundle-owned processes still fail closed. Bundle and extension teardown failures propagate instead of producing a premature `done` event.
- The new helper uses owned random-token staging under `/private/var/mobile/Media/ipadecrypt-op-<token>`. Legacy jobs from the prior pin have no durable journal and may leave the old shared helper/staging or predictable `/tmp/ipadecrypt-<pid>` resources. They cannot be automatically attested by the new API.
- App Store requests still use context-free private HTTP clients in places. The process-startup `JobTransport` attaches requests and body reads to the exclusive job context; outside-job requests are denied. Cleanup uses SSH, not this transport.
- SAP/Unicorn runtime caches remain reusable private service dependencies under the service user's OS cache. The fork now puts patched IPA temporaries inside `StateDir`; process TMPDIR isolation/reconciliation remains for compatibility with legacy debris. Workspaces, inputs and failed output remain disposable, but the new device journal directory must survive.
- Public verification/local filesystem calls are synchronous. The worker does not abandon ongoing operations when a deadline expires. Shutdown allows 75 seconds before forced process exit; startup then reconciles persisted ownership.
- The pinned revision includes the fork's whitespace-lint fix, complete-bundle verification, corrected Darwin process enumeration, safer child reaping, and lock detection/optional RemoteCompanion unlock. Its Go tests and vet pass, as do ipa-now's hardware-free checks; helper binary reproducibility and real-device behavior remain documented in the historical [validation record](cleanup-fork-validation.md).

## Operator cleanup review

Use this flow only when automatic cleanup remains unconfirmed.

1. Note the job ID, source, phase and installed/replaced/uninstalled flags. Missing result flags do not prove no installation happened.
2. Consult the private SQLite `operations` records for this job and their `device-journal/<operation-id>.json` files while the service is stopped. The fork journal identifies each owned remote directory and app policy. Keep the mapping and journals; do not expose their contents through public APIs or copy them into logs.
3. Inspect the device for a still-running helper or target belonging to the operation. Finish or stop only owned processes, then remove only the recorded operation's resources. Do not use blanket recursive deletion or broad globs. If this is a legacy job without journals, inspect the old `/var/mobile/Media/ipadecrypt/staging`, shared helpers and `/tmp/ipadecrypt-<pid>` resources conservatively; existing shared helpers are not automatically job-owned.
4. Check installation ownership. Preserve preexisting installed-app builds; finish uninstall of job-installed/replaced builds when policy requires it. Do not claim that a prior build was restored.
5. Restart for another automatic recovery pass. If ownership remains uncertain, keep quarantine until inspection establishes that all required cleanup is complete.
6. After manual cleanup, check **I inspected the device and completed all required cleanup** in the UI and confirm. This retries server removal and records an operator attestation; it does not run additional device commands. Journals remain retained. Expired artifacts are unavailable even after confirmation.

## Other dependencies

- [GORM v1.31.1 release](https://github.com/go-gorm/gorm/releases/tag/v1.31.1): reviewed schema lookup/parser and AutoMigrate fixes. The service uses parameterized queries, transactional mutations, silent SQL logging, and a versioned schema.
- [GORM SQLite v1.6.0 release](https://github.com/go-gorm/sqlite/releases/tag/v1.6.0): reviewed the driver integration. SQLite uses WAL, full synchronous commits, a busy timeout, and one database connection for the single-process queue.
- [Vite 8 release notes](https://vite.dev/blog/announcing-vite8): the frontend uses the current Rolldown-based build, compatible with the pinned Node 22 runtime. Vite is development/build tooling; Go serves production assets.
- [Vitest release notes](https://github.com/vitest-dev/vitest/releases): tests use the compatible Vitest 4 line and current patched packages. React, test, and build dependencies are exact-pinned in `web/package.json` with `pnpm-lock.yaml` committed.
