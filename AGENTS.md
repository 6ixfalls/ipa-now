# AGENTS.md

## Project overview

ipa-now is an open-source, self-hosted service for decrypting legally obtained and purchased iOS IPA files. It is intended as a private alternative to hosted services such as decrypt.day and armconverter.com.

The primary flow is:

1. A user requests an app or IPA.
2. The backend validates and records the request.
3. The request is placed on a durable queue.
4. A worker obtains exclusive access to a compatible jailbroken iOS device and runs the decryption job.
5. The worker verifies and stores the decrypted IPA.
6. The backend exposes job progress and serves the finished artifact to the user.

The decryption engine is the Go package `github.com/londek/ipadecrypt/pkg/ipadecrypt`, maintained as a fork of the upstream ipadecrypt project. Use its public package API; do not copy its internal packages or shell out to its CLI unless a documented compatibility constraint makes that unavoidable.

## Technology stack

- **Backend:** Go. Keep the HTTP API, durable queue, scheduler, worker, storage adapters, and ipadecrypt integration in clearly separated packages even when they ship as one binary.
- **Frontend:** React. Additional frameworks and build tooling may be introduced when they provide a clear benefit, but React remains the frontend foundation and the browser must communicate with the backend through an explicit API contract.
- **Configuration:** environment variables are the primary configuration interface. Parse and validate configuration once during backend startup, fail fast on missing or invalid required values, and pass typed configuration into components rather than reading environment variables throughout the codebase.
- **Device support:** the initial implementation connects to exactly one configured jailbroken device. Model device access behind a small interface and enforce a single active job so later multi-device scheduling does not require rewriting job execution.

Do not add a frontend server-side framework, external queue, or multiple-device scheduler speculatively. Record the rationale and update this file when the selected React tooling, persistence layer, or deployment topology becomes concrete.

## Product boundaries

- Support only IPAs and App Store apps that the operator is legally entitled to obtain and decrypt. Do not add piracy-oriented discovery, catalog scraping, public sharing, DRM circumvention as a service for third parties, or features that disguise misuse.
- The deployment model is a trusted, private network. No application authentication is required by default. Do not imply that the service is safe to expose directly to the public Internet.
- Lack of authentication is not permission to omit input validation, request limits, filesystem isolation, secret handling, or safe defaults.
- Keep decrypted artifacts private to the operator. Do not add public indexing or permanent public URLs.

## Architecture and ownership

Keep these responsibilities separate even if the first implementation is a single process:

- **HTTP/UI layer:** accepts requests, reports state, and serves completed artifacts. It must not perform long-running decryption inline with a request.
- **Job store and queue:** owns durable job state, retries, cancellation intent, and recovery after restart.
- **Scheduler:** initially admits work to the one configured device and guarantees at most one active decryption job. Preserve a boundary that can support multiple devices later without exposing that complexity in the initial configuration or UI.
- **Worker:** creates an isolated job workspace and invokes `ipadecrypt.Decrypt` with a cancellable context.
- **Artifact store:** owns atomic publication, metadata, retention, and deletion of source and decrypted IPAs.
- **Secret store/configuration:** owns Apple account credentials and tokens, SSH credentials, and device host-key material. Secrets must never be stored in job payloads, public API responses, artifact metadata, or logs.

Prefer explicit interfaces between these components so queue, database, storage, and worker implementations can evolve independently. Do not introduce distributed infrastructure until the behavior requires it; preserve the boundaries in-process first.

## Core invariants

Treat these as correctness requirements:

- Job state transitions are explicit and monotonic. A useful baseline is `queued -> running -> verifying -> cleaning -> completed`, with `failed` and `cancelled` reached only after required cleanup has been attempted and its outcome recorded. Persist the failure reason separately from user-safe error text.
- Enqueueing and claiming are race-safe. A job must not run twice concurrently, and a worker crash must leave it recoverable through a lease or equivalent mechanism.
- Acquire the single configured device's exclusive lease before calling ipadecrypt and release it on every exit path. The package explicitly leaves device concurrency control to its caller. Queue additional jobs rather than attempting parallel work on the device.
- Cancellation propagates through `context.Context`; cancelling an HTTP request alone must not accidentally cancel a durable background job.
- Each job gets a server-generated workspace. Never derive filesystem paths directly from a bundle ID, filename, URL, or other user input.
- Publish artifacts atomically only after decryption and verification succeed. Partial files must never be downloadable as completed artifacts.
- Retries must be bounded and classified. Retry transient device, network, and lease failures with backoff; do not blindly retry invalid input, verification failures, or credential failures.
- Cleanup is idempotent and runs after success, failure, cancellation, timeout, and process recovery. Define and test retention separately for encrypted inputs, temporary files, failed outputs, and completed artifacts.
- User-visible progress is derived from persisted job state/events, not only in-memory callbacks.

