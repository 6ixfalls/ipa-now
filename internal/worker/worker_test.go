package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/6ixfalls/ipa-now/internal/engine"
	"github.com/6ixfalls/ipa-now/internal/jobs"
	"github.com/6ixfalls/ipa-now/internal/storage"
	"github.com/6ixfalls/ipa-now/internal/testutil"
)

type fakeEngine struct {
	run        func(context.Context, engine.Request) (engine.Result, error)
	verify     error
	verifyFunc func() error
}

func (f fakeEngine) Decrypt(c context.Context, r engine.Request) (engine.Result, error) {
	return f.run(c, r)
}
func (f fakeEngine) Verify(string) error {
	if f.verifyFunc != nil {
		return f.verifyFunc()
	}
	return f.verify
}
func setup(t *testing.T) (*Worker, jobs.Job) {
	t.Helper()
	files, e := storage.Open(filepath.Join(t.TempDir(), "data"))
	if e != nil {
		t.Fatal(e)
	}
	s, e := jobs.Open(filepath.Join(files.Root, "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close(); files.Close() })
	j := jobs.Job{ID: jobs.NewID(), Target: "com.example.app", Source: "installed"}
	if e = s.Enqueue(&j, 20); e != nil {
		t.Fatal(e)
	}
	claimed, e := s.Claim()
	if e != nil {
		t.Fatal(e)
	}
	w := &Worker{Jobs: s, Files: files, Timeout: time.Second * 3, Retention: time.Hour, MaxAttempts: 3, RetryDelay: time.Millisecond}
	return w, *claimed
}
func success(t *testing.T, review bool) fakeEngine {
	return fakeEngine{run: func(ctx context.Context, r engine.Request) (engine.Result, error) {
		testutil.WriteIPA(t, filepath.Join(r.Workspace, "output.ipa"))
		return engine.Result{NeedsReview: review}, nil
	}}
}
func TestSuccessPublicationAndRetention(t *testing.T) {
	w, j := setup(t)
	w.Engine = success(t, false)
	if e := w.Run(context.Background(), j); e != nil {
		t.Fatal(e)
	}
	got, _ := w.Jobs.Get(j.ID)
	if got.State != jobs.Completed || got.Bytes == 0 || got.SHA256 == "" {
		t.Fatal(got)
	}
	for _, kind := range []string{"work", "inputs"} {
		p, _ := w.Files.Path(kind, j.ID)
		if _, e := os.Stat(p); !os.IsNotExist(e) {
			t.Fatal("resource retained", p)
		}
	}
	past := time.Now().Add(-time.Minute)
	w.Jobs.Metadata(j.ID, map[string]any{"expires_at": past})
	for range 2 {
		if e := w.Expire(); e != nil {
			t.Fatal(e)
		}
	}
	got, _ = w.Jobs.Get(j.ID)
	if !got.ArtifactExpired {
		t.Fatal("retention not persisted")
	}
	p, _ := w.Files.Path("artifacts", j.ID)
	if _, e := os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("expired artifact retained")
	}
}

func TestSuccessBoundsPersistedAppMetadata(t *testing.T) {
	w, j := setup(t)
	w.Engine = fakeEngine{run: func(ctx context.Context, r engine.Request) (engine.Result, error) {
		testutil.WriteIPA(t, filepath.Join(r.Workspace, "output.ipa"))
		return engine.Result{BundleID: strings.Repeat("a", 300), Version: strings.Repeat("界", 30)}, nil
	}}
	if err := w.Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	got, err := w.Jobs.Get(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BundleID != strings.Repeat("a", maxBundleIDBytes) {
		t.Fatalf("bundle ID was not bounded: %d bytes", len(got.BundleID))
	}
	if got.Version != strings.Repeat("界", 21) {
		t.Fatalf("version was not bounded on a UTF-8 boundary: %q", got.Version)
	}
}

func TestQuarantineBlocksDeviceUntilConfirmed(t *testing.T) {
	w, j := setup(t)
	w.Engine = success(t, true)
	next := jobs.Job{ID: jobs.NewID(), Target: "com.example.next", Source: "installed"}
	w.Jobs.Enqueue(&next, 20)
	if e := w.Run(context.Background(), j); e != nil {
		t.Fatal(e)
	}
	got, _ := w.Jobs.Get(j.ID)
	if got.State != jobs.Cleaning || got.PendingState != jobs.Completed || got.CleanupError == "" {
		t.Fatal(got)
	}
	if n, e := w.Jobs.Claim(); e != nil || n != nil {
		t.Fatal("quarantined device claimed", n, e)
	}
	if e := w.Resolve(j.ID); e != nil {
		t.Fatal(e)
	}
	got, _ = w.Jobs.Get(j.ID)
	if got.State != jobs.Completed {
		t.Fatal(got)
	}
	if n, e := w.Jobs.Claim(); e != nil || n == nil || n.ID != next.ID {
		t.Fatal(n, e)
	}
}
func TestVerificationFailureNeverPublishes(t *testing.T) {
	w, j := setup(t)
	f := success(t, false)
	f.verify = errors.New("failed verification")
	w.Engine = f
	if e := w.Run(context.Background(), j); e != nil {
		t.Fatal(e)
	}
	got, _ := w.Jobs.Get(j.ID)
	if got.State != jobs.Failed || got.ErrorCode != "verification_failed" {
		t.Fatal(got)
	}
	p, _ := w.Files.Path("artifacts", j.ID)
	if _, e := os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("failed artifact published")
	}
}
func TestRetryOnlyClassifiedSafeFailures(t *testing.T) {
	for _, safe := range []bool{true, false} {
		t.Run(map[bool]string{true: "safe", false: "uncertain"}[safe], func(t *testing.T) {
			w, j := setup(t)
			var calls int
			w.Engine = fakeEngine{run: func(ctx context.Context, r engine.Request) (engine.Result, error) {
				calls++
				return engine.Result{}, &engine.Failure{Code: "device_unavailable", Retryable: true, NeedsReview: !safe, Cause: errors.New("password=secret token=secret device=private")}
			}}
			if e := w.Run(context.Background(), j); e != nil {
				t.Fatal(e)
			}
			want := 1
			if safe {
				want = 3
			}
			if calls != want {
				t.Fatalf("calls=%d want %d", calls, want)
			}
			got, _ := w.Jobs.Get(j.ID)
			if got.Attempts != want {
				t.Fatal(got)
			}
			if safe && got.State != jobs.Failed {
				t.Fatal(got)
			}
			if !safe && got.State != jobs.Cleaning {
				t.Fatal(got)
			}
			if got.FailureReason != "device_unavailable" {
				t.Fatal("raw error persisted")
			}
		})
	}
}
func TestCancellationAndCallbackBackpressure(t *testing.T) {
	w, j := setup(t)
	started := make(chan struct{})
	var elapsed atomic.Int64
	w.Engine = fakeEngine{run: func(ctx context.Context, r engine.Request) (engine.Result, error) {
		start := time.Now()
		for range 100000 {
			r.Progress(engine.Progress{Phase: "decrypting", Current: 10, Total: 100})
		}
		elapsed.Store(int64(time.Since(start)))
		close(started)
		<-ctx.Done()
		return engine.Result{}, &engine.Failure{Code: "interrupted", NeedsReview: true, Cause: ctx.Err()}
	}}
	done := make(chan error, 1)
	go func() { done <- w.Run(context.Background(), j) }()
	<-started
	if time.Duration(elapsed.Load()) > time.Second {
		t.Fatal("callbacks blocked decryption")
	}
	if e := w.Jobs.Cancel(j.ID); e != nil {
		t.Fatal(e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	got, _ := w.Jobs.Get(j.ID)
	if got.State != jobs.Cleaning || got.PendingState != jobs.Cancelled {
		t.Fatal(got)
	}
	if e := w.Resolve(j.ID); e != nil {
		t.Fatal(e)
	}
	got, _ = w.Jobs.Get(j.ID)
	if got.State != jobs.Cancelled {
		t.Fatal(got)
	}
}
func TestStartupReconcilesPartialAndOrphanFiles(t *testing.T) {
	w, j := setup(t)
	workspace, e := w.Files.Workspace(j.ID)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(workspace, "partial.ipa"), []byte("partial"), 0600)
	orphan := jobs.NewID()
	p, _ := w.Files.Path("inputs", orphan)
	os.WriteFile(p, []byte("orphan"), 0600)
	if e = w.Recover(); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{workspace, p} {
		if _, e = os.Stat(p); !os.IsNotExist(e) {
			t.Fatal("abandoned resource retained", p)
		}
	}
	d, _ := w.Jobs.Device()
	if !d.Quarantined {
		t.Fatal("device not quarantined after restart")
	}
	if e = w.Recover(); e != nil {
		t.Fatal("recovery was not idempotent", e)
	}
}
func TestQueuedCancellationSkipsEngine(t *testing.T) {
	w, j := setup(t)
	j.CancelRequested = true
	w.Engine = fakeEngine{run: func(context.Context, engine.Request) (engine.Result, error) {
		t.Fatal("cancelled job executed")
		return engine.Result{}, nil
	}}
	if e := w.Run(context.Background(), j); e != nil {
		t.Fatal(e)
	}
	got, _ := w.Jobs.Get(j.ID)
	if got.State != jobs.Cancelled {
		t.Fatal(got)
	}
}

func TestFailureDetailsAreLoggedRedacted(t *testing.T) {
	w, j := setup(t)
	var buf bytes.Buffer
	w.Log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	w.Engine = fakeEngine{run: func(context.Context, engine.Request) (engine.Result, error) {
		return engine.Result{}, &engine.Failure{Code: "authentication_failed", Cause: errors.New("gsa login rejected password=hunter2 token=abc123")}
	}}
	if e := w.Run(context.Background(), j); e != nil {
		t.Fatal(e)
	}
	got, _ := w.Jobs.Get(j.ID)
	if got.State != jobs.Failed || got.ErrorCode != "authentication_failed" {
		t.Fatal(got)
	}
	out := buf.String()
	for _, want := range []string{"job failed", "authentication_failed", "gsa login rejected", "[redacted]", "job started"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log output missing %q:\n%s", want, out)
		}
	}
	for _, leaked := range []string{"hunter2", "abc123"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("log output leaked credential %q:\n%s", leaked, out)
		}
	}
	if got.FailureReason != "authentication_failed" {
		t.Fatal("raw error persisted")
	}
}

