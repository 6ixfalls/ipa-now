package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/6ixfalls/ipa-now/internal/jobs"
	"github.com/6ixfalls/ipa-now/internal/logging"
	"github.com/6ixfalls/ipa-now/internal/secrets"
	"github.com/6ixfalls/ipa-now/internal/storage"
	"gorm.io/gorm"
)

type Server struct {
	AllowedHosts  []string
	AllowedOrigin string
	Jobs          *jobs.Store
	Files         *storage.Store
	Auth          *secrets.Broker
	MaxUpload     int64
	QueueLimit    int
	AppleEnabled  bool
	Resolve       func(string) error
	UI            http.Handler
	Log           *slog.Logger
	uploadMu      sync.Mutex
}

func (s *Server) log() *slog.Logger { return logging.OrDiscard(s.Log) }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Jobs.List()
		if e != nil {
			problem(w, 500, "storage_error", "Unable to read jobs.")
			return
		}
		write(w, 200, v)
	})
	mux.HandleFunc("POST /api/jobs", s.enqueue)
	mux.HandleFunc("POST /api/uploads", s.upload)
	mux.HandleFunc("GET /api/jobs/{id}", s.get)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.cancel)
	mux.HandleFunc("POST /api/jobs/{id}/auth-code", s.authCode)
	mux.HandleFunc("POST /api/jobs/{id}/confirm-cleanup", s.confirm)
	mux.HandleFunc("GET /api/jobs/{id}/artifact", s.download)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { problem(w, 404, "not_found", "Endpoint not found.") })
	if s.UI != nil {
		mux.Handle("/", s.UI)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.AllowedHosts) > 0 {
			allowed := false
			for _, host := range s.AllowedHosts {
				if r.Host == host {
					allowed = true
					break
				}
			}
			if !allowed {
				s.log().Warn("request rejected by host check", "method", r.Method, "path", r.URL.Path, "host", r.Host)
				problem(w, 403, "host_denied", "Use the configured service address.")
				return
			}
		}

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if r.Header.Get("X-IPA-Now") != "1" {
				s.log().Warn("mutation rejected by same-origin check", "method", r.Method, "path", r.URL.Path)
				problem(w, 403, "origin_denied", "A same-origin application request is required.")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, e := url.Parse(origin)
				expected := s.AllowedOrigin
				if expected == "" {
					scheme := "http"
					if r.TLS != nil {
						scheme = "https"
					}
					expected = scheme + "://" + r.Host
				}
				if e != nil || u.String() != expected {
					s.log().Warn("mutation rejected by origin check", "method", r.Method, "path", r.URL.Path)
					problem(w, 403, "origin_denied", "Cross-origin requests are not allowed.")
					return
				}
			}
		}
		// Successful API reads stay at debug: the browser polls them
		// continuously. Rejections surface at warn/error.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			mux.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusWriter{ResponseWriter: w, status: 200}
		mux.ServeHTTP(rec, r)
		level := slog.LevelDebug
		switch {
		case rec.status >= 500:
			level = slog.LevelError
		case rec.status >= 400:
			level = slog.LevelWarn
		}
		if s.log().Enabled(context.Background(), level) {
			s.log().Log(context.Background(), level, "api request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start).Round(time.Millisecond).String())
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.ResponseWriter.Write(b)
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, code, message string) {
	write(w, status, map[string]string{"code": code, "message": message})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		problem(w, 415, "invalid_type", "Use application/json.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(&struct{}{}) != io.EOF {
		problem(w, 400, "invalid_request", "The request body is invalid.")
		return false
	}
	return true
}
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	d, e := s.Jobs.Device()
	if e != nil {
		problem(w, 500, "storage_error", "Unable to read device status.")
		return
	}
	write(w, 200, map[string]any{"device": d, "authJob": s.Auth.Pending(), "appleEnabled": s.AppleEnabled, "maxUploadBytes": s.MaxUpload, "queueLimit": s.QueueLimit, "cleanupReviewRequired": true})
}
func (s *Server) enqueue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target           string `json:"target"`
		Source           string `json:"source"`
		Entitled         bool   `json:"entitled"`
		AllowReplacement bool   `json:"allowReplacement"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !body.Entitled {
		problem(w, 400, "entitlement_required", "Confirm that you are entitled to obtain and decrypt this app.")
		return
	}
	target, e := ValidateTarget(body.Target, body.Source)
	if e != nil {
		problem(w, 400, "invalid_target", e.Error())
		return
	}
	if body.Source == "app-store" && (!s.AppleEnabled || !body.AllowReplacement) {
		problem(w, 400, "app_store_unavailable", "Configure an Apple account and acknowledge replacement and automatic uninstall.")
		return
	}
	j := jobs.Job{ID: jobs.NewID(), Target: target, Source: body.Source}
	if e = s.Jobs.Enqueue(&j, s.QueueLimit); e != nil {
		s.storeError(w, e)
		return
	}
	s.log().Info("job enqueued", "job", j.ID, "source", j.Source, "target", target)
	write(w, 202, j)
}
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if !s.uploadMu.TryLock() {
		problem(w, 429, "upload_busy", "Another upload is in progress.")
		return
	}
	defer s.uploadMu.Unlock()
	if r.Header.Get("X-IPA-Entitled") != "true" || r.Header.Get("X-IPA-Allow-Replacement") != "true" {
		problem(w, 400, "acknowledgement_required", "Confirm entitlement and device replacement/uninstall policy.")
		return
	}
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/octet-stream" {
		problem(w, 415, "invalid_type", "Upload the raw IPA as application/octet-stream.")
		return
	}
	id := jobs.NewID()
	p, _ := s.Files.Path("inputs", id)
	f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		problem(w, 500, "storage_error", "Unable to receive upload.")
		return
	}
	keep := false
	defer func() {
		f.Close()
		if !keep {
			_ = s.Files.Remove("inputs", id)
		}
	}()
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxUpload)
	var received int64
	received, e = io.Copy(f, r.Body)
	if e != nil {
		var tooBig *http.MaxBytesError
		if errors.As(e, &tooBig) {
			problem(w, 413, "upload_too_large", "The IPA exceeds the configured upload limit.")
		} else {
			s.log().Warn("upload interrupted", "detail", logging.Chain(e))
			problem(w, 400, "upload_failed", "The upload was interrupted.")
		}
		return
	}
	if e = f.Sync(); e != nil {
		problem(w, 500, "storage_error", "Unable to save upload.")
		return
	}
	if e = f.Close(); e != nil {
		problem(w, 500, "storage_error", "Unable to save upload.")
		return
	}
	if e = storage.ValidateIPA(p, uint64(s.MaxUpload)*4); e != nil {
		problem(w, 400, "invalid_archive", e.Error())
		return
	}
	if e = s.Files.SyncInputs(); e != nil {
		problem(w, 500, "storage_error", "Unable to persist upload.")
		return
	}
	j := jobs.Job{ID: id, Target: "Uploaded IPA", Source: "upload"}
	if e = s.Jobs.Enqueue(&j, s.QueueLimit); e != nil {
		s.storeError(w, e)
		return
	}
	keep = true
	s.log().Info("upload accepted", "job", id, "bytes", received)
	write(w, 202, j)
}
func (s *Server) lookup(w http.ResponseWriter, r *http.Request) (jobs.Job, bool) {
	id := r.PathValue("id")
	if !storage.ValidID(id) {
		problem(w, 404, "not_found", "Job not found.")
		return jobs.Job{}, false
	}
	j, e := s.Jobs.Get(id)
	if e != nil {
		s.storeError(w, e)
		return j, false
	}
	return j, true
}
func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	j, ok := s.lookup(w, r)
	if ok {
		write(w, 200, j)
	}
}
func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !decode(w, r, &body) {
		return
	}
	j, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if e := s.Jobs.Cancel(j.ID); e != nil {
		s.storeError(w, e)
		return
	}
	s.log().Info("cancellation requested", "job", j.ID)
	write(w, 202, map[string]bool{"cancelRequested": true})
}
func (s *Server) authCode(w http.ResponseWriter, r *http.Request) {
	j, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var b struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &b) {
		return
	}
	if e := s.Auth.Submit(j.ID, b.Code); e != nil {
		problem(w, 409, "invalid_challenge", e.Error())
		return
	}
	s.log().Info("operator submitted a 2FA code", "job", j.ID)
	w.WriteHeader(204)
}
func (s *Server) confirm(w http.ResponseWriter, r *http.Request) {
	j, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var b struct {
		Confirmed bool `json:"confirmed"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !b.Confirmed {
		problem(w, 400, "confirmation_required", "Confirm that you inspected the device and completed cleanup.")
		return
	}
	if e := s.Resolve(j.ID); e != nil {
		s.storeError(w, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	j, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if j.State != jobs.Completed || j.ArtifactExpired || j.ExpiresAt == nil || time.Now().After(*j.ExpiresAt) {
		problem(w, 409, "artifact_unavailable", "No retained, completed artifact is available.")
		return
	}
	p, _ := s.Files.Path("artifacts", j.ID)
	info, e := os.Lstat(p)
	if e != nil || !info.Mode().IsRegular() {
		problem(w, 404, "artifact_unavailable", "Artifact is unavailable.")
		return
	}
	f, e := os.Open(p)
	if e != nil {
		problem(w, 404, "artifact_unavailable", "Artifact is unavailable.")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+j.ID+`.ipa"`)
	http.ServeContent(w, r, j.ID+".ipa", info.ModTime(), f)
}
func (s *Server) storeError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, gorm.ErrRecordNotFound):
		problem(w, 404, "not_found", "Job not found.")
	case errors.Is(e, jobs.ErrConflict):
		problem(w, 409, "state_conflict", "The job no longer accepts that action.")
	case errors.Is(e, jobs.ErrQueueFull):
		problem(w, 429, "queue_full", "The queue is full. Wait for a job to finish.")
	default:
		problem(w, 500, "storage_error", "The operation could not be persisted.")
	}
}