## Cleanup and recovery

Cleanup is part of job correctness, not a best-effort afterthought. A job must not leave the phone or server in a state that prevents the next queued job from running.

- Track every server-side and device-side resource created or changed by a job, including temporary directories, uploaded helpers, installed or replaced apps, downloaded encrypted IPAs, partial decrypted outputs, and acquired leases.
- Use a cleanup stack or equivalent ownership mechanism so cleanup runs in reverse order for partially completed workflows. Register cleanup immediately after acquiring a resource rather than at the end of the happy path.
- Prefer the ipadecrypt package's default automatic uninstall and remote cleanup behavior. Set `KeepRemoteFiles` only for an explicit, operator-requested diagnostic mode with documented manual cleanup steps.
- Preserve apps that existed on the phone before the job unless the selected uninstall policy explicitly allows removal. Decide the uninstall behavior before execution, record whether the job installed or replaced an app, and do not claim that a previously installed build was restored unless that restoration was actually performed and verified.
- Remove job-owned phone staging files and server workspaces after success, failure, cancellation, and verification errors. Retain only artifacts and diagnostic data permitted by the configured retention policy.
- Write outputs to temporary paths and rename them atomically after verification. On startup, reconcile abandoned `running`, `verifying`, and `cleaning` jobs, expired leases, stale workspaces, and partial artifacts before accepting new device work.
- If phone cleanup fails, do not silently mark the job fully complete. Persist a cleanup error, mark the device unavailable or cleanup-required, and prevent the next job from starting until an automatic recovery pass succeeds or the operator resolves it.
- Cleanup must be safe to retry. Treat missing job-owned files as already cleaned, but never use broad globs or recursive deletion against an unvalidated path.
- Add timeouts to cleanup operations and retain enough redacted metadata for an operator to finish cleanup without exposing credentials.

## ipadecrypt integration

Use `github.com/londek/ipadecrypt/pkg/ipadecrypt` as the integration boundary. The package must be overridden to `replace github.com/londek/ipadecrypt => github.com/6ixfalls/ipadecrypt`, where the changes for implementation are located in the `fork` branch of the repository. The package currently provides `Decrypt(context.Context, Request)`, `Login`, `LoginWithMACAddress`, `ParseTarget`, `AppInfo`, and `Verify`, plus structured progress events and results.

When constructing a decryption request:

- Pass a job-scoped context and workspace-owned `StateDir` and `OutputPath`.
- Translate `OnEvent` callbacks into bounded, persisted progress updates. The callback is synchronous and must return quickly; never perform slow network work directly inside it.
- Serialize `OnAccountUpdate` per Apple account and persist refreshed tokens in the secret store without placing them in the job record.
- Provide `OnAuthCode` through a controlled operator interaction flow. Never log a password, token, SSH key/passphrase, or two-factor code.
- Configure SSH known-host verification with a dedicated `KnownHostsPath`. New-key enrollment must be an explicit operator choice; changed keys must be rejected.
- Use conservative defaults: verification enabled, remote cleanup enabled, and automatic uninstall behavior unless a documented workflow requires otherwise.
- Treat package errors as internal details. Map them to stable application error codes and sanitized user-facing messages while retaining wrapped causes for redacted operator diagnostics.

Pin the ipadecrypt module version. Review its public API and security changes deliberately when updating the dependency. Do not import `github.com/londek/ipadecrypt/internal/...`; Go's `internal` boundary is intentional.

## Security rules

- Treat request fields, uploaded archives, IPA metadata, callback attributes, device output, and filenames as untrusted.
- Validate accepted target forms and upload size before enqueueing. Reject paths, unsupported URL schemes/hosts, malformed identifiers, archive traversal, symlinks, and ambiguous output names at the appropriate boundary.
- Never interpolate untrusted values into shell commands. Prefer typed library calls; otherwise pass arguments without a shell and use the quoting helpers supplied by the owning package.
- Use restrictive permissions for workspaces, artifacts, known-host files, and secrets. Do not commit real credentials, device addresses, tokens, decrypted IPAs, or generated job data.
- Avoid logging complete request structs because they may gain secret-bearing fields over time. Use an allowlist of safe structured fields.
- Downloads should use server-generated opaque identifiers and safe `Content-Disposition` filenames. Prevent traversal and verify that resolved paths remain inside the artifact root.
- If a reverse proxy or public exposure is added, authentication, authorization, CSRF protection where applicable, TLS, request/body limits, rate limiting, and signed or access-checked downloads become required work—not optional hardening.
- The pinned durable helper uses operation-owned private staging and exclusive/no-follow file primitives. Keep device isolation and reject uncertain process/resource ownership; legacy jobs from older forks can still require manual staging inspection.

