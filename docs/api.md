# HTTP API v1

The browser and backend communicate only through `/api`. All responses and downloads are private to the trusted deployment. There is no application authentication. Requests from unsupported Host headers or cross-origin mutations are rejected; this is not an authorization system. When `IPA_NOW_DOMAIN` is set, its HTTPS hostname (and optional port) is the only accepted Host and mutation origin, regardless of the backend's HTTP listen address. Reverse proxies must preserve `Host`; `Forwarded` and `X-Forwarded-*` headers are not trusted.

Every mutation requires `X-IPA-Now: 1`. JSON mutations require `Content-Type: application/json`, accept at most 4096 bytes, reject unknown fields and trailing JSON, and use the exact origin of the configured service address (`https://IPA_NOW_DOMAIN` when configured, otherwise `http://IPA_NOW_LISTEN`). Errors have shape `{"code":"stable_code","message":"safe text"}`. No library error causes, credentials, host paths, device output, or raw callback attributes are returned.

| Method and route | Contract |
| --- | --- |
| `GET /api/status` | `{device:{cleanupRequired,activeJob?,reason?},authJob,appleEnabled,maxUploadBytes,queueLimit,cleanupReviewRequired}` |
| `GET /api/jobs` | Latest 200 jobs, newest first; empty array when there are none |
| `POST /api/jobs` | JSON `{target,source,entitled,allowReplacement?}`; source is `installed` or `app-store`; returns `202` with job |
| `POST /api/uploads` | Raw IPA bytes with `application/octet-stream`, `X-IPA-Entitled: true`, `X-IPA-Allow-Replacement: true`; returns `202` with job |
| `GET /api/jobs/{id}` | One job |
| `POST /api/jobs/{id}/cancel` | JSON `{}`; `202 {cancelRequested:true}`; persists cancellation until worker cleanup; cleaning/terminal jobs return `409` |
| `POST /api/jobs/{id}/auth-code` | JSON `{code:"123456"}` scoped to the active in-memory challenge; `204` |
| `POST /api/jobs/{id}/confirm-cleanup` | JSON `{confirmed:true}`; operator attests device cleanup; retries server cleanup, persists outcome, releases quarantine when all outstanding reviews are resolved; `204` |
| `GET /api/jobs/{id}/artifact` | `200` or range response only for completed, unexpired artifacts; opaque safe attachment filename; never serves partial or unconfirmed output |

IDs are server-generated 32-character lowercase hexadecimal strings. The service ignores client filenames and does not expose arbitrary local-file access. Installed source accepts a validated bundle ID. App Store source additionally accepts a positive numeric App Store ID or an `https://apps.apple.com/.../id<digits>` URL. Query strings are discarded when normalized to an ID. Other hosts, schemes, credentials in URLs, paths, and `.ipa` strings are rejected. IPA upload is the only way to submit a local file.

Uploaded archives are bounded by compressed and expanded size, entry count, and metadata size. Traversal, absolute paths, backslashes, symlinks, nonregular entries, duplicate names, ambiguous top-level apps, and unsafe app metadata are rejected before enqueueing. One upload is admitted at a time. Queue admission is transactionally bounded across queued, active, and cleanup-review jobs.

## Job response

```json
{
  "id": "0123456789abcdef0123456789abcdef",
  "target": "com.example.app",
  "source": "installed",
  "state": "queued",
  "phase": "",
  "attempts": 0,
  "cancelRequested": false,
  "installed": false,
  "replaced": false,
  "uninstalled": false,
  "current": 0,
  "total": 0,
  "bytes": 0,
  "artifactExpired": false,
  "createdAt": "2026-09-06T00:00:00Z",
  "updatedAt": "2026-09-06T00:00:00Z"
}
```

Optional fields are `errorCode`, `errorMessage`, `cleanupError`, `sha256`, and `expiresAt`. State is one of `queued`, `running`, `verifying`, `cleaning`, `completed`, `failed`, `cancelled`. `phase` is a safe persisted progress label. Transfer counts are nonnegative; `total=0` means unknown. Installation flags on failure may be unavailable, so `false` is not proof that an operation did not alter the device.

Typical errors: `400` invalid target/archive/JSON/acknowledgement, `403` origin/host denial, `404` unknown ID, `409` state conflict or unavailable artifact, `413` oversized upload, `415` wrong media type, `429` full queue or concurrent upload, `500` persistence failure. No error response includes raw upstream details.

## Example

```sh
curl http://127.0.0.1:8080/api/jobs \
  -H 'X-IPA-Now: 1' \
  -H 'Content-Type: application/json' \
  --data '{"target":"com.example.app","source":"installed","entitled":true}'
```

Closing the connection after the `202` response does not cancel the job. Poll `/api/jobs` or the individual job for persisted state. To cancel, POST `{}` to its cancellation route. For uploads, use `--data-binary @/your/private/file.ipa` with the documented upload headers and only for an IPA you are legally entitled to decrypt.
