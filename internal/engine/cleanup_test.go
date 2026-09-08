package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/6ixfalls/ipa-now/internal/secrets"
	ipa "github.com/londek/ipadecrypt/pkg/ipadecrypt"
)

func TestCleanupAllAttemptsAndLegacy(t *testing.T) {
	ops := &fakeOperations{}
	a := &Adapter{Operations: ops, JournalDir: filepath.Join(t.TempDir(), "journal")}
	if r, err := a.Cleanup(context.Background(), "job"); err == nil || r.Confirmed {
		t.Fatal("legacy job confirmed")
	}
	ops.StartOperation("job")
	ops.StartOperation("job")
	calls := 0
	a.CleanupFunc = func(ctx context.Context, r ipa.CleanupRequest) (ipa.CleanupReport, error) {
		calls++
		if r.OperationID != ops.ids[calls-1] || r.JournalDir != a.JournalDir {
			t.Fatal(r)
		}
		if calls == 1 {
			return ipa.CleanupReport{}, errors.New("unconfirmed")
		}
		return ipa.CleanupReport{Confirmed: true, Uninstalled: true}, nil
	}
	r, err := a.Cleanup(context.Background(), "job")
	if err == nil || r.Confirmed || calls != 2 {
		t.Fatal(r, err, calls)
	}
}

func TestAdapterPreservesVerifiedCleanupFailure(t *testing.T) {
	for _, verified := range []bool{false, true} {
		root := t.TempDir()
		a := &Adapter{Operations: &fakeOperations{}, JournalDir: filepath.Join(root, "journal"), Secrets: secrets.New(root, "", "", "")}
		a.DecryptFunc = func(context.Context, ipa.Request) (*ipa.Result, error) {
			r := &ipa.Result{Installed: true, Reinstalled: true}
			if verified {
				r.Verification.Scanned = 1
			}
			return r, ipa.ErrCleanupUnconfirmed
		}
		r, err := a.Decrypt(context.Background(), Request{ID: "job", Workspace: root, Source: "installed"})
		if (err == nil) != verified || !r.NeedsReview || !r.Installed || !r.Replaced {
			t.Fatal(r, err)
		}
	}
}

// Exercise the actual public journal/cleanup API without SSH or Apple: invalid
// target parsing happens after journal reservation and before device mutation.
func TestPublicJournalSurvivesAdapterRestart(t *testing.T) {
	root := t.TempDir()
	ops := &fakeOperations{}
	a := &Adapter{Operations: ops, JournalDir: filepath.Join(root, "journal"), Secrets: secrets.New(root, "", "", ""), Device: ipa.DeviceConfig{Host: "unused", KnownHostsPath: "unused"}}
	for range 2 {
		_, err := a.Decrypt(context.Background(), Request{ID: "job", Source: "installed", Target: "", Workspace: root})
		if err == nil {
			t.Fatal("invalid target accepted")
		}
	}
	if len(ops.ids) != 2 || ops.ids[0] == ops.ids[1] {
		t.Fatal(ops.ids)
	}
	restarted := &Adapter{Operations: ops, JournalDir: a.JournalDir, Device: a.Device}
	for range 2 {
		r, err := restarted.Cleanup(context.Background(), "job")
		if err != nil || !r.Confirmed {
			t.Fatal(r, err)
		}
	}
}