## Development workflow

The implementation uses Go 1.26.5, SQLite through GORM, and React/TypeScript with Vite and pnpm. Preserve these scaffolding requirements when extending the project:

- Add the canonical setup, development, formatting, lint, test, migration, build, and end-to-end commands to this file and the README in the same change.
- Prefer reproducible, non-interactive commands suitable for local development and CI.
- Pin toolchain and dependency versions. Commit the relevant lockfiles.
- Keep runtime configuration in environment variables. Provide a committed, redacted `.env.example` that documents every supported variable, whether it is required, its default, and whether it contains a secret; never commit a populated `.env` file.
- Give backend environment variables one consistent project prefix. Validate device host, port, user, SSH authentication, known-hosts path, Apple account settings, storage paths, retention, server listen address, and operational timeouts centrally.
- Expose only explicitly selected, non-secret build-time variables to the React bundle. Browser-delivered environment values are public and must never contain Apple credentials, SSH credentials, filesystem paths, or other backend secrets.
- Keep generated artifacts, uploaded/decrypted IPAs, local databases, secrets, and device host-key files out of Git.
- Make the default test suite independent of Apple credentials and physical hardware. Put real-device tests behind an explicit integration-test flag or command.

Before considering a change complete, run all formatting, static-analysis, unit, and relevant integration checks defined by the repository. If a check cannot run because it needs a jailbroken device, Apple account, or other external dependency, state that clearly in the handoff.

## Testing expectations

Favor behavior-focused tests around the risky boundaries:

- job transition validation, atomic claiming, lease expiry, retry classification, cancellation, and restart recovery;
- one active job on the configured device under concurrency, with later requests remaining queued;
- idempotent server and phone cleanup, cleanup-failure device quarantine, startup reconciliation, and artifact publication after failures at every workflow phase;
- path containment, malicious filenames, archive traversal, size limits, and safe download headers;
- log and API-response redaction of all credential types;
- progress-event coalescing/backpressure so noisy callbacks cannot stall decryption;
- ipadecrypt adapter tests using a fake interface, with a small, explicit real-package integration layer;
- end-to-end success, failure, retry, and cancellation flows without requiring a physical device.

Do not weaken verification or security behavior merely to make a test pass. Add a regression test with every bug fix when practical.

## Change guidelines

- Keep changes focused; avoid unrelated dependency, formatting, or architectural churn.
- Preserve user changes in a dirty working tree and never discard them without explicit permission.
- Update documentation and example configuration when behavior, state transitions, environment variables, storage layout, or operator steps change.
- Schema and job-payload changes must be backward compatible or include a migration and recovery story for queued/running jobs.
- Dependency changes require reviewing release notes and running the full available test suite, with extra scrutiny for ipadecrypt, SSH, archive, HTTP, and storage dependencies.
- Explain any new externally reachable endpoint, secret, filesystem write, subprocess, device command, or long-lived goroutine/task in the change summary.


## Selected implementation (2026-09-06)

- Persistence is SQLite (WAL, FULL synchronous commits) through GORM v1.31.1 and the GORM SQLite v1.6.0 driver. CGO and a C compiler are required. The schema is version 2 (additive migration from v1 adds durable operation-to-job mappings); migrations run transactionally at startup and newer schemas are rejected.
- React/TypeScript uses Vite 8, Node 22.15.0, and pnpm 11.24.0 with `web/pnpm-lock.yaml`. Production assets embed in the Go binary. Do not add a frontend server framework or external queue. This single local process is sufficient for one device and supports a simple private deployment on Linux/macOS.
- Device ownership is a transactional singleton lease plus a process-lifetime OS file lock on the private data directory. Recovery runs before new claims. Do not expire this lease under a live worker; do not point independent data directories/processes at the same device.
- The pinned fork is `v0.0.0-20260911004001-2e9761dfea6d`. The adapter uses durable `OperationID`/`JournalDir`, optional `DeviceConfig.UnlockPIN`, and public `Cleanup`. Every attempt gets a fresh ID persisted in SQLite first; completion/retry requires explicit cleanup confirmation for all attempts. Missing legacy journals, unknown ownership or cleanup errors retain quarantine. Never infer cleanup from a nil `Decrypt` error. See `docs/ipadecrypt.md`.
- The public package's context-free HTTP requests are bridged through a process-startup `JobTransport` bound to the exclusive active job. Its OS temporary files are isolated by setting process `TMPDIR` to the private data root's `tmp` directory. These are documented compatibility bridges; replace them with public injection points when the fork supports them.
- Encrypted inputs, workspaces, and failed outputs are removed during cleanup and recovery. Verified outputs expire after `IPA_NOW_ARTIFACT_RETENTION`, including waiting for cleanup confirmation. Secret sessions live separately with private permissions. Safe job metadata is retained; API history is bounded to the newest 200 rows.
- All runtime settings use `IPA_NOW_`; `.env.example` documents every supported variable. The app does not implicitly load dotenv files. `IPA_NOW_APPLE_MAC_ADDRESS` optionally pins one normalized App Store identity and invalidates a cached token when changed. `IPA_NOW_DEVICE_UNLOCK_PIN` optionally supplies an in-memory RemoteCompanion unlock credential and must never be logged, journaled, persisted in jobs, or exposed through `/api`. Browser configuration includes only safe operational status returned through `/api`.

