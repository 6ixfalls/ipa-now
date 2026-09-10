package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/6ixfalls/ipa-now/internal/engine"
	"github.com/6ixfalls/ipa-now/internal/jobs"
	"github.com/6ixfalls/ipa-now/internal/logging"
	"github.com/6ixfalls/ipa-now/internal/storage"
)

type Worker struct {
	Jobs               *jobs.Store
	Files              *storage.Store
	Engine             engine.Engine
	Timeout, Retention time.Duration
	MaxAttempts        int
	RetryDelay         time.Duration
	Log                *slog.Logger
	CleanupTimeout     time.Duration
}

func (w *Worker) log() *slog.Logger { return logging.OrDiscard(w.Log) }

func (w *Worker) Run(parent context.Context, j jobs.Job) (returnErr error) {
	started := time.Now()
	w.log().Info("job started", "job", j.ID, "target", j.Target, "source", j.Source, "attempt", j.Attempts)
	// Release only after every job-owned operation and persistence write finishes.
	defer func() { returnErr = errors.Join(returnErr, w.Jobs.Release(j.ID)) }()
	if j.CancelRequested {
		return w.finish(j, jobs.Cancelled, "", false, started)
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
		if err == nil || !errors.As(err, &failure) || !failure.Retryable || attempt == w.MaxAttempts || ctx.Err() != nil {
			break
		}
		if (failure.NeedsReview || result.NeedsReview) && !w.cleanupDevice(j.ID) {
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
		wait := delay * time.Duration(1<<(attempt-1))
		w.log().Warn("retryable attempt failed; retrying with backoff", "job", j.ID, "code", failure.Code, "attempt", attempt, "delay", wait.String(), "detail", logging.Chain(err))
		timer := time.NewTimer(wait)
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
		w.log().Error("job monitor failed", "job", j.ID, "detail", logging.Chain(monitorErr))
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
		w.log().Info("cancellation requested by operator", "job", j.ID)
		desired = jobs.Cancelled
		code = "cancelled"
	} else if ctx.Err() != nil {
		w.log().Warn("job deadline exceeded", "job", j.ID, "timeout", w.Timeout.String())
		desired = jobs.Failed
		code = "timeout"
	} else if err != nil {
		w.log().Error("job failed", "job", j.ID, "code", code, "attempts", current.Attempts, "review", review, "detail", logging.Chain(err))
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
			w.log().Error("artifact verification failed", "job", j.ID, "detail", logging.Chain(e))
			desired = jobs.Failed
			code = "verification_failed"
		} else {
			size, hash, publishErr := w.Files.Publish(j.ID, output)
			if publishErr != nil {
				w.log().Error("artifact publication failed", "job", j.ID, "detail", logging.Chain(publishErr))
				desired = jobs.Failed
				code = "storage_failed"
			} else {
				expiry := time.Now().Add(w.Retention)
				if e = w.Jobs.Metadata(j.ID, map[string]any{"bytes": size, "sha256": hash, "expires_at": expiry, "installed": result.Installed, "replaced": result.Replaced, "uninstalled": result.Uninstalled}); e != nil {
					return e
				}
				w.log().Info("artifact verified and published", "job", j.ID, "bytes", size, "expires_at", expiry.Format(time.RFC3339))
			}
		}
	}
	if ctx.Err() != nil {
		desired = jobs.Failed
		code = "timeout"
	}
	return w.finish(j, desired, code, review, started)
}
func (w *Worker) finish(j jobs.Job, desired jobs.State, code string, review bool, started time.Time) error {
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
	// A local removal failure must not prevent attempting independent device
	// cleanup. Both sides must succeed before the job can become terminal.
	if review {
		review = !w.cleanupDevice(j.ID)
	}
	if cleanupErr != nil {
		w.log().Error("server cleanup failed; device quarantined", "job", j.ID, "detail", logging.Chain(cleanupErr))
		return w.Jobs.Quarantine(j.ID, "Server cleanup failed. Resolve filesystem access and restart before confirming cleanup.", desired)
	}
	if review {
		w.log().Warn("device review required before the job can finish", "job", j.ID, "state", string(desired))
		return w.Jobs.Quarantine(j.ID, "Device cleanup could not be confirmed automatically. Inspect job-owned staging, helper files, and installed/replaced apps; confirm cleanup before continuing.", desired)
	}
	if err = w.Expire(); err != nil {
		return err
	}
	current, err := w.Jobs.Get(j.ID)
	if err != nil {
		return err
	}
	desired = current.PendingState
	w.log().Info("job finished", "job", j.ID, "state", string(desired), "duration", time.Since(started).Round(time.Millisecond).String())
	return w.Jobs.Change(j.ID, desired, map[string]any{"phase": string(desired)})
}

