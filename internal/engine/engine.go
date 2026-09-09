package engine

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/6ixfalls/ipa-now/internal/logging"
	"github.com/6ixfalls/ipa-now/internal/secrets"
	ipa "github.com/londek/ipadecrypt/pkg/ipadecrypt"
)

type Progress struct {
	Phase          string
	Current, Total int64
}
type Request struct {
	ID, Target, Source, Workspace string
	Progress                      func(Progress)
}
type Result struct {
	Installed, Replaced, Uninstalled bool
	NeedsReview                      bool
}
type Failure struct {
	Code                   string
	Retryable, NeedsReview bool
	Cause                  error
}

func (f *Failure) Error() string { return f.Code }
func (f *Failure) Unwrap() error { return f.Cause }

type Engine interface {
	Decrypt(context.Context, Request) (Result, error)
	Verify(string) error
}

// Cleaner is an optional capability. Implementations must durably journal device
// mutation intent before execution, keyed by job ID, outside the disposable job
// workspace. Cleanup must work after restart and be idempotent. Missing, legacy,
// corrupt or ambiguous ownership records must never produce Confirmed=true.
// Credentials and remote paths stay inside the implementation, not in job data.
type Cleaner interface {
	Cleanup(context.Context, string) (CleanupResult, error)
}

type CleanupResult struct {
	// Confirmed means all job-owned resources are gone, the helper has stopped,
	// and the recorded uninstall/preservation policy has been verified.
	Confirmed   bool
	Uninstalled bool
}
type OperationStore interface {
	StartOperation(string) (string, error)
	Operations(string) ([]string, error)
}
type Adapter struct {
	Operations    OperationStore
	JournalDir    string
	Transport     *JobTransport
	Device        ipa.DeviceConfig
	Secrets       *secrets.Store
	Auth          *secrets.Broker
	Log           *slog.Logger
	DecryptFunc   func(context.Context, ipa.Request) (*ipa.Result, error)
	CleanupFunc   func(context.Context, ipa.CleanupRequest) (ipa.CleanupReport, error)
	helperLogOnce sync.Once
	helperLogs    chan helperLogRecord
}

func (a *Adapter) log() *slog.Logger { return logging.OrDiscard(a.Log) }

const helperLogQueueSize = 128

// These fields are emitted by the pinned helper and contain only operational
// diagnostics. New helper fields stay private until they are reviewed here.
var helperLogFields = []string{
	"action", "actual", "addr", "address", "argc", "base", "budget_ms", "bundle", "bundle_id", "bundle_src",
	"bytes", "callers", "capacity", "cdhash", "cfrelease", "cfstring", "code0", "code1", "compressed", "core_foundation",
	"count", "cpusubtype", "cputype", "crc32", "created", "cryptoff", "cryptsize", "csflags", "debugged", "dep",
	"dependencies", "detail", "dir", "dirfd", "dlerror", "dst", "dyld", "dyld_base", "encrypted", "entries", "err",
	"error", "exception", "exec", "execs_only", "exhausted_at", "expected", "extras", "fat", "fault", "fault_skips",
	"fd", "file_size", "found", "foundation", "got", "handle", "has_error_object", "hits", "i", "image_base", "index",
	"ipa", "kind", "kr", "launch", "libdyld", "links", "loaded", "mach_msg", "magic", "main", "message_id", "method",
	"mode", "ms", "n", "name", "nsyms", "objc", "old_mode", "operation", "option", "output", "pac_strips", "pass",
	"path", "pause", "pc", "pid", "platform", "pool", "port", "prims", "pthread_create", "rc", "read", "reason",
	"regular", "requested", "requirements", "result", "ret", "rpaths", "scanned", "services", "signal", "size",
	"skip_appex", "source", "springboard_services", "src", "staging", "status", "stripped", "subcommand", "tag", "target",
	"verbose", "wait_result", "zlib",
}

type helperLogRecord struct {
	level slog.Level
	args  []any
	done  chan struct{}
}

