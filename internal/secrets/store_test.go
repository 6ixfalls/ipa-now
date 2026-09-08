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
	s := New(root, "test@example.test", "password-never-on-disk", "02:00:00:00:00:01")
	if e := s.Save(context.Background(), ipa.AppleAccount{Email: "test@example.test", Password: "password-never-on-disk", PasswordToken: "opaque-token", MACAddress: "02:00:00:00:00:01"}); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(s.path)
	if strings.Contains(string(b), "password-never-on-disk") {
		t.Fatal("password persisted")
	}
	a, e := s.Load()
	if e != nil || a.PasswordToken != "opaque-token" || a.Password != "password-never-on-disk" || a.MACAddress != "02:00:00:00:00:01" {
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

func TestMACAddressChangeInvalidatesSavedToken(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "secrets"), 0700); err != nil {
		t.Fatal(err)
	}
	old := New(root, "test@example.test", "password", "02:00:00:00:00:01")
	if err := old.Save(context.Background(), ipa.AppleAccount{Email: "test@example.test", PasswordToken: "old-token", MACAddress: "02:00:00:00:00:01"}); err != nil {
		t.Fatal(err)
	}
	updated := New(root, "test@example.test", "password", "02:00:00:00:00:02")
	account, err := updated.Load()
	if err != nil || account.PasswordToken != "" || account.MACAddress != "02:00:00:00:00:02" {
		t.Fatalf("stale identity retained: %+v, %v", account, err)
	}
}
