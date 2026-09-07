package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTransportPropagatesJobDeadline(t *testing.T) {
	tr := &JobTransport{Base: roundTripFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}
	r, _ := http.NewRequest("GET", "https://example.invalid", nil)
	if _, err := tr.RoundTrip(r); err == nil {
		t.Fatal("unscoped HTTP permitted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	release, err := tr.Scope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = tr.Scope(ctx); err == nil {
		t.Fatal("parallel job scopes permitted")
	}
	if _, err = tr.RoundTrip(r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestTransportKeepsBodyBoundToJob(t *testing.T) {
	var requestCtx context.Context
	tr := &JobTransport{Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requestCtx = r.Context()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("test"))}, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release, _ := tr.Scope(ctx)
	defer release()
	r, _ := http.NewRequest("GET", "https://example.invalid", nil)
	response, err := tr.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	if requestCtx.Err() != nil {
		t.Fatal("body cancelled before read")
	}
	response.Body.Close()
	if requestCtx.Err() == nil {
		t.Fatal("request scope not released")
	}
}
