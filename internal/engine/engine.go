package engine

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync/atomic"

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
type Adapter struct {
	Transport   *JobTransport
	Device      ipa.DeviceConfig
	Secrets     *secrets.Store
	Auth        *secrets.Broker
	DecryptFunc func(context.Context, ipa.Request) (*ipa.Result, error)
}

func (a *Adapter) Decrypt(ctx context.Context, r Request) (Result, error) {
	if a.Transport != nil {
		release, err := a.Transport.Scope(ctx)
		if err != nil {
			return Result{}, &Failure{Code: "device_unavailable", Cause: err}
		}
		defer release()
	}

	account, err := a.Secrets.Load()
	if err != nil {
		return Result{}, &Failure{Code: "account_unavailable", Cause: err}
	}
	if account != nil && account.PasswordToken == "" && r.Source != "installed" {
		account, err = ipa.Login(ctx, filepath.Join(r.Workspace, "login"), account.Email, account.Password, func(c context.Context) (string, error) { return a.Auth.Request(c, r.ID) })
		if err != nil {
			return Result{}, &Failure{Code: "authentication_failed", Cause: err}
		}
		if err = a.Secrets.Save(ctx, *account); err != nil {
			return Result{}, &Failure{Code: "account_unavailable", Cause: err}
		}
	}
	var touched atomic.Bool
	call := a.DecryptFunc
	if call == nil {
		call = ipa.Decrypt
	}
	result, err := call(ctx, ipa.Request{Target: r.Target, Device: a.Device, Apple: account, StateDir: filepath.Join(r.Workspace, "state"), OutputPath: filepath.Join(r.Workspace, "output.ipa"), Source: ipa.SourcePolicy(r.Source), Uninstall: ipa.UninstallAuto, ExtraVerify: r.Source != "installed", OnAccountUpdate: a.Secrets.Save, OnAuthCode: func(c context.Context) (string, error) { return a.Auth.Request(c, r.ID) }, OnEvent: func(e ipa.Event) {
		if e.Phase != ipa.PhaseConnecting {
			touched.Store(true)
		}
		switch e.Phase {
		case ipa.PhaseConnecting, ipa.PhaseProbing, ipa.PhaseResolving, ipa.PhaseDownloading, ipa.PhasePatching, ipa.PhaseInstalling, ipa.PhaseDecrypting, ipa.PhaseAssembling, ipa.PhaseVerifying, ipa.PhaseCleaning, ipa.PhaseComplete:
			r.Progress(Progress{string(e.Phase), max(e.Current, 0), max(e.Total, 0)})
		}
	}})
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
		return Result{}, &Failure{Code: code, Retryable: retry, NeedsReview: touched.Load(), Cause: err}
	}
	if result == nil || !result.Verification.OK() || result.Verification.Scanned == 0 || len(result.Verification.Missing) > 0 {
		return Result{}, &Failure{Code: "verification_failed", NeedsReview: true}
	}
	// The pinned API omits remote resource ownership/recovery and suppresses a
	// staging removal error. Never infer confirmed cleanup from a nil error.
	return Result{Installed: result.Installed, Replaced: result.Reinstalled, Uninstalled: result.Uninstalled, NeedsReview: true}, nil
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
