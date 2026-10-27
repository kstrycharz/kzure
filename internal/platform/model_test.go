package platform

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestEncryptedCredentialsRejectTamperingAndWrongKey(t *testing.T) {
	a := &App{Key: []byte(strings.Repeat("a", 32))}
	plain := map[string]string{"password": "never-store-this-in-plaintext"}
	sealed, e := a.seal(plain)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(sealed, plain["password"]) {
		t.Fatal("plaintext leaked")
	}
	decoded, e := a.unseal(sealed)
	if e != nil || decoded["password"] != plain["password"] {
		t.Fatal("round trip failed", e)
	}
	raw, _ := base64.StdEncoding.DecodeString(sealed)
	raw[len(raw)-1] ^= 1
	if _, e = a.unseal(base64.StdEncoding.EncodeToString(raw)); e == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
	a.Key = []byte(strings.Repeat("b", 32))
	if _, e = a.unseal(sealed); e == nil {
		t.Fatal("wrong installation key accepted")
	}
}
func TestAdmissionRejectsUnsafeResourceInputs(t *testing.T) {
	base := Spec{CPU: .25, RAM: .25}
	for _, name := range []string{"../host", "UPPER", "a", "x;rm -rf", "a/b", "<script>"} {
		if e := validateSpec("static-web", name, base); e == nil {
			t.Errorf("unsafe name accepted: %s", name)
		}
	}
	if e := validateSpec("static-web", "valid-name", Spec{CPU: -1, RAM: .25}); e == nil {
		t.Fatal("negative cpu")
	}
	if e := validateSpec("static-web", "valid-name", Spec{CPU: 1, RAM: .01}); e == nil {
		t.Fatal("tiny memory")
	}
	if e := validateSpec("static-web", "valid-name", Spec{CPU: .25, RAM: .25, Image: "malicious/unapproved"}); e == nil {
		t.Fatal("unapproved image")
	}
	if e := validateSpec("gpu", "gpu-job", Spec{CPU: 4, RAM: 8, Storage: 10}); e == nil {
		t.Fatal("unqualified gpu provider enabled")
	}
}
func TestStorageShowbackSurvivesComputeStop(t *testing.T) {
	s := Spec{CPU: 8, RAM: 32, Storage: 100}
	running := monthly(s, true)
	stopped := monthly(s, false)
	if stopped < 10.219999 || stopped > 10.220001 {
		t.Fatalf("retained storage wrong: %f", stopped)
	}
	if running-stopped < 105.119999 || running-stopped > 105.120001 {
		t.Fatalf("cpu/ram accounting wrong: %f", running-stopped)
	}
}