func TestCancelDuringVerificationRemovesPublishedArtifact(t *testing.T) {
	w, j := setup(t)
	f := success(t, false)
	f.verifyFunc = func() error { return w.Jobs.Cancel(j.ID) }
	w.Engine = f
	if e := w.Run(context.Background(), j); e != nil {
		t.Fatal(e)
	}
	got, _ := w.Jobs.Get(j.ID)
	if got.State != jobs.Cancelled {
		t.Fatal("verification cancellation was lost", got)
	}
	p, _ := w.Files.Path("artifacts", j.ID)
	if _, e := os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("cancelled artifact retained")
	}
}
func TestTimeoutDuringVerificationFails(t *testing.T) {
	w, j := setup(t)
	w.Timeout = 10 * time.Millisecond
	f := success(t, false)
	f.verifyFunc = func() error { time.Sleep(20 * time.Millisecond); return nil }
	w.Engine = f
	if e := w.Run(context.Background(), j); e != nil {
		t.Fatal(e)
	}
	got, _ := w.Jobs.Get(j.ID)
	if got.State != jobs.Failed || got.ErrorCode != "timeout" {
		t.Fatal("verification deadline was lost", got)
	}
}
func TestCleanupFailureIsQuarantinedAndRetryable(t *testing.T) {
	w, j := setup(t)
	w.Engine = success(t, false)
	p, _ := w.Files.Path("inputs", j.ID)
	os.Mkdir(p, 0700)
	child := filepath.Join(p, "blocking-entry")
	os.WriteFile(child, []byte("owned test fixture"), 0600)
	if e := w.Run(context.Background(), j); e != nil {
		t.Fatal(e)
	}
	got, _ := w.Jobs.Get(j.ID)
	if got.State != jobs.Cleaning || got.CleanupError == "" {
		t.Fatal("cleanup error ignored", got)
	}
	if e := w.Resolve(j.ID); e == nil {
		t.Fatal("failed cleanup was confirmed")
	}
	os.Remove(child)
	if e := w.Resolve(j.ID); e != nil {
		t.Fatal(e)
	}
	got, _ = w.Jobs.Get(j.ID)
	if got.State != jobs.Completed {
		t.Fatal(got)
	}
}
