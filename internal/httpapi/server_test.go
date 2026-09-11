package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/6ixfalls/ipa-now/internal/engine"
	"github.com/6ixfalls/ipa-now/internal/jobs"
	"github.com/6ixfalls/ipa-now/internal/secrets"
	"github.com/6ixfalls/ipa-now/internal/storage"
	"github.com/6ixfalls/ipa-now/internal/testutil"
	"github.com/6ixfalls/ipa-now/internal/worker"
)

type fake struct {
	t      *testing.T
	fail   bool
	calls  int
	retry  bool
	review bool
}

func (f *fake) Decrypt(ctx context.Context, r engine.Request) (engine.Result, error) {
	f.calls++
	if f.fail {
		return engine.Result{}, &engine.Failure{Code: "decryption_failed", Cause: errors.New("password=do-not-leak token=do-not-leak")}
	}
	if f.retry && f.calls == 1 {
		return engine.Result{}, &engine.Failure{Code: "device_unavailable", Retryable: true}
	}
	testutil.WriteIPA(f.t, filepath.Join(r.Workspace, "output.ipa"))
	return engine.Result{BundleID: "com.example.app", Version: "1.2.3", NeedsReview: f.review}, nil
}
func (f *fake) Verify(string) error { return nil }
func harness(t *testing.T) (http.Handler, *Server, *worker.Worker, *fake) {
	t.Helper()
	files, e := storage.Open(filepath.Join(t.TempDir(), "data"))
	if e != nil {
		t.Fatal(e)
	}
	store, e := jobs.Open(filepath.Join(files.Root, "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close(); files.Close() })
	f := &fake{t: t}
	w := &worker.Worker{Jobs: store, Files: files, Engine: f, Timeout: time.Second, Retention: time.Hour, MaxAttempts: 3, RetryDelay: time.Millisecond}
	s := &Server{Jobs: store, Files: files, Auth: &secrets.Broker{}, QueueLimit: 20, MaxUpload: 1 << 20, AppleEnabled: true, Resolve: w.Resolve}
	return s.Handler(), s, w, f
}
func request(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-IPA-Now", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}
func create(t *testing.T, h http.Handler) jobs.Job {
	t.Helper()
	rec := request(h, "POST", "/api/jobs", `{"target":"com.example.app","source":"installed"}`)
	if rec.Code != 202 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var j jobs.Job
	if e := json.Unmarshal(rec.Body.Bytes(), &j); e != nil {
		t.Fatal(e)
	}
	return j
}
func TestEndToEndSuccessFailureRetryCancellation(t *testing.T) {
	for _, mode := range []string{"success", "failure", "retry", "cancel", "cleanup"} {
		t.Run(mode, func(t *testing.T) {
			h, s, w, f := harness(t)
			f.fail = mode == "failure"
			f.retry = mode == "retry"
			f.review = mode == "cleanup"
			j := create(t, h)
			if rec := request(h, "GET", "/api/jobs/"+j.ID+"/artifact", ""); rec.Code != 409 {
				t.Fatal("premature download", rec.Code)
			}
			if mode == "cancel" {
				if rec := request(h, "POST", "/api/jobs/"+j.ID+"/cancel", `{}`); rec.Code != 202 {
					t.Fatal(rec.Code)
				}
			}
			claimed, e := s.Jobs.Claim()
			if e != nil {
				t.Fatal(e)
			}
			if e = w.Run(context.Background(), *claimed); e != nil {
				t.Fatal(e)
			}
			got, _ := s.Jobs.Get(j.ID)
			if mode == "cleanup" {
				if got.State != jobs.Cleaning {
					t.Fatal(got)
				}
				rec := request(h, "GET", "/api/jobs/"+j.ID+"/artifact", "")
				if rec.Code != 409 {
					t.Fatal("unconfirmed output downloadable")
				}
				rec = request(h, "POST", "/api/jobs/"+j.ID+"/confirm-cleanup", `{"confirmed":false}`)
				if rec.Code != 400 {
					t.Fatal(rec.Code)
				}
				rec = request(h, "POST", "/api/jobs/"+j.ID+"/confirm-cleanup", `{"confirmed":true}`)
				if rec.Code != 204 {
					t.Fatal(rec.Code, rec.Body.String())
				}
				got, _ = s.Jobs.Get(j.ID)
			}
			want := jobs.Completed
			if mode == "failure" {
				want = jobs.Failed
			}
			if mode == "cancel" {
				want = jobs.Cancelled
				if f.calls != 0 {
					t.Fatal("cancelled job ran")
				}
			}
			if got.State != want {
				t.Fatal(got)
			}
			rec := request(h, "GET", "/api/jobs/"+j.ID, "")
			if strings.Contains(rec.Body.String(), "do-not-leak") || strings.Contains(rec.Body.String(), "failureReason") || strings.Contains(rec.Body.String(), s.Files.Root) {
				t.Fatal("private information leaked")
			}
			rec = request(h, "GET", "/api/jobs/"+j.ID+"/artifact", "")
			if want == jobs.Completed {
				if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), testutil.IPA(t)) {
					t.Fatal("wrong artifact", rec.Code)
				}
				if rec.Header().Get("Content-Disposition") != `attachment; filename="com.example.app-1.2.3.ipa"` {
					t.Fatal("unsafe filename")
				}
			} else if rec.Code != 409 {
				t.Fatal("failed download exposed")
			}
			if mode == "retry" && f.calls != 2 {
				t.Fatal("retry did not execute")
			}
		})
	}
}
func TestInputAndOriginBoundaries(t *testing.T) {
	h, _, _, _ := harness(t)
	for _, target := range []string{"../file.ipa", "https://evil.example/id123", "http://apps.apple.com/us/app/example/id123", "https://apps.apple.com.evil/app/id123", "com.example;id", "file:///etc/passwd", "123"} {
		body, _ := json.Marshal(map[string]any{"target": target, "source": "installed"})
		if rec := request(h, "POST", "/api/jobs", string(body)); rec.Code != 400 {
			t.Fatal("unsafe target accepted", target, rec.Code)
		}
	}
	r := httptest.NewRequest("POST", "/api/jobs", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatal("missing application header accepted")
	}
	r.Header.Set("X-IPA-Now", "1")
	r.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatal("cross origin accepted")
	}
	for _, body := range []string{`{"target":"com.example.app","source":"installed","password":"x"}`, `{} {}`, strings.Repeat("x", 5000)} {
		rec = request(h, "POST", "/api/jobs", body)
		if rec.Code != 400 {
			t.Fatal("bad body accepted", rec.Code)
		}
	}
}

