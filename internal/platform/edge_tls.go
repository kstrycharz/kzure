package platform

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

func ensureEdgeCertificate(dir string, force bool) error {
	if _, err := os.Stat(filepath.Join(dir, "ca.crt")); errors.Is(err, os.ErrNotExist) {
		return GenerateLocalTLS(dir, "*."+applicationDomain)
	}
	if !force {
		if raw, err := os.ReadFile(filepath.Join(dir, "server.crt")); err == nil {
			if block, _ := pem.Decode(raw); block != nil {
				if cert, err := x509.ParseCertificate(block.Bytes); err == nil && time.Until(cert.NotAfter) > 30*24*time.Hour {
					return nil
				}
			}
		}
	}
	caRaw, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return err
	}
	keyRaw, err := os.ReadFile(filepath.Join(dir, "ca.key"))
	if err != nil {
		return err
	}
	caBlock, _ := pem.Decode(caRaw)
	keyBlock, _ := pem.Decode(keyRaw)
	if caBlock == nil || keyBlock == nil {
		return errors.New("invalid application certificate authority")
	}
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return err
	}
	caKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return err
	}
	if time.Until(ca.NotAfter) < 24*time.Hour {
		return errors.New("application certificate authority requires operator renewal")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	expires := time.Now().AddDate(0, 6, 0)
	if expires.After(ca.NotAfter) {
		expires = ca.NotAfter
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Kzure application gateway"}, DNSNames: []string{"*." + applicationDomain}, NotBefore: time.Now().Add(-5 * time.Minute), NotAfter: expires, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "server.key.next"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "server.crt.next"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		return err
	}
	if err = os.Rename(filepath.Join(dir, "server.key.next"), filepath.Join(dir, "server.key")); err != nil {
		return err
	}
	return os.Rename(filepath.Join(dir, "server.crt.next"), filepath.Join(dir, "server.crt"))
}
