package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/6ixfalls/ipa-now/internal/engine"
	"github.com/6ixfalls/ipa-now/internal/jobs"
)

type cleanupEngine struct {
	fakeEngine
	cleanup func(context.Context, string) (engine.CleanupResult, error)
}

func (f cleanupEngine) Cleanup(ctx context.Context, id string) (engine.CleanupResult, error) {
	return f.cleanup(ctx, id)
}

func TestAutomaticCleanupOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		confirmed bool
		err       error
		want      jobs.State
	}{
		{"confirmed", true, nil, jobs.Completed},
		{"unknown", false, nil, jobs.Cleaning},
		{"failed", true, errors.New("password=secret"), jobs.Cleaning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, j := setup(t)
			next := jobs.Job{ID: jobs.NewID(), Target: "com.example.next", Source: "installed"}
			if err := w.Jobs.Enqueue(&next, 20); err != nil {
				t.Fatal(err)
			}
			calls := 0
			w.Engine = cleanupEngine{fakeEngine: success(t, true), cleanup: func(ctx context.Context, id string) (engine.CleanupResult, error) {
				calls++
				if id != j.ID || ctx.Err() != nil {
					t.Fatal("invalid cleanup context or job")
				}
				current, err := w.Jobs.Get(id)
				if err != nil || current.State != jobs.Cleaning {
					t.Fatal(current, err)
				}
				device, err := w.Jobs.Device()
				if err != nil || device.Owner != id {
					t.Fatal("cleanup lost device lease", device, err)
				}
				if claimed, err := w.Jobs.Claim(); err != nil || claimed != nil {
					t.Fatal("concurrent claim", claimed, err)
				}
				return engine.CleanupResult{Confirmed: tc.confirmed}, tc.err
			}}
			if err := w.Run(context.Background(), j); err != nil {
				t.Fatal(err)
			}
			got, _ := w.Jobs.Get(j.ID)
			if got.State != tc.want || calls != 1 {
				t.Fatal(got, calls)
			}
			claimed, err := w.Jobs.Claim()
			if err != nil || (claimed != nil) != (tc.want == jobs.Completed) {
				t.Fatal(claimed, err)
			}
		})
	}
}

func TestAutomaticCleanupAfterCancelledExecution(t *testing.T) {
	w, j := setup(t)
	parent, cancel := context.WithCancel(context.Background())
	w.Engine = cleanupEngine{fakeEngine: fakeEngine{run: func(ctx context.Context, r engine.Request) (engine.Result, error) {
		if err := w.Jobs.Cancel(j.ID); err != nil {
			t.Fatal(err)
		}
		cancel()
		return engine.Result{}, &engine.Failure{Code: "interrupted", NeedsReview: true, Cause: ctx.Err()}
	}}, cleanup: func(ctx context.Context, id string) (engine.CleanupResult, error) {
		if ctx.Err() != nil {
			t.Fatal("job cancellation reached cleanup")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("cleanup has no deadline")
		}
		return engine.CleanupResult{Confirmed: true}, nil
	}}
	if err := w.Run(parent, j); err != nil {
		t.Fatal(err)
	}
	got, _ := w.Jobs.Get(j.ID)
	if got.State != jobs.Cancelled {
		t.Fatal(got)
	}
}

func TestAutomaticCleanupTimeoutQuarantines(t *testing.T) {
	w, j := setup(t)
	w.CleanupTimeout = time.Millisecond
	w.Engine = cleanupEngine{fakeEngine: success(t, true), cleanup: func(ctx context.Context, id string) (engine.CleanupResult, error) {
		<-ctx.Done()
		return engine.CleanupResult{Confirmed: true}, nil
	}}
	if err := w.Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	got, _ := w.Jobs.Get(j.ID)
	if got.State != jobs.Cleaning {
		t.Fatal(got)
	}
}

