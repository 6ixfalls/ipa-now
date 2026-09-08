package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestValidation(t *testing.T) {
	known := filepath.Join(t.TempDir(), "known_hosts")
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(known, []byte(knownhosts.Line([]string{"device.example"}, key)+"\n"), 0600)
	base := map[string]string{"IPA_NOW_DEVICE_HOST": "device.example", "IPA_NOW_SSH_AUTH": "password", "IPA_NOW_SSH_PASSWORD": "not-a-real-secret", "IPA_NOW_KNOWN_HOSTS_PATH": known}
	get := func(k string) string { return base[k] }
	c, e := Parse(get)
	if e != nil {
		t.Fatal(e)
	}
	if c.Listen != "127.0.0.1:8080" || c.Domain != "" || c.Device.AcceptNewHostKey || c.LogLevel != slog.LevelInfo || c.LogFormat != "text" {
		t.Fatal("unsafe defaults")
	}
	for k, v := range map[string]string{"LISTEN": "0.0.0.0:8080", "DOMAIN": "https://ipa.example.com/path", "DEVICE_PORT": "70000", "DEVICE_HOST": "host; command", "DEVICE_USER": "user && command", "JOB_TIMEOUT": "0s", "MAX_UPLOAD_BYTES": "-1", "ARTIFACT_RETENTION": "forever", "QUEUE_LIMIT": "0", "MAX_ATTEMPTS": "999", "APPLE_EMAIL": "bad address", "LOG_LEVEL": "verbose", "LOG_FORMAT": "xml"} {
		t.Run(k, func(t *testing.T) {
			key := "IPA_NOW_" + k
			old := base[key]
			base[key] = v
			defer func() { base[key] = old }()
			if _, e := Parse(get); e == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	base["IPA_NOW_LISTEN"] = "0.0.0.0:8080"
	base["IPA_NOW_DOMAIN"] = "IPA.Example.com:8443"
	c, e = Parse(get)
	if e != nil || c.Domain != "ipa.example.com:8443" {
		t.Fatalf("reverse-proxy configuration rejected: %+v, %v", c, e)
	}
	delete(base, "IPA_NOW_LISTEN")
	delete(base, "IPA_NOW_DOMAIN")
	base["IPA_NOW_LOG_LEVEL"] = "WARN"
	base["IPA_NOW_LOG_FORMAT"] = "JSON"
	c, e = Parse(get)
	if e != nil || c.LogLevel != slog.LevelWarn || c.LogFormat != "json" {
		t.Fatalf("logging configuration rejected: %+v, %v", c, e)
	}
	delete(base, "IPA_NOW_LOG_LEVEL")
	delete(base, "IPA_NOW_LOG_FORMAT")
	base["IPA_NOW_DEVICE_HOST"] = "invalid:hostname"
	if _, e = Parse(get); e == nil {
		t.Fatal("malformed hostname accepted")
	}
	base["IPA_NOW_DEVICE_HOST"] = "device.example"
	os.WriteFile(known, []byte("malformed key"), 0600)
	if _, e = Parse(get); e == nil {
		t.Fatal("malformed known-host material accepted")
	}
	os.Chmod(known, 0644)
	if _, e = Parse(get); e == nil {
		t.Fatal("public known_hosts accepted")
	}
}

func TestPrivateKeyValidatedAtStartup(t *testing.T) {
	dir := t.TempDir()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "synthetic-test-key")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "key")
	os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600)
	publicKey, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	known := filepath.Join(dir, "known_hosts")
	os.WriteFile(known, []byte(knownhosts.Line([]string{"device.example"}, publicKey)+"\n"), 0600)
	env := map[string]string{"IPA_NOW_DEVICE_HOST": "device.example", "IPA_NOW_SSH_KEY_PATH": keyPath, "IPA_NOW_KNOWN_HOSTS_PATH": known}
	get := func(k string) string { return env[k] }
	if _, err = Parse(get); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(keyPath, []byte("invalid"), 0600)
	if _, err = Parse(get); err == nil {
		t.Fatal("invalid private key accepted")
	}
}
