package secrets

import (
	"context"
	ipa "github.com/londek/ipadecrypt/pkg/ipadecrypt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSecretRoundTripAndMemoryChallenge(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "secrets"), 0700)
	s := New(root, "test@example.test", "password-never-on-disk")
	if e := s.Save(context.Background(), ipa.AppleAccount{Email: "test@example.test", Password: "password-never-on-disk", PasswordToken: "opaque-token"}); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(s.path)
	if strings.Contains(string(b), "password-never-on-disk") {
		t.Fatal("password persisted")
	}
	a, e := s.Load()
	if e != nil || a.PasswordToken != "opaque-token" || a.Password != "password-never-on-disk" {
		t.Fatal("account roundtrip failed")
	}
	i, _ := os.Stat(s.path)
	if i.Mode().Perm() != 0600 {
		t.Fatal(i.Mode())
	}
	broker := &Broker{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan string, 1)
	go func() { code, _ := broker.Request(ctx, "job"); done <- code }()
	for broker.Pending() == "" {
		time.Sleep(time.Millisecond)
	}
	if e = broker.Submit("another-job", "123456"); e == nil {
		t.Fatal("wrong job challenge accepted")
	}
	if e = broker.Submit("job", "abcdef"); e == nil {
		t.Fatal("invalid code accepted")
	}
	if e = broker.Submit("job", "123456"); e != nil {
		t.Fatal(e)
	}
	if code := <-done; code != "123456" {
		t.Fatal("challenge failed")
	}
	if broker.Pending() != "" {
		t.Fatal("challenge retained")
	}
}
