package jobs

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestTransitions(t *testing.T) {
	states := []State{Queued, Running, Verifying, Cleaning, Completed, Failed, Cancelled}
	valid := map[[2]State]bool{{Queued, Running}: true, {Queued, Cleaning}: true, {Running, Verifying}: true, {Running, Cleaning}: true, {Verifying, Cleaning}: true, {Cleaning, Completed}: true, {Cleaning, Failed}: true, {Cleaning, Cancelled}: true}
	for _, a := range states {
		for _, b := range states {
			if CanTransition(a, b) != valid[[2]State{a, b}] {
				t.Fatalf("unexpected transition %s -> %s", a, b)
			}
		}
	}
}
func TestConcurrentClaimAndQueueBound(t *testing.T) {
	s := openTest(t)
	for range 10 {
		j := Job{ID: NewID(), Target: "com.example.app", Source: "installed"}
		if e := s.Enqueue(&j, 10); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.Enqueue(&Job{ID: NewID()}, 10); !errors.Is(e, ErrQueueFull) {
		t.Fatal(e)
	}
	var n atomic.Int32
	var wg sync.WaitGroup
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, e := s.Claim()
			if e != nil {
				t.Error(e)
			}
			if j != nil {
				n.Add(1)
			}
		}()
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Fatalf("claimed %d jobs", n.Load())
	}
	d, e := s.Device()
	if e != nil || d.Owner == "" {
		t.Fatal(d, e)
	}
	all, _ := s.List()
	queued := 0
	for _, j := range all {
		if j.State == Queued {
			queued++
		}
	}
	if queued != 9 {
		t.Fatal(queued)
	}
}
func TestRecoveryQuarantinesAndKeepsQueued(t *testing.T) {
	p := filepath.Join(t.TempDir(), "jobs.db")
	s, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	a := Job{ID: NewID()}
	b := Job{ID: NewID()}
	s.Enqueue(&a, 10)
	s.Enqueue(&b, 10)
	claimed, e := s.Claim()
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Recover(); e != nil {
		t.Fatal(e)
	}
	j, _ := s.Get(claimed.ID)
	if j.State != Cleaning || j.PendingState != Failed || j.CleanupError == "" {
		t.Fatal(j)
	}
	next, e := s.Claim()
	if e != nil || next != nil {
		t.Fatal(next, e)
	}
	if e = s.Resolve(j.ID); e != nil {
		t.Fatal(e)
	}
	next, e = s.Claim()
	if e != nil || next == nil || next.ID == j.ID {
		t.Fatal(next, e)
	}
	if e = s.Change(j.ID, Running, nil); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
func TestCancellationIntentAndRetryMonotonic(t *testing.T) {
	s := openTest(t)
	j := Job{ID: NewID()}
	s.Enqueue(&j, 2)
	if e := s.Cancel(j.ID); e != nil {
		t.Fatal(e)
	}
	claimed, e := s.Claim()
	if e != nil || !claimed.CancelRequested {
		t.Fatal(claimed, e)
	}
	if e = s.Retry(j.ID); e != nil {
		t.Fatal(e)
	}
	got, _ := s.Get(j.ID)
	if got.State != Running || got.Attempts != 2 {
		t.Fatal(got)
	}
	s.Change(j.ID, Cleaning, nil)
	s.Change(j.ID, Cancelled, nil)
	if e = s.Cancel(j.ID); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

func TestCancellationWinsAtCleanupBoundary(t *testing.T) {
	s := openTest(t)
	j := Job{ID: NewID()}
	s.Enqueue(&j, 1)
	s.Claim()
	s.Change(j.ID, Verifying, nil)
	if e := s.Cancel(j.ID); e != nil {
		t.Fatal(e)
	}
	desired, e := s.BeginCleanup(j.ID, Completed, "", "")
	if e != nil || desired != Cancelled {
		t.Fatal(desired, e)
	}
	if e = s.Cancel(j.ID); !errors.Is(e, ErrConflict) {
		t.Fatal("cancellation accepted after cutoff")
	}
}
func TestRejectNewerSchema(t *testing.T) {
	p := filepath.Join(t.TempDir(), "jobs.db")
	s, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.db.Exec("PRAGMA user_version = 999").Error; e != nil {
		t.Fatal(e)
	}
	s.Close()
	if other, e := Open(p); e == nil {
		other.Close()
		t.Fatal("newer database accepted")
	}
}
