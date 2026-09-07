// Package testutil provides synthetic IPA fixtures with no Apple software.
package testutil

import (
	"archive/zip"
	"bytes"
	"os"
	"testing"
)

func IPA(t testing.TB) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, e := z.Create("Payload/Example.app/Info.plist")
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.Write([]byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.app</string><key>CFBundleExecutable</key><string>Example</string><key>CFBundleShortVersionString</key><string>1.0</string></dict></plist>`))
	if e != nil {
		t.Fatal(e)
	}
	f, e = z.Create("Payload/Example.app/Example")
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.Write([]byte("synthetic test executable; not a real Mach-O"))
	if e != nil {
		t.Fatal(e)
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func WriteIPA(t testing.TB, p string) {
	t.Helper()
	if err := os.WriteFile(p, IPA(t), 0600); err != nil {
		t.Fatal(err)
	}
}