func TestAutomaticRecoveryDoesNotReplayDecryption(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "uncertain", true: "confirmed"}[confirmed], func(t *testing.T) {
			w, j := setup(t)
			calls := 0
			w.Engine = cleanupEngine{fakeEngine: fakeEngine{run: func(context.Context, engine.Request) (engine.Result, error) {
				t.Fatal("replayed decryption")
				return engine.Result{}, nil
			}}, cleanup: func(ctx context.Context, id string) (engine.CleanupResult, error) {
				calls++
				if id != j.ID {
					t.Fatal(id)
				}
				d, err := w.Jobs.Device()
				if err != nil || !d.Quarantined {
					t.Fatal("recovery not exclusive", d, err)
				}
				return engine.CleanupResult{Confirmed: confirmed}, nil
			}}
			for range 2 {
				if err := w.Recover(); err != nil {
					t.Fatal(err)
				}
			}
			got, _ := w.Jobs.Get(j.ID)
			d, _ := w.Jobs.Device()
			if confirmed {
				if got.State != jobs.Failed || got.Phase != "automatically confirmed cleanup" || d.Quarantined || calls != 1 {
					t.Fatal(got, d, calls)
				}
			} else if got.State != jobs.Cleaning || !d.Quarantined || calls != 2 {
				t.Fatal(got, d, calls)
			}
		})
	}
}

func TestAutomaticCleanupAfterVerificationFailure(t *testing.T) {
	w, j := setup(t)
	f := success(t, true)
	f.verify = errors.New("invalid archive")
	calls := 0
	w.Engine = cleanupEngine{fakeEngine: f, cleanup: func(context.Context, string) (engine.CleanupResult, error) {
		calls++
		return engine.CleanupResult{Confirmed: true}, nil
	}}
	if err := w.Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	got, _ := w.Jobs.Get(j.ID)
	if calls != 1 || got.State != jobs.Failed || got.ErrorCode != "verification_failed" {
		t.Fatal(got, calls)
	}
}

func TestRecoveryPreservesCompletedIntentAndExpiry(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "available", true: "expired"}[expired], func(t *testing.T) {
			w, j := setup(t)
			w.Engine = success(t, true)
			if err := w.Run(context.Background(), j); err != nil {
				t.Fatal(err)
			}
			if expired {
				if err := w.Jobs.Metadata(j.ID, map[string]any{"expires_at": time.Now().Add(-time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			w.Engine = cleanupEngine{cleanup: func(context.Context, string) (engine.CleanupResult, error) {
				return engine.CleanupResult{Confirmed: true}, nil
			}}
			if err := w.Recover(); err != nil {
				t.Fatal(err)
			}
			got, _ := w.Jobs.Get(j.ID)
			want := jobs.Completed
			if expired {
				want = jobs.Failed
			}
			if got.State != want || got.ArtifactExpired != expired {
				t.Fatal(got)
			}
		})
	}
}

func TestCleanupLogging(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		confirmed  bool
		err        error
		timeout    bool
	}{
		{name: "confirmed", want: "automatic device cleanup confirmed", confirmed: true},
		{name: "incomplete", want: "incomplete_report"},
		{name: "failed", want: "automatic device cleanup failed", err: errors.New("password=hidden-password token=hidden-token")},
		{name: "timeout", want: "deadline_exceeded", timeout: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, j := setup(t)
			var output bytes.Buffer
			w.Log = slog.New(slog.NewJSONHandler(&output, nil))
			w.CleanupTimeout = time.Millisecond
			w.Engine = cleanupEngine{cleanup: func(ctx context.Context, id string) (engine.CleanupResult, error) {
				if tc.timeout {
					<-ctx.Done()
				}
				return engine.CleanupResult{Confirmed: tc.confirmed}, tc.err
			}}
			confirmed := w.cleanupDevice(j.ID)
			if confirmed != tc.confirmed {
				t.Fatal(confirmed)
			}
			logs := output.String()
			for _, want := range []string{"automatic device cleanup started", tc.want, j.ID, "duration"} {
				if !strings.Contains(logs, want) {
					t.Fatalf("missing %q: %s", want, logs)
				}
			}
			for _, secret := range []string{"hidden-password", "hidden-token"} {
				if strings.Contains(logs, secret) {
					t.Fatal("cleanup log exposed credentials")
				}
			}
		})
	}
}
