//go:build integration

package worker

import (
	"context"
	"github.com/6ixfalls/ipa-now/internal/config"
	"github.com/6ixfalls/ipa-now/internal/engine"
	"github.com/6ixfalls/ipa-now/internal/jobs"
	"github.com/6ixfalls/ipa-now/internal/secrets"
	"github.com/6ixfalls/ipa-now/internal/storage"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Explicitly operates on the configured device and verifies automatic cleanup.
// On failure, retain the job/journal for recovery and operator inspection.
func TestRealDevice(t *testing.T) {
	target := os.Getenv("IPA_NOW_INTEGRATION_TARGET")
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*(\.[A-Za-z0-9][A-Za-z0-9-]*)+$`).MatchString(target) {
		t.Fatal("IPA_NOW_INTEGRATION_TARGET must name an installed app you are entitled to decrypt")
	}
	c, e := config.Load()
	if e != nil {
		t.Fatal(e)
	}
	files, e := storage.Open(c.DataDir)
	if e != nil {
		t.Fatal(e)
	}
	defer files.Close()
	t.Setenv("TMPDIR", filepath.Join(files.Root, "tmp"))
	store, e := jobs.Open(filepath.Join(files.Root, "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	transport := &engine.JobTransport{Base: http.DefaultTransport}
	old := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = old }()
	adapter := &engine.Adapter{Operations: store, JournalDir: filepath.Join(files.Root, "device-journal"), Device: c.Device, Secrets: secrets.New(files.Root, "", "", ""), Auth: &secrets.Broker{}, Transport: transport}
	w := &Worker{Jobs: store, Files: files, Engine: adapter, Timeout: c.JobTimeout, Retention: c.Retention, MaxAttempts: 1}
	if e = w.Recover(); e != nil {
		t.Fatal(e)
	}
	existing, e := store.All()
	if e != nil {
		t.Fatal(e)
	}
	for _, j := range existing {
		if !jobs.Terminal(j.State) {
			t.Fatal("finish existing jobs before running the real-device check")
		}
	}
	j := jobs.Job{ID: jobs.NewID(), Target: target, Source: "installed"}
	if e = store.Enqueue(&j, 1); e != nil {
		t.Fatal(e)
	}
	claimed, e := store.Claim()
	if e != nil || claimed == nil {
		t.Fatal("device unavailable")
	}
	if e = w.Run(context.Background(), *claimed); e != nil {
		t.Fatal("worker failed; restart service and inspect cleanup")
	}
	got, e := store.Get(j.ID)
	if e != nil || got.State != jobs.Completed {
		t.Fatal("decryption did not produce a verified artifact; inspect the job through the UI")
	}
	t.Log("Verified IPA retained; device cleanup automatically confirmed. The installed app was preserved.")
}