func (a *Adapter) enqueueHelperLog(level slog.Level, args ...any) {
	logger := a.log()
	if !logger.Enabled(context.Background(), level) {
		return
	}
	a.helperLogOnce.Do(func() {
		a.helperLogs = make(chan helperLogRecord, helperLogQueueSize)
		go func() {
			for record := range a.helperLogs {
				if record.done != nil {
					close(record.done)
					continue
				}
				logger.Log(context.Background(), record.level, "helper output", record.args...)
			}
		}()
	})
	select {
	case a.helperLogs <- helperLogRecord{level: level, args: args}:
	default:
		// Device work must keep moving even when the operator log is slow.
	}
}

func (a *Adapter) flushHelperLogs(ctx context.Context) bool {
	if a.helperLogs == nil {
		return true
	}
	done := make(chan struct{})
	select {
	case a.helperLogs <- helperLogRecord{done: done}:
	case <-ctx.Done():
		return false
	}
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (a *Adapter) Decrypt(ctx context.Context, r Request) (Result, error) {
	if a.Transport != nil {
		release, err := a.Transport.Scope(ctx)
		if err != nil {
			a.log().Warn("job HTTP scope unavailable", "job", r.ID, "detail", logging.Chain(err))
			return Result{}, &Failure{Code: "device_unavailable", Cause: err}
		}
		defer release()
	}

	account, err := a.Secrets.Load()
	if err != nil {
		a.log().Error("apple account session could not be loaded", "job", r.ID, "detail", logging.Chain(err))
		return Result{}, &Failure{Code: "account_unavailable", Cause: err}
	}
	if account != nil && account.PasswordToken == "" && r.Source != "installed" {
		a.log().Info("apple account login required", "job", r.ID)
		account, err = ipa.LoginWithMACAddress(ctx, filepath.Join(r.Workspace, "login"), account.Email, account.Password, account.MACAddress, a.authCode(r.ID))
		if err != nil {
			a.log().Error("apple account login failed", "job", r.ID, "detail", logging.Chain(err))
			return Result{}, &Failure{Code: "authentication_failed", Cause: err}
		}
		if err = a.Secrets.Save(ctx, *account); err != nil {
			a.log().Error("apple account session could not be persisted", "job", r.ID, "detail", logging.Chain(err))
			return Result{}, &Failure{Code: "account_unavailable", Cause: err}
		}
		a.log().Info("apple account login succeeded", "job", r.ID)
	}
	var touched atomic.Bool
	if a.Operations == nil || !filepath.IsAbs(a.JournalDir) {
		return Result{}, &Failure{Code: "storage_failed", Cause: errors.New("durable operation storage required")}
	}
	op, err := a.Operations.StartOperation(r.ID)
	if err != nil {
		return Result{}, &Failure{Code: "storage_failed", Cause: err}
	}
	call := a.DecryptFunc
	if call == nil {
		call = ipa.Decrypt
	}
	result, err := call(ctx, ipa.Request{OperationID: op, JournalDir: a.JournalDir, Target: r.Target, Device: a.Device, Apple: account, StateDir: filepath.Join(r.Workspace, "state"), OutputPath: filepath.Join(r.Workspace, "output.ipa"), Source: ipa.SourcePolicy(r.Source), Uninstall: ipa.UninstallAuto, ExtraVerify: r.Source != "installed", OnAccountUpdate: a.Secrets.Save, OnAuthCode: a.authCode(r.ID), OnEvent: func(e ipa.Event) {
		if e.Phase != ipa.PhaseConnecting && e.Phase != ipa.PhaseCleaning {
			touched.Store(true)
		}
		if e.Phase == ipa.PhaseDecrypting && len(e.Attributes) > 0 {
			fields := logging.Fields(e.Attributes, helperLogFields...)
			args := []any{"job", r.ID, "action", logging.Detail(e.Action)}
			if e.Message != "" {
				args = append(args, "output", logging.Detail(e.Message))
			}
			if fields != "" {
				args = append(args, "attributes", fields)
			}
			a.enqueueHelperLog(helperLogLevel(e.Attributes["level"]), args...)
		}
		switch e.Phase {
		case ipa.PhaseConnecting, ipa.PhaseProbing, ipa.PhaseResolving, ipa.PhaseDownloading, ipa.PhasePatching, ipa.PhaseInstalling, ipa.PhaseDecrypting, ipa.PhaseAssembling, ipa.PhaseVerifying, ipa.PhaseCleaning, ipa.PhaseComplete:
			a.log().Log(context.Background(), slog.LevelDebug, "engine progress", "job", r.ID, "phase", string(e.Phase), "current", max(e.Current, 0), "total", max(e.Total, 0))
			r.Progress(Progress{string(e.Phase), max(e.Current, 0), max(e.Total, 0)})
		}
	}})
	metadata := Result{NeedsReview: true}
	if result != nil {
		metadata.Installed, metadata.Replaced, metadata.Uninstalled = result.Installed, result.Reinstalled, result.Uninstalled
	}
	verified := result != nil && result.Verification.OK() && result.Verification.Scanned > 0 && len(result.Verification.Missing) == 0
	// The public package returns a verified result alongside a deferred cleanup
	// failure. Preserve it for independent verification/publication, but never
	// release the device until the explicit Cleanup report confirms ownership.
	if verified && errors.Is(err, ipa.ErrCleanupUnconfirmed) && ctx.Err() == nil {
		a.log().Warn("verified output awaiting device cleanup", "job", r.ID, "detail", logging.Chain(err))
		return metadata, nil
	}
	if err != nil {
		code := "decryption_failed"
		retry := false
		var ne net.Error
		if errors.Is(err, ipa.ErrVerificationFailed) {
			code = "verification_failed"
		} else if ctx.Err() != nil {
			code = "interrupted"
		} else if !touched.Load() && errors.As(err, &ne) {
			code = "device_unavailable"
			retry = true
		}
		a.log().Debug("classified engine failure", "job", r.ID, "code", code, "retryable", retry, "device_touched", touched.Load(), "detail", logging.Chain(err))
		return metadata, &Failure{Code: code, Retryable: retry, NeedsReview: true, Cause: err}
	}
	if result == nil || !result.Verification.OK() || result.Verification.Scanned == 0 || len(result.Verification.Missing) > 0 {
		a.log().Error("engine verification rejected the output", "job", r.ID, "scanned", verificationCount(result))
		return metadata, &Failure{Code: "verification_failed", NeedsReview: true}
	}
	return metadata, nil
}

func helperLogLevel(level string) slog.Level {
	switch level {
	case "error":
		return slog.LevelError
	case "warn":
		return slog.LevelWarn
	case "debug":
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

func (a *Adapter) Cleanup(ctx context.Context, id string) (CleanupResult, error) {
	if a.Operations == nil || !filepath.IsAbs(a.JournalDir) {
		return CleanupResult{}, errors.New("durable operation storage required")
	}
	ops, err := a.Operations.Operations(id)
	if err != nil {
		return CleanupResult{}, err
	}
	if len(ops) == 0 {
		return CleanupResult{}, ipa.ErrCleanupUnconfirmed
	}
	call := a.CleanupFunc
	if call == nil {
		call = ipa.Cleanup
	}
	result := CleanupResult{Confirmed: true}
	for _, op := range ops {
		if ctx.Err() != nil {
			return CleanupResult{}, errors.Join(err, ctx.Err())
		}
		report, e := call(ctx, ipa.CleanupRequest{OperationID: op, JournalDir: a.JournalDir, Device: a.Device})
		result.Confirmed = result.Confirmed && report.Confirmed && e == nil
		result.Uninstalled = result.Uninstalled || report.Uninstalled
		err = errors.Join(err, e)
	}
	return result, err
}

func (a *Adapter) authCode(id string) func(context.Context) (string, error) {
	return func(c context.Context) (string, error) {
		a.log().Info("waiting for the operator to submit a 2FA code", "job", id)
		code, err := a.Auth.Request(c, id)
		if err != nil {
			a.log().Warn("2FA challenge ended without a code", "job", id, "detail", logging.Chain(err))
		}
		return code, err
	}
}

func verificationCount(r *ipa.Result) int {
	if r == nil {
		return 0
	}
	return r.Verification.Scanned
}
func (a *Adapter) Verify(p string) error {
	v, err := ipa.Verify(p, "", false)
	if err != nil {
		return err
	}
	if !v.OK() || v.Scanned == 0 || len(v.Missing) > 0 {
		return errors.New("verification failed")
	}
	return nil
}