// Run only after Decrypt has returned, under the active device lease or startup
// process lock/quarantine. Cancellation of the job must not cancel its cleanup.
// Do not race a timeout against an ongoing device operation: the implementation
// must honor the context and return before device ownership can be released.
func (w *Worker) cleanupDevice(id string) bool {
	cleaner, ok := w.Engine.(engine.Cleaner)
	if !ok {
		w.log().Warn("automatic device cleanup unavailable", "job", id, "reason", "unsupported_engine")
		return false
	}
	timeout := w.CleanupTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	started := time.Now()
	w.log().Info("automatic device cleanup started", "job", id, "timeout", timeout.String())
	result, err := cleaner.Cleanup(ctx, id)
	duration := time.Since(started).Round(time.Millisecond).String()
	if ctx.Err() != nil {
		w.log().Warn("automatic device cleanup unconfirmed", "job", id, "reason", "deadline_exceeded", "duration", duration)
		return false
	}
	if err != nil {
		w.log().Error("automatic device cleanup failed", "job", id, "duration", duration, "detail", logging.Chain(err))
		return false
	}
	if !result.Confirmed {
		w.log().Warn("automatic device cleanup unconfirmed", "job", id, "reason", "incomplete_report", "duration", duration)
		return false
	}
	if result.Uninstalled {
		if err := w.Jobs.Metadata(id, map[string]any{"uninstalled": true}); err != nil {
			w.log().Error("cleanup metadata could not be persisted", "job", id, "detail", logging.Chain(err))
			return false
		}
	}
	w.log().Info("automatic device cleanup confirmed", "job", id, "duration", duration)
	return true
}
func safeMessage(code string) string {
	switch code {
	case "verification_failed":
		return "The output did not pass IPA verification."
	case "device_locked":
		return "The device could not be unlocked. Check RemoteCompanion and the configured unlock PIN, or unlock it manually."
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
	w.log().Info("startup recovery started")
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
				w.log().Warn("removing abandoned storage entry", "kind", kind, "job", id)
				if e = w.Files.Remove(kind, id); e != nil {
					return e
				}
			}
		}
	}
	if err = w.Files.CleanupTemp(); err != nil {
		return err
	}
	if err = w.Expire(); err != nil {
		return err
	}
	// Server reconciliation must succeed before remote recovery can release the
	// quarantine. The engine journal must survive workspace/temp removal above.
	for _, j := range all {
		if j.State == jobs.Cleaning && j.CleanupError != "" && w.cleanupDevice(j.ID) {
			if err = w.Jobs.ResolveAutomatic(j.ID); err != nil {
				w.log().Error("automatic cleanup recovery could not be persisted", "job", j.ID, "detail", logging.Chain(err))
				return err
			}
			w.log().Info("automatic cleanup recovery resolved", "job", j.ID)
		}
	}
	w.log().Info("startup recovery completed")
	return nil
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
		w.log().Info("artifact retention elapsed", "job", j.ID)
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
	w.log().Info("operator confirmed device cleanup", "job", id)
	return w.Jobs.Resolve(id)
}
