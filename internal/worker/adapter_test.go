package worker

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/6ixfalls/ipa-now/internal/engine"
	"github.com/6ixfalls/ipa-now/internal/jobs"
	"github.com/6ixfalls/ipa-now/internal/secrets"
	"github.com/6ixfalls/ipa-now/internal/testutil"
	ipa "github.com/londek/ipadecrypt/pkg/ipadecrypt"
)

func TestDurableAdapterRetryAndRecovery(t *testing.T) {
	for _, recoverLater := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "recovery"}[recoverLater], func(t *testing.T) {
			w, j := setup(t)
			calls := 0
			confirmed := !recoverLater
			a := &engine.Adapter{Operations: w.Jobs, JournalDir: filepath.Join(w.Files.Root, "device-journal"), Secrets: secrets.New(w.Files.Root, "", "", "")}
			var seen []string
			a.DecryptFunc = func(ctx context.Context, r ipa.Request) (*ipa.Result, error) {
				calls++
				ops, err := w.Jobs.Operations(j.ID)
				if err != nil || len(ops) != calls || ops[len(ops)-1] != r.OperationID {
					t.Fatal("operation not durably recorded", ops, err)
				}
				seen = append(seen, r.OperationID)
				if calls == 1 {
					r.OnEvent(ipa.Event{Phase: ipa.PhaseConnecting})
					// The new library emits cleaning even after a failed connection.
					r.OnEvent(ipa.Event{Phase: ipa.PhaseCleaning})
					return nil, &net.OpError{Op: "dial", Err: &net.DNSError{IsTimeout: true}}
				}
				testutil.WriteIPA(t, r.OutputPath)
				return &ipa.Result{Installed: true, Verification: ipa.VerificationResult{Scanned: 1}}, ipa.ErrCleanupUnconfirmed
			}
			a.CleanupFunc = func(ctx context.Context, r ipa.CleanupRequest) (ipa.CleanupReport, error) {
				// First attempt was a proven connection failure and must clean before retry.
				if r.OperationID == seen[0] {
					return ipa.CleanupReport{Confirmed: true}, nil
				}
				return ipa.CleanupReport{Confirmed: confirmed, Uninstalled: confirmed}, nil
			}
			// Use the real adapter Decrypt/Cleanup boundaries, with a synthetic output verifier.
			w.Engine = adapterFixture{Adapter: a}
			if err := w.Run(context.Background(), j); err != nil {
				t.Fatal(err)
			}
			got, _ := w.Jobs.Get(j.ID)
			if calls != 2 || seen[0] == seen[1] {
				t.Fatal("retry identity", calls, seen)
			}
			if recoverLater {
				if got.State != jobs.Cleaning || got.PendingState != jobs.Completed {
					t.Fatal(got)
				}
				confirmed = true
				if err := w.Recover(); err != nil {
					t.Fatal(err)
				}
				got, _ = w.Jobs.Get(j.ID)
			}
			if got.State != jobs.Completed || !got.Uninstalled || got.Bytes == 0 {
				t.Fatal(got)
			}
			if calls != 2 {
				t.Fatal("decryption replayed")
			}
		})
	}
}

type adapterFixture struct{ *engine.Adapter }

func (adapterFixture) Verify(string) error { return nil }
