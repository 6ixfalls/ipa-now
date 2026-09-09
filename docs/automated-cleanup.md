# Automated cleanup

The real adapter is wired to `github.com/6ixfalls/ipadecrypt` at `v0.0.0-20260908222426-dbce8d45df22`. It uses only the public `Decrypt`, `Cleanup`, `LoginWithMACAddress`, and `Verify` APIs.

## Execution and retry

Before each decryption attempt, SQLite commits a server-generated operation ID associated with the job under the exclusive device lease. The adapter supplies it as `OperationID` along with an absolute `JournalDir` in the private data directory. IDs are never reused, including after a pre-device connection failure. Operation records are private and never included in API payloads.

The library journals mutation intent, uses operation-owned remote staging, and attempts cleanup with a fresh connection on exit. ipa-now still asks the public cleanup API for explicit confirmation rather than trusting a successful decryption return. Only classified pre-device network failures can retry, and any recorded attempt must be confirmed clean before another attempt starts. Cleanup failure is not permission to replay decryption.

A verified result returned alongside `ErrCleanupUnconfirmed` is retained for independent IPA verification and atomic publication. It remains unavailable to download until cleanup is confirmed. Other execution errors retain their failed/cancelled outcome. Reported installation/replacement metadata is preserved even on errors; a confirmed cleanup report updates the uninstall flag.

## Finish and recovery

The worker persists `cleaning` and the pending terminal outcome before cleaning. It attempts independent server and device cleanup even if a server removal fails, and holds device ownership until all calls return. Cleanup has an independent 30-second context, so a job cancellation/deadline does not cancel cleanup. The fork also has an independent 30-second cleanup pass. Shutdown allows 75 seconds; a forced exit leaves recovery to startup rather than releasing live device ownership.

Confirmed cleanup, successful server removal and persisted metadata permit the terminal transition. Unconfirmed reports, errors, deadlines or missing ownership records quarantine the device and block downloads/new claims. One worker cleanup pass is attempted; there is no periodic retry loop. Restart attempts recovery again without re-decrypting. Operator confirmation remains the fallback when ownership or helper completion cannot be established.

On startup, under the process lock and quarantine, the worker reconciles disposable server files, expires artifacts, then calls cleanup for every recorded operation of each unfinished job. Successful recovery resolves the persisted outcome. A crash after `BeginCleanup` preserves its pending completed/failed/cancelled state; interrupted execution fails (or cancels when cancellation was recorded). Expired artifacts cannot become downloadable after cleanup. Quarantine is cleared only after all unresolved jobs are resolved.

## Storage, migration and retention

Schema v2 transactionally adds the `operations` table. Upgrading preserves existing jobs. Queued v1 jobs get new operation records when run; legacy active jobs with no journals require manual inspection. Stop the service and back up the entire data directory before upgrade; do not downgrade the migrated database in place.

`IPA_NOW_DATA_DIR/device-journal` is a private 0700 directory; the fork creates 0600 journal/lock files. It is outside disposable work, input and temp roots. Records include device/app ownership details, not Apple/SSH credentials. SQLite operation mappings, unresolved journals, and durable clean markers are retained with job metadata indefinitely, including after operator confirmation. Do not delete them while the service runs; stale fork atomic-write `.tmp` files may be removed only during stopped-service maintenance. Retention of inputs, failed output, workspaces and artifacts is unchanged.

Preserving journals after cleanup is intentional: a crash can occur after remote removal but before SQLite's terminal commit. Clean-marker replay closes that recovery gap. A missing/corrupt journal never proves that nothing happened. A crash between committing an operation ID and creating its first journal therefore requires inspection.

## Device behavior and operator fallback

Installed-app requests preserve the preexisting app. Upload/App Store requests acknowledge automatic uninstall of installed/replaced builds; uninstall does not restore a prior app or its data. Durable installs/uninstalls use the library helper's private iOS APIs and entitlements. Unsupported device behavior fails closed. Helper locks, seals and completion receipts prevent cleanup from assuming an interrupted process has stopped; hard-killed helpers can require intervention.

For unconfirmed jobs, follow [operator cleanup review](ipadecrypt.md#operator-cleanup-review). Logs use the configured process logger with job IDs, durations, stable outcome reasons and bounded/redacted diagnostics; raw journal contents and progress attributes are not logged.

## Validation

`make check` is the full hardware-free validation command (formatting, lint, race tests, React tests, end-to-end flows and build). Tests cover durable retry IDs, all-attempt confirmation, public journal replay without SSH, additive migration, verified-output recovery, cancellation, timeouts, metadata and quarantine. `make test-device` explicitly operates on an installed app and requires successful automatic cleanup. It must run only on an authorized idle device; hardware-specific private APIs, signing and process behavior cannot be validated by fake device tests. See the [fork review](cleanup-fork-validation.md).
