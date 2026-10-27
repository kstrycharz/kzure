package platform

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalCertificateChain(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	if err := GenerateLocalTLS(dir, "portal.example.test"); err != nil {
		t.Fatal(err)
	}
	ca, _ := os.ReadFile(filepath.Join(dir, "ca.crt"))
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	raw, _ := os.ReadFile(filepath.Join(dir, "server.crt"))
	block, _ := pem.Decode(raw)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"localhost", "127.0.0.1", "portal.example.test"} {
		if _, err = cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: name, CurrentTime: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: "evil.example.test"}); err == nil {
		t.Fatal("unexpected hostname accepted")
	}
	if err = GenerateLocalTLS(dir, "localhost"); err == nil {
		t.Fatal("existing installation key overwritten")
	}
}
