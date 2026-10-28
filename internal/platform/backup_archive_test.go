package platform

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupAuthenticatedStream(t *testing.T) {
	key := bytes.Repeat([]byte{17}, 32)
	plain := bytes.Repeat([]byte("workload private content"), 10000)
	var encrypted bytes.Buffer
	if e := encryptBackup(&encrypted, bytes.NewReader(plain), key, "snapshot-1"); e != nil {
		t.Fatal(e)
	}
	var restored bytes.Buffer
	if e := decryptBackup(&restored, bytes.NewReader(encrypted.Bytes()), key, "snapshot-1"); e != nil || !bytes.Equal(restored.Bytes(), plain) {
		t.Fatalf("round trip: %v", e)
	}
	if bytes.Contains(encrypted.Bytes(), []byte("workload private content")) {
		t.Fatal("plaintext in ciphertext")
	}
	damaged := append([]byte{}, encrypted.Bytes()...)
	damaged[40] ^= 1
	for name, data := range map[string][]byte{"modified": damaged, "truncated": encrypted.Bytes()[:encrypted.Len()-20], "trailing": append(append([]byte{}, encrypted.Bytes()...), 1)} {
		t.Run(name, func(t *testing.T) {
			if decryptBackup(io.Discard, bytes.NewReader(data), key, "snapshot-1") == nil {
				t.Fatal("invalid archive accepted")
			}
		})
	}
	if decryptBackup(io.Discard, bytes.NewReader(encrypted.Bytes()), key, "snapshot-2") == nil {
		t.Fatal("cross-backup replay accepted")
	}
}
func TestBackupArchivePathsAndContent(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	if e := os.Mkdir(filepath.Join(source, "nested"), 0750); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(source, "nested", "data"), []byte("preserved"), 0640); e != nil {
		t.Fatal(e)
	}
	var archive bytes.Buffer
	if e := packBackup(&archive, source); e != nil {
		t.Fatal(e)
	}
	raw := append([]byte{}, archive.Bytes()...)
	if e := unpackBackup(&archive, target); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(target, "nested", "data"))
	if e != nil || string(b) != "preserved" {
		t.Fatal("file did not survive")
	}
	if unpackBackup(bytes.NewReader(raw), target) == nil {
		t.Fatal("nonempty target accepted")
	}
	for _, name := range []string{"../escape", "/escape", "nested/../../escape", "nested\\escape"} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			tw := tar.NewWriter(&b)
			_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600})
			tw.Close()
			if unpackBackup(&b, t.TempDir()) == nil {
				t.Fatal("path accepted")
			}
		})
	}
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeFifo} {
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		_ = tw.WriteHeader(&tar.Header{Name: "link", Typeflag: kind, Linkname: "/etc/passwd", Mode: 0600})
		tw.Close()
		if unpackBackup(&b, t.TempDir()) == nil {
			t.Fatal("special entry accepted")
		}
	}
}
