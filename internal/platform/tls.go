package platform

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func allowedOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	for _, candidate := range strings.Split(env("KZURE_ORIGIN", "http://localhost:8088,http://localhost:5173"), ",") {
		if origin == strings.TrimSpace(candidate) {
			return true
		}
	}
	return false
}
func secureCookie(r *http.Request) bool {
	return env("KZURE_COOKIE_SECURE", "false") == "true" || r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// GenerateLocalTLS creates installation-private certificates; it never modifies
// an OS/browser trust store or transmits the key material.
func GenerateLocalTLS(dir, hosts string) error {
	if e := os.MkdirAll(dir, 0750); e != nil {
		return e
	}
	for _, name := range []string{"ca.key", "ca.crt", "server.key", "server.crt"} {
		if _, e := os.Stat(filepath.Join(dir, name)); e == nil {
			return &os.PathError{Op: "create", Path: name, Err: os.ErrExist}
		}
	}
	serial := func() *big.Int {
		n, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if e != nil {
			panic(e)
		}
		return n
	}
	caKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return e
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "Kzure local development CA"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(3, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, MaxPathLenZero: true}
	caDER, e := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if e != nil {
		return e
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return e
	}
	cert := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "Kzure portal"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(0, 6, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, host := range strings.Split("localhost,127.0.0.1,"+hosts, ",") {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			cert.IPAddresses = append(cert.IPAddresses, ip)
		} else {
			cert.DNSNames = append(cert.DNSNames, host)
		}
	}
	der, e := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
	if e != nil {
		return e
	}
	write := func(name, kind string, bytes []byte, mode os.FileMode) error {
		return os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: bytes}), mode)
	}
	caPK, e := x509.MarshalPKCS8PrivateKey(caKey)
	if e != nil {
		return e
	}
	pk, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		return e
	}
	for _, file := range []struct {
		name, kind string
		data       []byte
		mode       os.FileMode
	}{{"ca.key", "PRIVATE KEY", caPK, 0600}, {"ca.crt", "CERTIFICATE", caDER, 0644}, {"server.key", "PRIVATE KEY", pk, 0640}, {"server.crt", "CERTIFICATE", der, 0644}} {
		if e = write(file.name, file.kind, file.data, file.mode); e != nil {
			return e
		}
	}
	return nil
}
