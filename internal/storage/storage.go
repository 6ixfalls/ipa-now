package storage

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"howett.net/plist"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func ValidID(id string) bool { return idPattern.MatchString(id) }

type Store struct {
	Root string
	lock *os.File
}

func Open(root string) (*Store, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("data directory must be a real directory with mode 0700")
	}
	lockPath := filepath.Join(root, "service.lock")
	if info, e := os.Lstat(lockPath); e == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("service lock must be a private regular file")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("data directory is already in use")
	}
	s := &Store{Root: root, lock: f}
	for _, name := range []string{"work", "inputs", "artifacts", "secrets", "tmp"} {
		p := filepath.Join(root, name)
		if err = os.MkdirAll(p, 0700); err != nil {
			s.Close()
			return nil, err
		}
		i, e := os.Lstat(p)
		if e != nil || !i.IsDir() || i.Mode().Perm()&0077 != 0 {
			s.Close()
			return nil, errors.New("unsafe storage directory")
		}
	}
	return s, nil
}
func (s *Store) Close() error { return s.lock.Close() }
func (s *Store) Path(kind, id string) (string, error) {
	if !ValidID(id) {
		return "", errors.New("invalid opaque ID")
	}
	switch kind {
	case "work", "inputs", "artifacts":
	default:
		return "", errors.New("invalid storage kind")
	}
	return filepath.Join(s.Root, kind, id), nil
}
func (s *Store) Workspace(id string) (string, error) {
	p, err := s.Path("work", id)
	if err != nil {
		return "", err
	}
	err = os.Mkdir(p, 0700)
	return p, err
}
func (s *Store) Remove(kind, id string) error {
	p, err := s.Path(kind, id)
	if err != nil {
		return err
	}
	if kind == "work" {
		return os.RemoveAll(p)
	}
	err = os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func (s *Store) Publish(id, from string) (int64, string, error) {
	dest, err := s.Path("artifacts", id)
	if err != nil {
		return 0, "", err
	}
	expected, _ := s.Path("work", id)
	if from != filepath.Join(expected, "output.ipa") {
		return 0, "", errors.New("output outside workspace")
	}
	sourceInfo, err := os.Lstat(from)
	if err != nil || !sourceInfo.Mode().IsRegular() {
		return 0, "", errors.New("output must be a regular file")
	}
	f, err := os.OpenFile(from, os.O_RDWR, 0600)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, "", errors.New("invalid output")
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return 0, "", err
	}
	if err = f.Sync(); err != nil {
		return 0, "", err
	}
	if err = os.Chmod(from, 0600); err != nil {
		return 0, "", err
	}
	if err = os.Rename(from, dest); err != nil {
		return 0, "", err
	}
	dir, err := os.Open(filepath.Dir(dest))
	if err != nil {
		return 0, "", err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return 0, "", err
	}
	return info.Size(), hex.EncodeToString(h.Sum(nil)), nil
}

// Inspect the archive before the decryption library sees metadata or filenames.
func ValidateIPA(filename string, maxExpanded uint64) error {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("IPA must be a regular file")
	}
	z, err := zip.OpenReader(filename)
	if err != nil {
		return errors.New("invalid IPA archive")
	}
	defer z.Close()
	if len(z.File) == 0 || len(z.File) > 100000 {
		return errors.New("archive entry limit")
	}
	seen := map[string]bool{}
	apps := map[string]bool{}
	var expanded uint64
	for _, f := range z.File {
		n := f.Name
		clean := strings.TrimSuffix(n, "/")
		key := strings.ToLower(clean)
		if n == "" || strings.ContainsAny(n, "\\\x00") || strings.HasPrefix(n, "/") || path.Clean(clean) != clean || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, ":") || seen[key] || f.Mode()&os.ModeSymlink != 0 || (!f.Mode().IsRegular() && !f.FileInfo().IsDir()) {
			return errors.New("unsafe or ambiguous archive entry")
		}
		seen[key] = true
		if f.UncompressedSize64 > maxExpanded-expanded {
			return errors.New("expanded archive exceeds limit")
		}
		expanded += f.UncompressedSize64
		parts := strings.Split(n, "/")
		if len(parts) == 3 && parts[0] == "Payload" && strings.HasSuffix(parts[1], ".app") && parts[2] == "Info.plist" {
			apps[parts[1]] = true
			if f.UncompressedSize64 > 1<<20 {
				return errors.New("metadata exceeds limit")
			}
			reader, e := f.Open()
			if e != nil {
				return errors.New("invalid IPA metadata")
			}
			data, e := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
			reader.Close()
			if e != nil || len(data) > 1<<20 {
				return errors.New("invalid IPA metadata")
			}
			var meta struct {
				ID         string `plist:"CFBundleIdentifier"`
				Executable string `plist:"CFBundleExecutable"`
			}
			if _, e = plist.Unmarshal(data, &meta); e != nil || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*(\.[A-Za-z0-9][A-Za-z0-9-]*)+$`).MatchString(meta.ID) || meta.Executable == "" || strings.ContainsAny(meta.Executable, "/\\\x00") || meta.Executable == "." || meta.Executable == ".." {
				return errors.New("unsafe or invalid app metadata")
			}

		}
	}
	if len(apps) != 1 {
		return fmt.Errorf("IPA must contain exactly one top-level app")
	}
	return nil
}

// CleanupTemp reconciles only the library's generated filenames inside the
// process-owned private temp directory. Unknown entries require inspection.
func (s *Store) CleanupTemp() error {
	dir := filepath.Join(s.Root, "tmp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	allowed := regexp.MustCompile(`^ipadecrypt-patched-[0-9]+\.ipa$`)
	for _, entry := range entries {
		if !allowed.MatchString(entry.Name()) || entry.IsDir() {
			return errors.New("unrecognized private temporary resource")
		}
		if err = os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Store) SyncInputs() error {
	dir, err := os.Open(filepath.Join(s.Root, "inputs"))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
