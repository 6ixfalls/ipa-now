package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestChainUnwrapsAndDeduplicates(t *testing.T) {
	inner := errors.New("grand cause")
	mid := &wrapped{msg: "mid cause", err: inner}
	top := &wrapped{msg: "top", err: mid}
	got := Chain(top)
	want := "top: mid cause: grand cause"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if Chain(nil) != "" {
		t.Fatal("nil chain not empty")
	}
}

func TestChainRedactsCredentialLikeValues(t *testing.T) {
	cases := map[string]string{
		"password=hunter2 login rejected":  "password= [redacted] login rejected",
		"token: abc123 expired":            "token: [redacted] expired",
		"Authorization: Bearer eyJhbGciOi": "Authorization: [redacted]",
		"session=\"a b\" invalid":          "session= [redacted] invalid",
		"device unreachable":               "device unreachable",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
	got := Chain(errors.New("password=hunter2 rejected"))
	if strings.Contains(got, "hunter2") || !strings.Contains(got, "[redacted]") {
		t.Fatalf("chain leaked a credential: %q", got)
	}
}

func TestChainBoundedAndSingleLine(t *testing.T) {
	long := errors.New(strings.Repeat("x", detailLimit+500))
	got := Chain(long)
	if len(got) > detailLimit+100 {
		t.Fatal("chain not truncated")
	}
	multi := errors.New("line one\nline two")
	if strings.ContainsAny(Chain(multi), "\n") {
		t.Fatal("chain not single line")
	}
}

func TestFieldsOnlyRendersAllowedKeys(t *testing.T) {
	got := Fields(map[string]string{"path": "/private/app", "auth_code": "123456", "token": "abc123"}, "path")
	if got != "path=/private/app" {
		t.Fatalf("unexpected fields: %q", got)
	}
}

func TestOrDiscardKeepsNilSilent(t *testing.T) {
	OrDiscard(nil).Info("must not panic or print")
	// A nil logger writing to stderr would pollute test output.
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, nil))
	OrDiscard(l).Info("routed")
	if !strings.Contains(buf.String(), "routed") {
		t.Fatal("configured logger not used")
	}
}

func TestNewRespectsFormatAndLevel(t *testing.T) {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = old }()
	debug := New(slog.LevelDebug, "json")
	info := New(slog.LevelInfo, "text")
	debug.Debug("visible")
	info.Debug("hidden")
	w.Close()
	var out bytes.Buffer
	out.ReadFrom(r)
	s := out.String()
	if !strings.Contains(s, `"msg":"visible"`) {
		t.Fatal("debug logger missing json record")
	}
	if strings.Contains(s, "hidden") {
		t.Fatal("info logger emitted debug record")
	}
}

type wrapped struct {
	msg string
	err error
}

func (w *wrapped) Error() string { return w.msg }
func (w *wrapped) Unwrap() error { return w.err }
