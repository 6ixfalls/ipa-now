package scheduler

import (
	"context"
	"errors"
	"github.com/6ixfalls/ipa-now/internal/jobs"
	"path/filepath"
	"sync/atomic"
	"testing"
)

type testRunner struct {
	store    *jobs.Store
	active   atomic.Int32
	calls    atomic.Int32
	max      atomic.Int32
	finished chan struct{}
	fail     bool
}

func (r *testRunner) Expire() error { return nil }
func (r *testRunner) Run(ctx context.Context, j jobs.Job) error {
	n := r.active.Add(1)
	defer r.active.Add(-1)
	if n > r.max.Load() {
		r.max.Store(n)
	}
	defer r.store.Release(j.ID)
	if r.fail {
		return errors.New("persistence failed")
	}
	if e := r.store.Change(j.ID, jobs.Cleaning, nil); e != nil {
		return e
	}
	if e := r.store.Change(j.ID, jobs.Completed, nil); e != nil {
		return e
	}
	if r.calls.Add(1) == 3 {
		close(r.finished)
	}
	return nil
}
func TestSerialAdmissionAndStopOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "fail-closed"
		}
		t.Run(name, func(t *testing.T) {
			s, e := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			for range 3 {
				if e = s.Enqueue(&jobs.Job{ID: jobs.NewID()}, 10); e != nil {
					t.Fatal(e)
				}
			}
			runner := &testRunner{store: s, finished: make(chan struct{}), fail: fail}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- Run(ctx, s, runner) }()
			if fail {
				if e = <-done; e == nil {
					t.Fatal("scheduler ignored failure")
				}
				all, _ := s.List()
				queued := 0
				for _, j := range all {
					if j.State == jobs.Queued {
						queued++
					}
				}
				if queued != 2 {
					t.Fatal("work continued after failure")
				}
			} else {
				<-runner.finished
				cancel()
				if e = <-done; e != nil {
					t.Fatal(e)
				}
				if runner.max.Load() != 1 || runner.calls.Load() != 3 {
					t.Fatal("device concurrency was not serialized")
				}
			}
		})
	}
}