### Canonical commands

- Setup: `make setup` (Go modules and frozen pnpm install).
- Development: `make dev` and `make dev-web` in separate terminals.
- Formatting: `make fmt` (gofmt and Biome); verification: `make fmt-check`.
- Static analysis: `make lint` (format checks, go vet, Biome recommended lint/import checks with warnings treated as errors, and TypeScript).
- Tests: `make test` (race-enabled Go tests and React/Vitest).
- Hardware-free end-to-end: `make e2e` (HTTP, SQLite, worker, cleanup, download).
- Build: `make build`; frontend only: `make frontend`.
- Full required checks: `make check`.
- Migration procedure: `make migrate`; schema initialization/migrations execute transactionally on startup under the data lock. Stop the service and back up the entire private data directory before upgrades.
- Explicit hardware integration: `make test-device`, with `IPA_NOW_INTEGRATION_TARGET` and normal device configuration. It uses an installed app and asserts automatic cleanup while preserving the preexisting app. Never run against a busy device. On failure, start the service afterward to recover or finish cleanup review.


### Frontend module boundaries

Use Biome (pinned in `web/package.json`, configured in `web/biome.json`) for frontend linting, formatting, and import organization. Keep TypeScript type-checking as a separate required check. Do not replace Biome with a type-check-only lint script.

Keep `web/src/App.tsx` focused on composition and shared selection/filter state. Put focused UI components in `components/{layout,workspace,requests,jobs,ui}`, asynchronous lifecycle and feedback in `hooks`, endpoint calls and response handling in `api`, API contracts in `types`, and pure shared logic in `utils`. Form fields, 2FA codes, and cleanup confirmations belong to their owning form components. Keep raw `fetch` calls out of components and do not merge these responsibilities back into a monolithic App.

### Container deployment

`docker build --tag ipa-now:local .` builds the server image using pinned Node/pnpm and Go stages, followed by an Alpine 3.23 musl runtime with CA certificates and libstdc++ for CGO SQLite and the decryption engine's dynamically loaded dependencies. The Go builder uses Alpine 3.23 with build-base; the pinned fork selects musl-compatible Unicorn libraries on Linux amd64/arm64. Run as UID/GID 10001 with a private persistent volume at `/var/lib/ipa-now`; runtime caches also live in that volume. Credential files are mounted read-only at runtime and excluded from the build context. The default documented Linux deployment uses host networking with the loopback listen address and exact Host validation. `IPA_NOW_DOMAIN` explicitly enables a proxy-reachable public/wildcard listen IP while pinning Host and mutation Origin checks to that external HTTPS hostname; it does not trust forwarded headers or provide authentication. See README for the required reverse-proxy controls, complete run command, and volume permissions. Validate Dockerfile changes with an image build and a hardware-free startup smoke check when Docker is available.

### Automated cleanup capability

The real adapter implements `engine.Cleaner` with a separate 30-second cleanup context and startup recovery under quarantine. Only explicit confirmation for every persisted attempt may release uncertain device work. Private journals live in `device-journal/` outside disposable work/input/tmp storage and are retained with job metadata and clean markers. Schema v2 adds operation ownership records; legacy active jobs without journals remain unconfirmed. `make test` includes adapter, migration, retry and cleanup regressions; canonical commands above are unchanged. Graceful shutdown allows 75 seconds for independent library and worker cleanup. See `docs/automated-cleanup.md` for recovery/retention and `docs/cleanup-fork-validation.md` for remaining device validation limits.
