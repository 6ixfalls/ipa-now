package config

import (
	"errors"
	"fmt"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"log/slog"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	ipa "github.com/londek/ipadecrypt/pkg/ipadecrypt"
)

type Config struct {
	Listen, Domain, DataDir                    string
	LogLevel                                   slog.Level
	LogFormat                                  string
	Device                                     ipa.DeviceConfig
	AppleEmail, ApplePassword, AppleMACAddress string
	JobTimeout, Retention                      time.Duration
	MaxUpload                                  int64
	QueueLimit, MaxAttempts                    int
}

func Load() (Config, error) { return Parse(os.Getenv) }
func Parse(get func(string) string) (Config, error) {
	var c Config
	val := func(k, d string) string {
		v := get("IPA_NOW_" + k)
		if v == "" {
			return d
		}
		return v
	}
	c.Listen = val("LISTEN", "127.0.0.1:8080")
	host, port, e := net.SplitHostPort(c.Listen)
	if e != nil {
		return c, errors.New("IPA_NOW_LISTEN must be an IP address and port")
	}
	ip := net.ParseIP(host)
	p, e := strconv.Atoi(port)
	if e != nil || p < 1 || p > 65535 || ip == nil {
		return c, errors.New("IPA_NOW_LISTEN must be an IP address and port")
	}
	c.Domain = strings.ToLower(val("DOMAIN", ""))
	if c.Domain == "" && !ip.IsLoopback() && !ip.IsPrivate() {
		return c, errors.New("IPA_NOW_LISTEN must use a loopback or private IP unless IPA_NOW_DOMAIN is configured")
	}
	if c.Domain != "" {
		domainHost := c.Domain
		if strings.Contains(c.Domain, ":") {
			var domainPort string
			domainHost, domainPort, e = net.SplitHostPort(c.Domain)
			if e != nil {
				return c, errors.New("IPA_NOW_DOMAIN must be a hostname with an optional port")
			}
			p, e = strconv.Atoi(domainPort)
			if e != nil || p < 1 || p > 65535 {
				return c, errors.New("invalid IPA_NOW_DOMAIN port")
			}
		}
		if domainHost != strings.TrimSpace(domainHost) || strings.HasSuffix(domainHost, ".") || net.ParseIP(domainHost) != nil || !validHostname(domainHost) {
			return c, errors.New("IPA_NOW_DOMAIN must be a hostname with an optional port")
		}
	}
	c.DataDir, e = filepath.Abs(val("DATA_DIR", "./data"))
	if e != nil {
		return c, e
	}
	c.Device.Host = val("DEVICE_HOST", "")
	if len(c.Device.Host) > 253 || (net.ParseIP(c.Device.Host) == nil && !validHostname(c.Device.Host)) {
		return c, errors.New("IPA_NOW_DEVICE_HOST is required and must be a hostname or IP")
	}
	c.Device.Port, e = strconv.Atoi(val("DEVICE_PORT", "22"))
	if e != nil || c.Device.Port < 1 || c.Device.Port > 65535 {
		return c, errors.New("invalid IPA_NOW_DEVICE_PORT")
	}
	c.Device.User = val("DEVICE_USER", "mobile")
	if !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]{0,31}$`).MatchString(c.Device.User) {
		return c, errors.New("invalid IPA_NOW_DEVICE_USER")
	}
	c.Device.UnlockPIN = val("DEVICE_UNLOCK_PIN", "")
	if c.Device.UnlockPIN != "" && (!utf8.ValidString(c.Device.UnlockPIN) || len(c.Device.UnlockPIN) > 64 || strings.TrimSpace(c.Device.UnlockPIN) != c.Device.UnlockPIN || strings.IndexFunc(c.Device.UnlockPIN, unicode.IsControl) >= 0) {
		return c, errors.New("invalid IPA_NOW_DEVICE_UNLOCK_PIN")
	}
	c.Device.Auth = ipa.DeviceAuth{Kind: val("SSH_AUTH", "key"), KeyPath: val("SSH_KEY_PATH", ""), KeyPassphrase: val("SSH_KEY_PASSPHRASE", ""), Password: val("SSH_PASSWORD", "")}
	switch c.Device.Auth.Kind {
	case "key":
		if e = privateFile(c.Device.Auth.KeyPath); e != nil {
			return c, fmt.Errorf("IPA_NOW_SSH_KEY_PATH: %w", e)
		}
		key, err := os.ReadFile(c.Device.Auth.KeyPath)
		if err != nil {
			return c, errors.New("unable to read IPA_NOW_SSH_KEY_PATH")
		}
		if c.Device.Auth.KeyPassphrase != "" {
			_, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(c.Device.Auth.KeyPassphrase))
		} else {
			_, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			return c, errors.New("invalid SSH private key or passphrase")
		}

	case "password":
		if c.Device.Auth.Password == "" {
			return c, errors.New("IPA_NOW_SSH_PASSWORD is required")
		}
	default:
		return c, errors.New("IPA_NOW_SSH_AUTH must be key or password")
	}
	c.Device.KnownHostsPath = val("KNOWN_HOSTS_PATH", "")
	if e = privateFile(c.Device.KnownHostsPath); e != nil {
		return c, fmt.Errorf("IPA_NOW_KNOWN_HOSTS_PATH: %w", e)
	}
	known, e := os.ReadFile(c.Device.KnownHostsPath)
	if e != nil {
		return c, errors.New("unable to read known-hosts material")
	}
	hasKey := false
	for _, line := range strings.Split(string(known), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			hasKey = true
		}
	}
	if _, e = knownhosts.New(c.Device.KnownHostsPath); e != nil || !hasKey {
		return c, errors.New("known-hosts file must contain valid, explicitly verified host keys")
	}

	c.AppleEmail = val("APPLE_EMAIL", "")
	c.ApplePassword = val("APPLE_PASSWORD", "")
	c.AppleMACAddress = val("APPLE_MAC_ADDRESS", "")
	if c.AppleEmail != "" {
		if address, err := mail.ParseAddress(c.AppleEmail); err != nil || address.Address != c.AppleEmail || len(c.AppleEmail) > 254 {
			return c, errors.New("invalid IPA_NOW_APPLE_EMAIL")
		}
	} else if c.ApplePassword != "" || c.AppleMACAddress != "" {
		return c, errors.New("IPA_NOW_APPLE_EMAIL is required with Apple account settings")
	}
	if c.AppleMACAddress != "" {
		mac, err := net.ParseMAC(strings.TrimSpace(c.AppleMACAddress))
		if err != nil || len(mac) != 6 {
			return c, errors.New("IPA_NOW_APPLE_MAC_ADDRESS must be a six-byte MAC address")
		}
		c.AppleMACAddress = mac.String()
	}
	c.JobTimeout, e = time.ParseDuration(val("JOB_TIMEOUT", "30m"))
	if e != nil || c.JobTimeout < time.Second || c.JobTimeout > 24*time.Hour {
		return c, errors.New("invalid IPA_NOW_JOB_TIMEOUT (1s to 24h)")
	}
	c.Retention, e = time.ParseDuration(val("ARTIFACT_RETENTION", "24h"))
	if e != nil || c.Retention < time.Minute || c.Retention > 365*24*time.Hour {
		return c, errors.New("invalid IPA_NOW_ARTIFACT_RETENTION (1m to 8760h)")
	}
	c.MaxUpload, e = strconv.ParseInt(val("MAX_UPLOAD_BYTES", "2147483648"), 10, 64)
	if e != nil || c.MaxUpload < 1 || c.MaxUpload > 8<<30 {
		return c, errors.New("invalid IPA_NOW_MAX_UPLOAD_BYTES (1 to 8 GiB)")
	}
	c.QueueLimit, e = strconv.Atoi(val("QUEUE_LIMIT", "20"))
	if e != nil || c.QueueLimit < 1 || c.QueueLimit > 1000 {
		return c, errors.New("invalid IPA_NOW_QUEUE_LIMIT")
	}
	c.MaxAttempts, e = strconv.Atoi(val("MAX_ATTEMPTS", "3"))
	if e != nil || c.MaxAttempts < 1 || c.MaxAttempts > 5 {
		return c, errors.New("invalid IPA_NOW_MAX_ATTEMPTS")
	}
	switch strings.ToLower(val("LOG_LEVEL", "info")) {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "info":
		c.LogLevel = slog.LevelInfo
	case "warn", "warning":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		return c, errors.New("invalid IPA_NOW_LOG_LEVEL (debug, info, warn, or error)")
	}
	c.LogFormat = strings.ToLower(val("LOG_FORMAT", "text"))
	if c.LogFormat != "text" && c.LogFormat != "json" {
		return c, errors.New("invalid IPA_NOW_LOG_FORMAT (text or json)")
	}
	return c, nil
}
func privateFile(p string) error {
	if !filepath.IsAbs(p) {
		return errors.New("an absolute path is required")
	}
	i, e := os.Lstat(p)
	if e != nil {
		return errors.New("file is missing or unreadable")
	}
	if !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 {
		return errors.New("must be a regular file with mode 0600 or stricter")
	}
	f, e := os.Open(p)
	if e == nil {
		e = f.Close()
	}
	return e
}

func validHostname(host string) bool {
	if host == "" {
		return false
	}
	label := regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
	for _, part := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if !label.MatchString(part) {
			return false
		}
	}
	return true
}
