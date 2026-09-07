package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/6ixfalls/ipa-now/internal/config"
	"github.com/6ixfalls/ipa-now/internal/engine"
	"github.com/6ixfalls/ipa-now/internal/httpapi"
	"github.com/6ixfalls/ipa-now/internal/jobs"
	"github.com/6ixfalls/ipa-now/internal/scheduler"
	"github.com/6ixfalls/ipa-now/internal/secrets"
	"github.com/6ixfalls/ipa-now/internal/storage"
	"github.com/6ixfalls/ipa-now/internal/web"
	"github.com/6ixfalls/ipa-now/internal/worker"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	// Library-created cookies and temporary files inherit private permissions.
	syscall.Umask(0077)
	c, err := config.Load()
	if err != nil {
		return err
	}
	files, err := storage.Open(c.DataDir)
	if err != nil {
		return err
	}
	defer files.Close()
	// Compatibility bridge for package-owned patched-IPAs outside StateDir.
	if err = os.Setenv("TMPDIR", filepath.Join(files.Root, "tmp")); err != nil {
		return errors.New("unable to isolate temporary files")
	}
	store, err := jobs.Open(filepath.Join(files.Root, "jobs.db"))
	if err != nil {
		return errors.New("unable to open SQLite job database")
	}
	defer store.Close()
	secretStore := secrets.New(files.Root, c.AppleEmail, c.ApplePassword)
	if _, err = secretStore.Load(); err != nil {
		return errors.New("unable to load the configured Apple account")
	}
	auth := &secrets.Broker{}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.ResponseHeaderTimeout = 30 * time.Second
	base.TLSHandshakeTimeout = 10 * time.Second
	transport := &engine.JobTransport{Base: base}
	http.DefaultTransport = transport
	adapter := &engine.Adapter{Device: c.Device, Secrets: secretStore, Auth: auth, Transport: transport}
	runner := &worker.Worker{Jobs: store, Files: files, Engine: adapter, Timeout: c.JobTimeout, Retention: c.Retention, MaxAttempts: c.MaxAttempts}
	if err = runner.Recover(); err != nil {
		return errors.New("startup reconciliation failed; inspect the private data directory")
	}
	api := &httpapi.Server{Jobs: store, Files: files, Auth: auth, MaxUpload: c.MaxUpload, QueueLimit: c.QueueLimit, AppleEnabled: c.AppleEmail != "", Resolve: runner.Resolve, UI: web.Handler(), AllowedHosts: []string{c.Listen}}
	srv := &http.Server{Addr: c.Listen, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return errors.New("unable to bind the configured private listen address")
	}
	defer listener.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerDone := make(chan error, 1)
	go func() { workerDone <- scheduler.Run(ctx, store, runner) }()
	serverDone := make(chan error, 1)
	go func() { serverDone <- srv.Serve(listener) }()
	log.Print("ipa-now ready on configured private listen address")
	workerStopped := false
	select {
	case <-ctx.Done():
	case err = <-workerDone:
		workerStopped = true
		stop()
	case err = <-serverDone:
		stop()
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if shutdownErr := srv.Shutdown(shutdown); shutdownErr != nil {
		log.Print("HTTP shutdown deadline exceeded; restart requires reconciliation")
		os.Exit(1) // active handlers cannot outlive the process-owned storage lock
	}
	if !workerStopped {
		select {
		case workerErr := <-workerDone:
			err = errors.Join(err, workerErr)
		case <-shutdown.Done():
			// Never close the lease/database while a library operation may still run.
			// Process exit releases the OS lock; next startup quarantines the job.
			log.Print("worker shutdown deadline exceeded; restart requires cleanup review")
			os.Exit(1) // preserve the lease until the process and all goroutines exit
		}
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("service stopped after an HTTP, worker, or persistence failure; restart requires reconciliation")
	}
	return nil
}
