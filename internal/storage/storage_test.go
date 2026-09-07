package storage

import (
	"archive/zip"
	"bytes"
	"github.com/6ixfalls/ipa-now/internal/testutil"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveBoundaries(t *testing.T) {
	p := filepath.Join(t.TempDir(), "input.ipa")
	testutil.WriteIPA(t, p)
	if e := ValidateIPA(p, 1<<20); e != nil {
		t.Fatal(e)
	}
	if e := ValidateIPA(p, 1); e == nil {
		t.Fatal("expanded limit ignored")
	}
	for _, name := range []string{"../escape", "/absolute", "Payload/../escape", `Payload\escape`, "Payload/App.app/Info.plist/../bad", "Payload/x:evil"} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			f, _ := z.Create(name)
			f.Write([]byte("x"))
			z.Close()
			os.WriteFile(p, b.Bytes(), 0600)
			if e := ValidateIPA(p, 1<<20); e == nil {
				t.Fatal("accepted unsafe entry")
			}
		})
	}
}
func TestSymlinkAndDuplicateRejected(t *testing.T) {
	for _, mode := range []string{"symlink", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			h := &zip.FileHeader{Name: "Payload/App.app/Info.plist"}
			if mode == "symlink" {
				h.SetMode(os.ModeSymlink | 0777)
			}
			f, _ := z.CreateHeader(h)
			f.Write([]byte("../../outside"))
			if mode == "duplicate" {
				z.Create("payload/app.app/info.plist")
			}
			z.Close()
			p := filepath.Join(t.TempDir(), "x.ipa")
			os.WriteFile(p, b.Bytes(), 0600)
			if e := ValidateIPA(p, 1<<20); e == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
func TestIsolationPublicationAndProcessLock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if second, e := Open(root); e == nil {
		second.Close()
		t.Fatal("second process acquired lock")
	}
	for _, id := range []string{"../outside", "", "aa/bb"} {
		if _, e = s.Path("work", id); e == nil {
			t.Fatal("accepted unsafe ID")
		}
	}
	id := "1234567890abcdef1234567890abcdef"
	w, e := s.Workspace(id)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(w, "output.ipa")
	testutil.WriteIPA(t, p)
	size, hash, e := s.Publish(id, p)
	if e != nil || size == 0 || len(hash) != 64 {
		t.Fatal(size, hash, e)
	}
	if _, e = os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("output was not renamed")
	}
	for range 2 {
		if e = s.Remove("work", id); e != nil {
			t.Fatal(e)
		}
	}
	if _, _, e = s.Publish(id, "/tmp/unowned"); e == nil {
		t.Fatal("unowned output accepted")
	}
}

func TestPrivateTemporaryCleanup(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "data"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := filepath.Join(s.Root, "tmp", "ipadecrypt-patched-12345.ipa")
	os.WriteFile(p, []byte("partial"), 0600)
	for range 2 {
		if e = s.CleanupTemp(); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("temporary IPA retained")
	}
	p = filepath.Join(s.Root, "tmp", "unowned")
	os.WriteFile(p, []byte("leave alone"), 0600)
	if e = s.CleanupTemp(); e == nil {
		t.Fatal("unknown temporary entry accepted")
	}
	if _, e = os.Stat(p); e != nil {
		t.Fatal("unknown entry removed")
	}
}
func TestMetadataTraversalRejected(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("Payload/App.app/Info.plist")
	f.Write([]byte(`<plist><dict><key>CFBundleIdentifier</key><string>com.example.app</string><key>CFBundleExecutable</key><string>../../outside</string></dict></plist>`))
	z.Close()
	p := filepath.Join(t.TempDir(), "malicious.ipa")
	os.WriteFile(p, b.Bytes(), 0600)
	if e := ValidateIPA(p, 1<<20); e == nil {
		t.Fatal("metadata traversal accepted")
	}
}

func TestLockSymlinkRejected(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	os.Mkdir(root, 0700)
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("leave alone"), 0600)
	os.Symlink(outside, filepath.Join(root, "service.lock"))
	if s, e := Open(root); e == nil {
		s.Close()
		t.Fatal("lock symlink accepted")
	}
}
