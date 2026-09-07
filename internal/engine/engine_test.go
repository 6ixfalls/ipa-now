package engine

import (
	"context"
	"errors"
	"github.com/6ixfalls/ipa-now/internal/secrets"
	ipa "github.com/londek/ipadecrypt/pkg/ipadecrypt"
	"net"
	"path/filepath"
	"testing"
)

func TestAdapterPublicAPIAndSafeDefaults(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Device: ipa.DeviceConfig{Host: "configured-device", KnownHostsPath: "/dedicated/known_hosts"}, Secrets: secrets.New(root, "", ""), Auth: &secrets.Broker{}}
	called := false
	a.DecryptFunc = func(ctx context.Context, r ipa.Request) (*ipa.Result, error) {
		called = true
		if r.KeepRemoteFiles || r.SkipVerify || r.Device.AcceptNewHostKey || r.Uninstall != ipa.UninstallAuto || r.StateDir != filepath.Join(root, "state") || r.OutputPath != filepath.Join(root, "output.ipa") {
			t.Fatal("unsafe request")
		}
		r.OnEvent(ipa.Event{Phase: ipa.PhaseDecrypting, Message: "password=secret", Attributes: map[string]string{"token": "secret"}})
		return &ipa.Result{Verification: ipa.VerificationResult{Scanned: 1}}, nil
	}
	var phase string
	r, e := a.Decrypt(context.Background(), Request{ID: "id", Target: "com.example.app", Source: "installed", Workspace: root, Progress: func(p Progress) { phase = p.Phase }})
	if e != nil || !called || phase != "decrypting" || !r.NeedsReview {
		t.Fatal(r, e)
	}
}
func TestAdapterRetryClassification(t *testing.T) {
	for _, touched := range []bool{false, true} {
		a := &Adapter{Secrets: secrets.New(t.TempDir(), "", ""), Auth: &secrets.Broker{}, DecryptFunc: func(ctx context.Context, r ipa.Request) (*ipa.Result, error) {
			if touched {
				r.OnEvent(ipa.Event{Phase: ipa.PhaseProbing})
			}
			return nil, &net.OpError{Op: "dial", Err: &net.DNSError{IsTimeout: true}}
		}}
		_, e := a.Decrypt(context.Background(), Request{Source: "installed", Progress: func(Progress) {}})
		var f *Failure
		if !errors.As(e, &f) || f.Retryable == touched || f.NeedsReview != touched {
			t.Fatal(e)
		}
	}
}