func TestReverseProxyHostAndOriginBoundaries(t *testing.T) {
	h, s, _, _ := harness(t)
	s.AllowedHosts = []string{"ipa.example.com"}
	s.AllowedOrigin = "https://ipa.example.com"

	r := httptest.NewRequest("POST", "http://ipa.example.com/api/jobs", strings.NewReader(`{"target":"com.example.app","source":"installed"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-IPA-Now", "1")
	r.Header.Set("Origin", "https://ipa.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 202 {
		t.Fatalf("proxy HTTPS origin rejected: %d %s", rec.Code, rec.Body.String())
	}

	for _, tc := range []struct {
		host, origin string
	}{
		{"evil.example", "https://ipa.example.com"},
		{"ipa.example.com", "http://ipa.example.com"},
		{"ipa.example.com", "https://ipa.example.com.evil"},
	} {
		r = httptest.NewRequest("POST", "http://"+tc.host+"/api/jobs", strings.NewReader(`{}`))
		r.Header.Set("X-IPA-Now", "1")
		r.Header.Set("Origin", tc.origin)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != 403 {
			t.Fatalf("unsafe proxy request accepted for host %q origin %q: %d", tc.host, tc.origin, rec.Code)
		}
	}
}
func TestUploadValidationSizeAndCleanup(t *testing.T) {
	h, s, _, _ := harness(t)
	for _, test := range []struct {
		name   string
		body   []byte
		status int
	}{{"valid", testutil.IPA(t), 202}, {"invalid", []byte("not zip"), 400}, {"oversized", bytes.Repeat([]byte("x"), (1<<20)+1), 413}} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/uploads", bytes.NewReader(test.body))
			r.Header.Set("X-IPA-Now", "1")
			r.Header.Set("X-IPA-Allow-Replacement", "true")
			r.Header.Set("Content-Type", "application/octet-stream")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != test.status {
				t.Fatal(rec.Code, rec.Body.String())
			}
		})
	}
	entries, e := os.ReadDir(filepath.Join(s.Files.Root, "inputs"))
	if e != nil || len(entries) != 1 {
		t.Fatal("failed inputs retained", entries, e)
	}
}
func TestValidationNormalizesAppStoreURLs(t *testing.T) {
	for _, raw := range []string{"123456", "com.example.app", "https://apps.apple.com/us/app/example/id123456?mt=8"} {
		got, e := ValidateTarget(raw, "app-store")
		if e != nil || got == "" {
			t.Fatal(got, e)
		}
	}
	if _, e := ValidateTarget("https://apps.apple.com:443/app/id123", "app-store"); e == nil {
		t.Fatal("noncanonical host accepted")
	}
}
func TestDownloadTraversalAndRedaction(t *testing.T) {
	h, _, _, _ := harness(t)
	rec := request(h, "GET", "/api/jobs/not-an-id/artifact", "")
	if rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	rec = request(h, "GET", "/api/status", "")
	data, _ := io.ReadAll(rec.Result().Body)
	for _, forbidden := range []string{"password", "knownHosts", "keyPath", "device.example"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatal("status leaked credentials")
		}
	}
}
func TestDownloadName(t *testing.T) {
	for _, test := range []struct {
		name string
		job  jobs.Job
		want string
	}{{"app and version", jobs.Job{BundleID: "com.example.app", Version: "1.2.3"}, "com.example.app-1.2.3.ipa"},
		{"no version", jobs.Job{BundleID: "com.example.app"}, "com.example.app.ipa"},
		{"legacy job", jobs.Job{ID: "abc123"}, "abc123.ipa"},
		{"hostile metadata", jobs.Job{ID: "abc123", BundleID: `../../e"x\` + "\n" + `.app`, Version: "1..2/../3"}, "e-x--.app-1..2-..-3.ipa"},
		{"overlong", jobs.Job{ID: "abc123", BundleID: strings.Repeat("a", 300)}, strings.Repeat("a", 150) + ".ipa"}} {
		t.Run(test.name, func(t *testing.T) {
			if got := downloadName(test.job); got != test.want {
				t.Fatalf("got %q want %q", got, test.want)
			}
		})
	}
}
