package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/6ixfalls/ipa-now/internal/secrets"
	ipa "github.com/londek/ipadecrypt/pkg/ipadecrypt"
)

func TestAdapterPublicAPIAndSafeDefaults(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Operations: &fakeOperations{}, JournalDir: filepath.Join(root, "journal"), Device: ipa.DeviceConfig{Host: "configured-device", KnownHostsPath: "/dedicated/known_hosts"}, Secrets: secrets.New(root, "", "", ""), Auth: &secrets.Broker{}}
	called := false
	a.DecryptFunc = func(ctx context.Context, r ipa.Request) (*ipa.Result, error) {
		called = true
		if r.OperationID == "" || r.JournalDir != a.JournalDir || r.KeepRemoteFiles || r.SkipVerify || r.Device.AcceptNewHostKey || r.Uninstall != ipa.UninstallAuto || r.StateDir != filepath.Join(root, "state") || r.OutputPath != filepath.Join(root, "output.ipa") {
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
		a := &Adapter{Operations: &fakeOperations{}, JournalDir: filepath.Join(t.TempDir(), "journal"), Secrets: secrets.New(t.TempDir(), "", "", ""), Auth: &secrets.Broker{}, DecryptFunc: func(ctx context.Context, r ipa.Request) (*ipa.Result, error) {
			if touched {
				r.OnEvent(ipa.Event{Phase: ipa.PhaseProbing})
			}
			return nil, &net.OpError{Op: "dial", Err: &net.DNSError{IsTimeout: true}}
		}}
		_, e := a.Decrypt(context.Background(), Request{Source: "installed", Progress: func(Progress) {}})
		var f *Failure
		if !errors.As(e, &f) || f.Retryable == touched || !f.NeedsReview {
			t.Fatal(e)
		}
	}
}

func TestAdapterLogsHelperOutputRedactedAtHelperLevel(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	a := &Adapter{
		Operations: &fakeOperations{}, JournalDir: filepath.Join(root, "journal"),
		Secrets: secrets.New(root, "", "", ""), Auth: &secrets.Broker{},
		Log: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})),
		DecryptFunc: func(_ context.Context, r ipa.Request) (*ipa.Result, error) {
			r.OnEvent(ipa.Event{
				Phase: ipa.PhaseDecrypting, Action: "image.failed", Message: "could not decrypt\npassword=hunter2",
				Attributes: map[string]string{"event": "image.failed", "level": "error", "msg": "duplicate", "path": "/private/app", "token": "abc123", "auth_code": "123456"},
			})
			return nil, errors.New("helper exited with status 1")
		},
	}
	_, _ = a.Decrypt(context.Background(), Request{ID: "job", Source: "installed", Workspace: root, Progress: func(Progress) {}})
	flushCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !a.flushHelperLogs(flushCtx) {
		t.Fatal("helper log did not flush")
	}
	out := buf.String()
	for _, want := range []string{"level=ERROR", "helper output", "action=image.failed", "could not decrypt; password= [redacted]", "path=/private/app"} {
		if !strings.Contains(out, want) {
			t.Fatalf("helper log missing %q:\n%s", want, out)
		}
	}
	for _, leaked := range []string{"hunter2", "abc123", "123456", "duplicate", "auth_code", "token="} {
		if strings.Contains(out, leaked) {
			t.Fatalf("helper log leaked or duplicated %q:\n%s", leaked, out)
		}
	}
}

func TestAdapterHelperLoggingDoesNotBlockEventCallback(t *testing.T) {
	root := t.TempDir()
	handler := &blockingHandler{started: make(chan struct{}), release: make(chan struct{})}
	a := &Adapter{
		Operations: &fakeOperations{}, JournalDir: filepath.Join(root, "journal"),
		Secrets: secrets.New(root, "", "", ""), Auth: &secrets.Broker{},
		Log: slog.New(handler),
		DecryptFunc: func(_ context.Context, r ipa.Request) (*ipa.Result, error) {
			for range helperLogQueueSize * 2 {
				r.OnEvent(ipa.Event{Phase: ipa.PhaseDecrypting, Action: "image.done", Attributes: map[string]string{"level": "info", "path": "/private/app"}})
			}
			return &ipa.Result{Verification: ipa.VerificationResult{Scanned: 1}}, nil
		},
	}
	done := make(chan error, 1)
	go func() {
		_, err := a.Decrypt(context.Background(), Request{ID: "job", Source: "installed", Workspace: root, Progress: func(Progress) {}})
		done <- err
	}()
	select {
	case <-handler.started:
	case <-time.After(time.Second):
		t.Fatal("helper logger never started")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("helper logging blocked decryption")
	}
	close(handler.release)
}

type blockingHandler struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *blockingHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}
func (h *blockingHandler) Handle(context.Context, slog.Record) error {
	h.once.Do(func() { close(h.started) })
	<-h.release
	return nil
}
func (h *blockingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *blockingHandler) WithGroup(string) slog.Handler      { return h }

type fakeOperations struct {
	ids []string
	err error
}

func (f *fakeOperations) StartOperation(string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	id := fmt.Sprintf("%032d", len(f.ids)+1)
	f.ids = append(f.ids, id)
	return id, nil
}
func (f *fakeOperations) Operations(string) ([]string, error) { return f.ids, f.err }
