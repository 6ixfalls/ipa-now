package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/6ixfalls/ipa-now/internal/engine"
	"github.com/6ixfalls/ipa-now/internal/jobs"
	"github.com/6ixfalls/ipa-now/internal/storage"
)

type Worker struct {
	Jobs               *jobs.Store
	Files              *storage.Store
	Engine             engine.Engine
	Timeout, Retention time.Duration
	MaxAttempts        int
	RetryDelay         time.Duration
}

func (w *Worker) Run(parent context.Context, j jobs.Job) (returnErr error) {
	// Release only after every job-owned operation and persistence write finishes.
	defer func() { returnErr = errors.Join(returnErr, w.Jobs.Release(j.ID)) }()
	if j.CancelRequested {
		return w.finish(j, jobs.Cancelled, "", false)
	}
	workspace, err := w.Files.Workspace(j.ID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, w.Timeout)
	defer cancel()
	progress := make(chan engine.Progress, 1)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	var monitorErr error
	go func() {
		defer wg.Done()
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				current, e := w.Jobs.Get(j.ID)
				if e != nil {
					monitorErr = e
					cancel()
					return
				}
				if current.CancelRequested {
					cancel()
				}
				select {
				case p := <-progress:
					if e = w.Jobs.Progress(j.ID, p.Phase, p.Current, p.Total); e != nil {
						monitorErr = e
						cancel()
						return
					}
				default:
				}
			}
		}
	}()
	emit := func(p engine.Progress) {
		select {
		case progress <- p:
		default:
			select {
			case <-progress:
			default:
			}
			select {
			case progress <- p:
			default:
			}
		}
	}
	target := j.Target
	if j.Source == "upload" {
		target, _ = w.Files.Path("inputs", j.ID)
	}
	source := j.Source
	if source == "upload" {
		source = "auto"
	}
	var result engine.Result
	for attempt := 1; attempt <= w.MaxAttempts; attempt++ {
		result, err = w.Engine.Decrypt(ctx, engine.Request{ID: j.ID, Target: target, Source: source, Workspace: workspace, Progress: emit})
		var failure *engine.Failure
		if err == nil || !errors.As(err, &failure) || !failure.Retryable || failure.NeedsReview || attempt == w.MaxAttempts || ctx.Err() != nil {
			break
		}
		if e := w.Jobs.Retry(j.ID); e != nil {
			err = e
			break
		}
		delay := w.RetryDelay
		if delay == 0 {
			delay = time.Second
		}
		timer := time.NewTimer(delay * time.Duration(1<<(attempt-1)))
		select {
		case <-ctx.Done():
			timer.Stop()
			err = ctx.Err()
		case <-timer.C:
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(done)
	wg.Wait()
	if monitorErr != nil {
		return monitorErr
	}
	current, e := w.Jobs.Get(j.ID)
	if e != nil {
		return e
	}
	desired := jobs.Completed
	code := ""
	review := result.NeedsReview
	if err != nil {
		desired = jobs.Failed
		code = "decryption_failed"
		var f *engine.Failure
		if errors.As(err, &f) {
			code = f.Code
			review = review || f.NeedsReview
		} else {
			review = true
		}
	}
	if current.CancelRequested {
		desired = jobs.Cancelled
		code = "cancelled"
	} else if ctx.Err() != nil {
		desired = jobs.Failed
		code = "timeout"
	}
	if e = w.Jobs.Metadata(j.ID, map[string]any{"installed": result.Installed, "replaced": result.Replaced, "uninstalled": result.Uninstalled}); e != nil {
		return e
	}
	if desired == jobs.Completed {
		if e = w.Jobs.Change(j.ID, jobs.Verifying, nil); e != nil {
			return e
		}
		output := filepath.Join(workspace, "output.ipa")
		if e = storage.ValidateIPA(output, 32<<30); e == nil {
			e = w.Engine.Verify(output)
		}
		if e != nil {
			desired = jobs.Failed
			code = "verification_failed"
		} else {
			size, hash, publishErr := w.Files.Publish(j.ID, output)
			if publishErr != nil {
				desired = jobs.Failed
				code = "storage_failed"
			} else {
				expiry := time.Now().Add(w.Retention)
				if e = w.Jobs.Metadata(j.ID, map[string]any{"bytes": size, "sha256": hash, "expires_at": expiry, "installed": result.Installed, "replaced": result.Replaced, "uninstalled": result.Uninstalled}); e != nil {
					return e
				}
			}
		}
	}
	if ctx.Err() != nil {
		desired = jobs.Failed
		code = "timeout"
	}
	return w.finish(j, desired, code, review)
}
func (w *Worker) finish(j jobs.Job, desired jobs.State, code string, review bool) error {
	message := ""
	if code != "" {
		message = safeMessage(code)
	}
	var err error
	desired, err = w.Jobs.BeginCleanup(j.ID, desired, code, message)
	if err != nil {
		return err
	}
	cleanupErr := errors.Join(w.Files.Remove("work", j.ID), w.Files.Remove("inputs", j.ID), w.Files.CleanupTemp())
	if desired != jobs.Completed {
		cleanupErr = errors.Join(cleanupErr, w.Files.Remove("artifacts", j.ID))
	}
	if cleanupErr != nil {
		return w.Jobs.Quarantine(j.ID, "Server cleanup failed. Resolve filesystem access and restart before confirming cleanup.", desired)
	}
	if review {
		return w.Jobs.Quarantine(j.ID, "Inspect job-owned device staging, helper files, and installed/replaced apps; confirm cleanup before continuing.", desired)
	}
	return w.Jobs.Change(j.ID, desired, map[string]any{"phase": string(desired)})
}
func safeMessage(code string) string {
	switch code {
	case "verification_failed":
		return "The output did not pass IPA verification."
	case "device_unavailable":
		return "The configured device could not be reached."
	case "authentication_failed", "account_unavailable":
		return "Apple account authentication needs operator attention."
	case "cancelled":
		return "The job was cancelled."
	case "timeout":
		return "The job exceeded its execution deadline."
	case "storage_failed":
		return "The artifact could not be stored."
	default:
		return "The job could not be completed. Inspect the device before retrying."
	}
}

// Reconcile on startup under the process lock before accepting device work.
func (w *Worker) Recover() error {
	if err := w.Jobs.Recover(); err != nil {
		return err
	}
	all, err := w.Jobs.All()
	if err != nil {
		return err
	}
	known := map[string]jobs.Job{}
	for _, j := range all {
		known[j.ID] = j
	}
	for _, kind := range []string{"work", "inputs", "artifacts"} {
		entries, e := os.ReadDir(filepath.Join(w.Files.Root, kind))
		if e != nil {
			return e
		}
		for _, entry := range entries {
			id := entry.Name()
			if !storage.ValidID(id) {
				return errors.New("unrecognized storage entry requires operator inspection")
			}
			j, exists := known[id]
			keep := exists && ((kind == "inputs" && j.State == jobs.Queued) || (kind == "artifacts" && (j.State == jobs.Completed || (j.State == jobs.Cleaning && j.PendingState == jobs.Completed))))
			if !keep {
				if e = w.Files.Remove(kind, id); e != nil {
					return e
				}
			}
		}
	}
	if err = w.Files.CleanupTemp(); err != nil {
		return err
	}
	return w.Expire()
}
func (w *Worker) Expire() error {
	all, err := w.Jobs.All()
	if err != nil {
		return err
	}
	for _, j := range all {
		if j.ExpiresAt == nil || j.ArtifactExpired || time.Now().Before(*j.ExpiresAt) {
			continue
		}
		if err = w.Files.Remove("artifacts", j.ID); err != nil {
			return err
		}
		fields := map[string]any{"artifact_expired": true}
		if j.State == jobs.Cleaning {
			fields["pending_state"] = jobs.Failed
			fields["error_code"] = "artifact_expired"
			fields["error_message"] = "Artifact retention elapsed during cleanup review."
		}
		if err = w.Jobs.Metadata(j.ID, fields); err != nil {
			return err
		}
	}
	return nil
}
func (w *Worker) Resolve(id string) error {
	j, err := w.Jobs.Get(id)
	if err != nil {
		return err
	}
	if j.State != jobs.Cleaning || j.CleanupError == "" {
		return jobs.ErrConflict
	}
	if err = errors.Join(w.Files.Remove("work", id), w.Files.Remove("inputs", id), w.Files.CleanupTemp()); err != nil {
		return err
	}
	if err = w.Expire(); err != nil {
		return err
	}
	return w.Jobs.Resolve(id)
}
