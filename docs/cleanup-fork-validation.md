# Cleanup fork validation

Historical pre-integration review. The adapter/pin and schema integration described as outstanding below have now been implemented; see [current workflow](automated-cleanup.md). Hardware validation remains outstanding; the later pinned revision includes the upstream lint fix.

Reviewed fork commit: `7a3e6f73be47c1b28c032af216056416f3c8705d` (`feat: replace appinst with helper for durable cleanup`), compared with the current pin `fbe5e07bbc7104bc85917cbd98a77201c50f89c8`.

## Result

The requested public API and the core durable-cleanup mechanism are implemented. Hardware-free tests and ipa-now compatibility checks pass. The fork's configured lint check fails with 12 whitespace violations. Real-device correctness has not been validated, and ipa-now's production dependency/adapter remain unchanged. This is not approval to remove the real-device review gate yet.

The reviewed implementation adds `Request.OperationID`, `Request.JournalDir`, public `Cleanup(context.Context, CleanupRequest)`, and `CleanupReport{Confirmed, Uninstalled}`. Journals reserve single-use IDs, record mutation intent before dependent device actions, sync atomic updates, bind device identity, and retain clean markers for replay. Cleanup uses a fresh SSH connection/context, checks helper quiescence, verifies installation identity and uninstall/preservation postconditions, and removes operation-owned staging. Missing/invalid journals remain unconfirmed.

The helper uses a private operation directory, exclusive/no-follow file primitives, an operation lock, a permanent seal against delayed install/decrypt launches, and completion receipts. A busy or hard-killed helper without the required receipt remains unconfirmed; automatic termination of arbitrary interrupted processes is intentionally not implemented. This conservative intervention path is consistent with automation unless an issue requires intervention.

Durable installation now calls LaunchServices from the owned helper instead of appinst. That avoids appinst's shared temporary storage but introduces a device compatibility requirement: private installation/uninstallation selectors, signing entitlements, and jailbreak permissions must be exercised on the target device. No physical device or Apple account was used during this review.

## Validation evidence

- `go test ./...`: passed in the fork checkout, including the compiled native C operation-protocol test.
- `go test -race ./pkg/ipadecrypt ./internal/device`: passed.
- `go vet ./...` and `go build ./...`: passed.
- `./helper/build.sh`: passed using the available Docker toolchain. `cmp` confirmed the rebuilt and embedded helper are byte-identical. SHA-256: `c8b0d0c48a240b07ddf7c328ab77eee278a95b302d07c4b9e3a8d90ed7bbeed5`.
- CI-pinned `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run`: failed with 12 `wsl_v5` whitespace violations. The machine's default golangci-lint is v1 and cannot read the fork's v2 configuration; the listed failures are from the correct v2.12.2 tool.
- ipa-now `make check` with a temporary modfile replacing the library with this fork checkout: passed formatting, static analysis, race tests, React tests, hardware-free end-to-end tests, and build. This demonstrates compatibility with the existing adapter and fake cleaner tests, not actual execution of the new phone cleanup path.

Lint locations in the reviewed commit: `pkg/ipadecrypt/cleanup.go` lines 223, 402, 407, 412; `pkg/ipadecrypt/cleanup_test.go` lines 143, 336, 341, 347, 363, 367, 382, 383. Add the required blank lines and rerun the CI-pinned linter.

## Remaining integration work

1. Fix the fork lint failures and validate the intended success, cancellation, disconnect, failed install/uninstall, and restart scenarios on an explicitly authorized device. Recheck the final commit before pinning it.
2. Add a dedicated private persistent journal directory outside ipa-now's disposable work/input/tmp roots and wire `Adapter.Cleanup` to the public API. Keep missing legacy journals quarantined.
3. Allocate and durably associate a unique operation ID with each decryption attempt. The fork reserves an ID even on a connection failure and rejects reuse; ipa-now's current retry loop passes the same job ID each time. A direct `OperationID: r.ID` mapping would break safe retries. Recovery must account for every attempt's journal.
4. Preserve structured partial result information when `Decrypt` returns a result alongside a cleanup error, and map `CleanupReport.Uninstalled` into job metadata. Confirmation of cleanup must remain separate from artifact verification and the original execution outcome.
5. Define journal retention and clean-marker deletion after terminal persistence, update configuration/storage documentation, and test the real adapter boundary with the new API. Existing `engine.Cleaner` orchestration tests do not replace these tests.

No dependency files or runtime source were changed by this validation. The fork was inspected in `/private/tmp/ipadecrypt-cleanup-review`; the temporary ipa-now override was `/private/tmp/ipa-now-cleanup-review.mod`.
