package secrets

import (
	"context"
	"encoding/json"
	"errors"
	ipa "github.com/londek/ipadecrypt/pkg/ipadecrypt"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	mu                                sync.Mutex
	path, email, password, macAddress string
}

func New(root, email, password, macAddress string) *Store {
	return &Store{path: filepath.Join(root, "secrets", "account.json"), email: email, password: password, macAddress: macAddress}
}
func (s *Store) Load() (*ipa.AppleAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.email == "" {
		return nil, nil
	}
	a := &ipa.AppleAccount{Email: s.email, Password: s.password, MACAddress: s.macAddress}
	if info, e := os.Lstat(s.path); e == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("account session must be a private regular file")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, a); err != nil {
		return nil, err
	}
	if a.Email != s.email {
		return nil, errors.New("saved account does not match configured email")
	}
	if a.MACAddress != s.macAddress {
		// App Store tokens are tied to the machine identity. Force a fresh login
		// rather than sending a cached token under a newly configured identity.
		a.PasswordToken = ""
	}
	a.Password = s.password
	a.MACAddress = s.macAddress
	return a, nil
}
func (s *Store) Save(_ context.Context, a ipa.AppleAccount) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.Password = ""
	a.MACAddress = s.macAddress
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), "account-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Broker holds 2FA codes only in memory, scoped to the active job challenge.
type Broker struct {
	mu  sync.Mutex
	job string
	ch  chan string
}

func (b *Broker) Pending() string { b.mu.Lock(); defer b.mu.Unlock(); return b.job }
func (b *Broker) Request(ctx context.Context, id string) (string, error) {
	b.mu.Lock()
	if b.ch != nil {
		b.mu.Unlock()
		return "", errors.New("authentication already pending")
	}
	ch := make(chan string, 1)
	b.job = id
	b.ch = ch
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.job = ""; b.ch = nil; b.mu.Unlock() }()
	select {
	case code := <-ch:
		return code, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func (b *Broker) Submit(id, code string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.job != id || b.ch == nil {
		return errors.New("no matching challenge")
	}
	if len(code) != 6 {
		return errors.New("code must contain six digits")
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return errors.New("code must contain six digits")
		}
	}
	select {
	case b.ch <- code:
		return nil
	default:
		return errors.New("code already submitted")
	}
}
