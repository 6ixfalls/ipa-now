# ipadecrypt integration review

Reviewed source: `6ixfalls/ipadecrypt`, `fork` commit `fbe5e07bbc7104bc85917cbd98a77201c50f89c8` (2026-09-06). `go.mod` pins `v0.0.0-20260906200042-fbe5e07bbc71` through:

```go
replace github.com/londek/ipadecrypt => github.com/6ixfalls/ipadecrypt v0.0.0-20260906200042-fbe5e07bbc71
```

Only `github.com/londek/ipadecrypt/pkg/ipadecrypt` is imported. The adapter constructs typed `Request` values, calls `Decrypt`, `Login`, and `Verify`, and consumes typed events/results. Target validation is intentionally stricter than the package's permissive `ParseTarget`; arbitrary paths and arbitrary URL hosts are not accepted through the API.

Verification is enabled, remote cleanup is enabled, unknown SSH keys are rejected, and `UninstallAuto` is explicit. Extra source comparison is enabled when a source IPA is available. The worker independently verifies the final archive and rejects zero scanned Mach-O files or missing comparison entries in addition to the package's `OK()` checks. Existing installed-app requests use `SourceInstalled`; they never fall back to an App Store download or replacement.

## Compatibility limitations

The inspected package is not sufficient for unattended cleanup recovery:

- `pkg/ipadecrypt/decrypt.go` discards the error from `dev.Remove(plan.stagingRemote)`. A nil return from `Decrypt` does not prove staging cleanup succeeded.
- `EnsureHelper` stores a checksum-named helper in `/var/mobile/Media/ipadecrypt/helpers`; the public result does not report whether it was created by the current job.
- The helper uses the predictable `/tmp/ipadecrypt-<pid>` staging directory. The existing symlink/file-creation risk remains. Use a dedicated, isolated device; isolation does not fix those primitives.
- There is no public cleanup/recovery API or complete resource manifest. On many failures `Decrypt` returns a nil result, so installation/replacement ownership can be uncertain, including a partially successful install.
- `UninstallAuto` removes builds installed/replaced by the operation. Replacing and uninstalling a build does **not** restore the prior build. App Store and upload requests explicitly acknowledge this policy.
- App Store operations use a private `http.Client` without a timeout and some calls are not made with the job context. The service installs a `JobTransport` as `http.DefaultTransport` once at startup. It attaches all library HTTP requests and response-body reads to the exclusive active job's context. Requests outside a job scope are denied. No process-global transport is swapped during jobs.
- Device calls are cancelled by the package closing its SSH connection. Cleanup is attempted under the job deadline, and an interrupted cleanup remains uncertain. Public verification and local filesystem operations are synchronous; a deadline does not forcibly terminate CPU or filesystem operations. If shutdown does not finish in ten seconds, the process exits while retaining the OS lock until exit; restart requires reconciliation. Never release the device to another process merely because a deadline elapsed.
- Apple login may populate the service user’s OS cache under `ipatool/sap/apple-assets-v2` and `ipatool/unicorn/<version>` with validated runtime dependencies. The public API has no cache-root injection. These are persistent service dependencies, not job IPA/session artifacts; retain them across jobs and manage them under the dedicated service user during stopped-service maintenance.
- The library may create `ipadecrypt-patched-*.ipa` in the OS temporary directory. To isolate these files, the service sets its process temp directory to a private directory under the data root at startup; restart reconciles only that service-owned directory.

The adapter therefore requires operator cleanup review for every successful real-device run and for any error after contact beyond the connection phase. Such jobs remain in `cleaning`, the device is quarantined, and artifacts cannot be downloaded. A proven pre-device network connection failure may retry, at most the configured number of attempts. Authentication and unknown errors are not retried blindly.

## Operator cleanup review

1. Open the job and note its opaque ID, target, source, last recorded phase, error code, and reported installed/replaced/uninstalled flags. On library failure these flags may be unavailable; treat ownership as uncertain.
2. Inspect the dedicated device for a still-running ipadecrypt helper. Finish or stop only the helper belonging to this operation before removing its files.
3. Inspect `/var/mobile/Media/ipadecrypt/staging`, `/var/mobile/Media/ipadecrypt/helpers`, and this operation's `/tmp/ipadecrypt-<pid>` directory. Identify resources through the isolated device's observed state. Remove only job-owned files; do not use blanket recursive deletion or broad globs. Existing shared helpers are not automatically job-owned.
4. For App Store or upload jobs, check whether the app was installed or replaced. Complete automatic uninstall of the operation's build if required. For installed-app requests, preserve the preexisting app. Do not claim restoration of a previous build without actually restoring and verifying it.
5. Confirm no job-owned phone staging files or running helper remain and that the device is ready for another job. If ownership is unclear, keep the job quarantined while investigating.
6. Check **I inspected the device and completed all required cleanup** in the UI, then confirm. This retries server-side removal before recording the terminal result and releasing the quarantine. It does not execute remote cleanup commands on your behalf.

The approval is a durable operator attestation, not an automated verification of phone state. If artifact retention elapsed during review, the job resolves to failed and no download is available. Repeated confirmations do not replay the job.

## Follow-up required in the fork

Before enabling unattended completion, the public package needs an operation ID, a durable resource manifest registered immediately on acquisition, idempotent recovery/cleanup under a separate cancellable cleanup context, and structured cleanup outcomes on every exit. It must report installation ownership even after partial installation, propagate staging removal errors, isolate and clean helper uploads, use safe temporary-directory/file-opening primitives, and accept explicit runtime cache roots, job-scoped temp paths, and HTTP transports directly. Until those changes are pinned and tested, keep the conservative review gate.

## Other dependencies

- [GORM v1.31.1 release](https://github.com/go-gorm/gorm/releases/tag/v1.31.1): reviewed schema lookup/parser and AutoMigrate fixes. The service uses parameterized queries, transactional mutations, silent SQL logging, and a versioned schema.
- [GORM SQLite v1.6.0 release](https://github.com/go-gorm/sqlite/releases/tag/v1.6.0): reviewed the driver integration. SQLite uses WAL, full synchronous commits, a busy timeout, and one database connection for the single-process queue.
- [Vite 8 release notes](https://vite.dev/blog/announcing-vite8): the frontend uses the current Rolldown-based build, compatible with the pinned Node 22 runtime. Vite is development/build tooling; Go serves production assets.
- [Vitest release notes](https://github.com/vitest-dev/vitest/releases): tests use the compatible Vitest 4 line and current patched packages. React, test, and build dependencies are exact-pinned in `web/package.json` with `pnpm-lock.yaml` committed.
