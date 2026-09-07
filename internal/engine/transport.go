package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
)

// JobTransport bridges the pinned library's context-free HTTP requests into the
// job deadline. Install once as http.DefaultTransport at process startup. The
// single-device lease allows exactly one active scope, including Apple login.
type JobTransport struct {
	Base   http.RoundTripper
	mu     sync.Mutex
	active context.Context
}

func (t *JobTransport) Scope(ctx context.Context) (func(), error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active != nil {
		return nil, errors.New("HTTP job scope already active")
	}
	t.active = ctx
	return func() { t.mu.Lock(); t.active = nil; t.mu.Unlock() }, nil
}
func (t *JobTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	job := t.active
	t.mu.Unlock()
	if job == nil {
		return nil, errors.New("HTTP request outside an active job")
	}
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(job, cancel)
	if job.Err() != nil {
		cancel()
	}
	release := func() { stop(); cancel() }
	base := t.Base
	if base == nil {
		release()
		return nil, errors.New("missing base transport")
	}
	response, err := base.RoundTrip(r.Clone(ctx))
	if err != nil {
		release()
		return nil, err
	}
	response.Body = &scopedBody{ReadCloser: response.Body, release: release}
	return response, nil
}

type scopedBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (b *scopedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(b.release)
	}
	return n, err
}
func (b *scopedBody) Close() error { err := b.ReadCloser.Close(); b.once.Do(b.release); return err }
