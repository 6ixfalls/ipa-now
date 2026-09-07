# ipa-now

A private, self-hosted workspace for decrypting iOS apps you are legally entitled to obtain and decrypt. Go runs the HTTP API, a SQLite/GORM durable queue, one device worker, and the compiled React UI in one process.

**Current integration limitation:** the pinned ipadecrypt fork cannot report or recover every device-side resource. Real-device jobs pause in `cleaning` for explicit operator review, including successful decryptions. The queue remains paused and downloads remain unavailable until the operator confirms cleanup. This is intentional; a successful library return is not sufficient proof of cleanup. See [the integration review](docs/ipadecrypt.md).

## Requirements

- Go **1.26.5**, a C compiler and CGO enabled (GORM's SQLite driver uses `go-sqlite3`).
- Node **22.15.0** and pnpm **11.24.0**. The frontend uses React, TypeScript, and Vite; no frontend server is needed in production.
- Linux or macOS, local storage supporting SQLite WAL, file locking, atomic renames, and directory fsync. Do not use network storage.
- One dedicated compatible jailbroken iOS device with SSH, the fork's required tools, and a manually verified host key. Upload/App Store installation requires `appinst`. The default installed-app workflow preserves the existing installation.
- An Apple account only for App Store requests. Passwords and refreshed sessions stay server-side.

Only deploy on a trusted private network. There is no application authentication. Do not expose this service, its development server, or its data directory to the public Internet.

## Setup

```sh
corepack enable
corepack prepare pnpm@11.24.0 --activate
make setup
cp .env.example .env
```

Edit `.env` with your private configuration. It is ignored by Git. The app does not load dotenv files automatically. Export the configuration in your shell without printing it:

```sh
set -a
. ./.env
set +a
make build
./bin/ipa-now
```

Open [the local workspace](http://127.0.0.1:8080). If the listen IP changes, open that exact configured address; the server validates the Host header. Public and wildcard listen addresses are rejected. Backend secrets and paths are never included in the React bundle.

Use `chmod 600` on the dedicated SSH key and known-hosts file. The data directory is created with mode `0700`; an existing directory with broader permissions is rejected. Run the process as a dedicated unprivileged OS user. Never share a device with another ipa-now instance, upstream CLI, or decryption service, even if each service uses a different data directory.

For host enrollment, obtain the SSH host public key and fingerprint through a trusted device console or another independently verified channel. Review the fingerprint, then add the matching key to the dedicated known-hosts file (use `[host]:port` for nondefault ports). `ssh-keyscan` output alone does not establish trust. The app never automatically enrolls new keys and rejects changed keys.

## Using the workspace

1. Choose **Installed app**, **App Store**, or **Upload IPA**. Confirm that you are entitled to obtain and decrypt the app.
2. Installed-app requests accept a bundle ID and preserve the installed app. App Store and upload requests additionally require acknowledgement that the build may replace an installed app and be automatically uninstalled. The previous build is not restored.
3. Follow persisted job phases in the queue. A request survives closing the browser or disconnecting its HTTP request. Submit an Apple 2FA code in the active job's prompt if requested; codes exist only in memory for that challenge.
4. On cancellation, timeout, failure, or success, the worker attempts cleanup. The pinned real engine requires a device review. Follow [the exact review steps](docs/ipadecrypt.md#operator-cleanup-review), then confirm cleanup in the job details.
5. Download the verified IPA after the job is `completed`. The default retention is 24 hours from verification, including time waiting for cleanup review. Download links are private API routes, not permanent public URLs.

A stopped or crashed job is not blindly replayed. Restart reconciles server files and quarantines uncertain device work before claiming the next job. After reviewing cleanup, failed/cancelled jobs can be requested again explicitly.

## Commands

| Command | Purpose |
| --- | --- |
| `make setup` | Download locked Go dependencies and install frontend dependencies using pnpm's frozen lockfile |
| `make dev` | Run the Go server using exported environment variables |
| `make dev-web` | Run Vite at `127.0.0.1:5173`, proxying API requests to `127.0.0.1:8080` |
| `make fmt` | Format Go with gofmt and frontend code with Biome |
| `make lint` | Verify formatting, run `go vet`, Biome lint/import checks, and TypeScript type-checking |
| `make test` | Run race-enabled Go tests and React/Vitest tests; no device or account required |
| `make e2e` | Exercise HTTP → SQLite → worker → cleanup → download flows with synthetic IPA fixtures |
| `make frontend` | Build React assets for embedding in Go |
| `make build` | Build frontend and `bin/ipa-now` |
| `make check` | Run formatting, static analysis, tests, end-to-end flows, and production build |
| `make migrate` | Explain the startup migration procedure; schema v1 initializes transactionally on startup |
| `make test-device` | Explicit real-device integration test; requires `IPA_NOW_INTEGRATION_TARGET` and normal device configuration |

For frontend development, run `make dev` and `make dev-web` in separate terminals, then use [Vite](http://127.0.0.1:5173). Keep the default backend address for the development proxy. It only rewrites the exact development origin; arbitrary origins are rejected.

The real-device test operates on an installed app you are entitled to decrypt, keeps the durable job in the configured data directory, and leaves device cleanup confirmation to the operator. Stop the server first. Do not run it against a busy device. Start the service afterward to inspect and confirm cleanup. It is excluded from the default suite and CI.

## Architecture

| Package | Responsibility |
| --- | --- |
| `internal/config` | Parse/validate prefixed environment configuration once |
| `internal/httpapi` | Explicit JSON contract, bounded uploads, operator actions, private downloads |
| `internal/jobs` | GORM models, SQLite transactions, monotonic transitions, cancellation intent, singleton device ownership |
| `internal/scheduler` | Serial admission to the one configured device |
| `internal/worker` | Job context, workspace ownership, retries, verification, cleanup, retention, reconciliation |
| `internal/engine` | Public ipadecrypt adapter and job-bound App Store HTTP transport |
| `internal/storage` | Private directories, process lock, archive validation, atomic artifact files |
| `internal/secrets` | Atomic refreshed-account storage and in-memory 2FA broker |
| `internal/web` / `web` | Embedded assets and the React application |

The scheduler owns a persisted device lease in SQLite and the process holds an exclusive OS lock on the data directory. This is the single-process equivalent of a renewable distributed lease: ownership lasts until the process exits; restart reconciles abandoned ownership before any new claim. It cannot expire beneath a live decryption and allow overlapping work. An in-process failure stops the scheduler rather than continuing on uncertain persistence.

State transitions are `queued → running → verifying → cleaning → completed`. Failure/cancellation branches enter `cleaning` before a terminal state. Safe connection retries stay within the same `running` job and device lease, increment a persisted attempt counter, and wait with bounded exponential backoff (1, 2, 4, 8 seconds). Unknown errors, authentication errors, verification failures, and any error after device contact do not automatically retry. Accepted cancellation wins until the atomic transition to cleaning.

Progress callbacks coalesce into a one-element channel and persist at most four times per second. Raw helper messages/attributes are discarded. The database stores safe phase/counter snapshots and error codes, not callback logs or secret-bearing library error strings. Typed adapter errors retain wrapped causes only in memory; durable diagnostics contain the operation/code and job ID.

The application starts an HTTP server, one scheduler goroutine, and a bounded per-job progress/cancellation monitor. The library also owns its SSH cancellation watcher. It makes Apple HTTPS and device SSH/SFTP requests through the public package; ipa-now does not spawn the ipadecrypt CLI or import internal packages. The library uploads/runs its helper, installs/uninstalls when policy allows, and writes device staging files. See [the reviewed limitations](docs/ipadecrypt.md) before operating a device.

## Storage, retention, and recovery

```text
data/                      0700, private to the service user
  service.lock             exclusive OS lock for service lifetime
  jobs.db[-wal,-shm]        GORM/SQLite state; private regular files
  secrets/account.json     0600, refreshed Apple session (password omitted)
  inputs/<opaque-id>       0600, validated uploaded IPA until job cleanup
  work/<opaque-id>/         0700, job-scoped state/cookies/output
  tmp/                     0700, library-owned patched-IPAs; reconciled on restart
  artifacts/<opaque-id>    0600, verified output, gated by completed state
```

Inputs stay while queued and are deleted when cleanup runs. Encrypted downloads, cookie jars, and temporary workspaces are deleted on success, failure, cancellation, and restart recovery. Failed output is removed. Verified output waiting for device review is retained only until the artifact expiry. No raw device logs or failed IPA binaries are retained. The library also caches validated SAP/Unicorn runtime dependencies in the service user’s OS cache under `ipatool` (for example `~/Library/Caches/ipatool` on macOS). These reusable dependency files are retained across jobs and upgrades; they are separate from account sessions and IPA retention. Run under a dedicated service user, and manage these dependency caches during stopped-service maintenance. Job metadata is retained for operator history; the UI/API lists the latest 200 jobs.

Artifacts are written inside the job workspace, verified, fsynced, and renamed atomically. Until server cleanup and any device review finish, the HTTP API refuses to serve them. Orphan inputs, abandoned workspaces, and artifacts not owned by an eligible job are reconciled at startup. Unknown filenames stop recovery for inspection instead of triggering broad deletion. A filesystem cleanup error also quarantines the device. Resolve filesystem access and restart, then review device cleanup before confirming.

Back up the entire data directory **with the service stopped**, including its database and account session, using private/encrypted backup storage. Schema v1 is created/migrated in a transaction under the process lock. A database with a newer schema version is rejected. Future schema changes must supply explicit migrations and a recovery story; do not downgrade a database in place.

See [.env.example](.env.example) for every supported setting, defaults, bounds, and secret classification. See [API contract](docs/api.md) for all externally reachable routes.


## Frontend organization

`web/src/App.tsx` composes the workspace and owns only the selected job and queue filter. Keep new behavior in the smallest relevant module:

- `components/layout`: workspace shell and sidebar.
- `components/workspace`: status overview and operator notifications.
- `components/requests`: request form and its local input state.
- `components/jobs`: queue rows, empty state, progress, details, 2FA, and cleanup review. Job-specific forms reset when the selected job changes.
- `components/ui`: shared presentation primitives.
- `hooks`: polling/cancellation of pending reads and shared async action feedback.
- `api`: typed endpoint functions, upload serialization, and response/error handling. Components must not call `fetch` directly.
- `types`: the browser-visible API contract; never add credentials or server paths.
- `utils`: shared job-state/filter/download rules and display formatting.

Biome is pinned as a pnpm development dependency and configured in `web/biome.json`. Recommended lint rules, formatting, and import organization run through `pnpm --dir web run lint`; warnings fail the check. `pnpm --dir web run lint:fix` applies safe fixes, and `pnpm --dir web run format` formats supported frontend files. TypeScript remains a separate `typecheck` step. `make lint` and CI's `make check` run both. Generated output and dependency directories are excluded from Biome.

## AI Disclaimer

This application has been developed with the assistance of AI tools. While the maintainer has reviewed all code for correctness and reliability, users are encouraged to exercise caution and perform their own testing when using the application. The maintainer does not assume responsibility for any issues that may arise from the use of this software.

## Docker server

Build from the repository root; the image builds React with pinned pnpm and embeds it in a CGO-enabled Go binary:

```sh
docker build --tag ipa-now:local .
```

The final Alpine 3.23 image runs as UID/GID `10001:10001`, includes CA certificates and the musl/C++ runtime, and contains no Node/Go build tools. `.dockerignore` limits the build context to source and lockfiles. Credentials are supplied only at runtime. The Go builder also uses Alpine 3.23 with CGO enabled for SQLite; the pinned ipadecrypt fork selects musl-compatible Unicorn libraries on Linux amd64/arm64.

The documented topology uses **Linux Docker Engine with host networking**. The server deliberately binds to `127.0.0.1:8080` and validates that exact Host header; ordinary bridge networking with `-p` will not expose a loopback-only container listener. Host networking preserves those checks and needs no `-p`. Other Docker environments require working host-network support; see [Docker's host-network documentation](https://docs.docker.com/engine/network/drivers/host/).

Prepare a private runtime environment file using `.env.example`. For Docker `--env-file`, use literal `NAME=value` lines without shell `export` or shell quoting. Set `IPA_NOW_KNOWN_HOSTS_PATH=/run/ipa-now/known_hosts` and, for key authentication, `IPA_NOW_SSH_KEY_PATH=/run/ipa-now/id_ed25519`. The host credential directory and files must be readable by UID 10001, with files mode `0600`; verify and enroll the device host key before starting the container.

```sh
docker volume create ipa-now-data
docker run --detach --name ipa-now \
  --network host \
  --env-file /absolute/private/path/ipa-now.env \
  --env IPA_NOW_DATA_DIR=/var/lib/ipa-now \
  --env IPA_NOW_LISTEN=127.0.0.1:8080 \
  --mount type=volume,source=ipa-now-data,target=/var/lib/ipa-now \
  --mount type=bind,source=/absolute/private/path/device-credentials,target=/run/ipa-now,readonly \
  --read-only --cap-drop ALL --security-opt no-new-privileges \
  --stop-timeout 30 \
  ipa-now:local
```

A fresh named volume inherits the image directory's ownership and mode. Existing volumes or bind-mounted data directories must be owned by UID/GID 10001 and have mode `0700`. The data volume retains SQLite, inputs, artifacts, account sessions, private temporary files, and the engine's runtime cache. Do not mount it with `noexec`: the library loads runtime dependencies from that cache. One container may own a data directory and device at a time.

Open [the local workspace](http://127.0.0.1:8080). Stop with `docker stop --time 30 ipa-now` so cancellation and cleanup can run before termination. The existing device-cleanup review gate still applies. No device credentials or real-device jobs are needed to build the image.
