package platform

import (
	"strings"
	"testing"
)

func TestPolicyPreservesRulePriorityAndOwnership(t *testing.T) {
	install := "0123456789abcdef0123456789abcdef"
	base := NetworkRule{Name: "allow-http", Priority: 100, Direction: "inbound", Action: "allow", Protocol: "tcp", Source: "10.96.1.0/24", Destination: "*", Port: 8080}
	deny := base
	deny.Priority = 200
	deny.Action = "deny"
	endpoint := policyEndpoint{PolicyResource: PolicyResource{ID: "abcdef0123456789abcdef0123456789", TenantID: "tenant-a", Address: "10.96.1.2", Rules: []NetworkRule{deny, base}}}
	text, _, _, e := renderPolicy(install, []policyEndpoint{endpoint}, []string{"169.254.0.0/16"})
	if e != nil {
		t.Fatal(e)
	}
	allowAt := strings.Index(text, "-s 10.96.1.0/24 -p tcp --dport 8080 -j RETURN")
	denyAt := strings.Index(text, "-s 10.96.1.0/24 -p tcp --dport 8080 -j DROP")
	if allowAt < 0 || denyAt < allowAt {
		t.Fatal("first-match priorities were not retained")
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "-F ") && !strings.HasPrefix(line, "-F KZ01234567") {
			t.Fatalf("unowned chain flushed: %s", line)
		}
	}
	if strings.Contains(text, "-F DOCKER") || strings.Contains(text, "-F INPUT") {
		t.Fatal("host chains must be preserved")
	}
}

func TestPolicyRejectsMalformedRulesAndCollisions(t *testing.T) {
	base := NetworkRule{Priority: 100, Direction: "inbound", Action: "allow", Protocol: "tcp", Source: "*", Destination: "*", Port: 22}
	for _, change := range []func(*NetworkRule){func(r *NetworkRule) { r.Source = "0.0.0.0/0 -j ACCEPT" }, func(r *NetworkRule) { r.Protocol = "icmp" }, func(r *NetworkRule) { r.Action = "ACCEPT" }, func(r *NetworkRule) { r.Direction = "sideways" }, func(r *NetworkRule) { r.Protocol = "any" }, func(r *NetworkRule) { r.Port = -1 }, func(r *NetworkRule) { r.Source = "::/0" }} {
		r := base
		change(&r)
		if validateNetworkRules([]NetworkRule{r}) == nil {
			t.Fatal("unsafe rule accepted")
		}
	}
	if validateNetworkRules([]NetworkRule{base, base}) == nil {
		t.Fatal("duplicate priorities accepted")
	}
	a := policyEndpoint{PolicyResource: PolicyResource{ID: "abcdef0123456789abcdef0123456789", Address: "10.96.1.2"}}
	b := a
	b.ID = "abcdef9876543210abcdef9876543210"
	b.Address = "10.96.1.3"
	if _, _, _, e := renderPolicy("0123456789abcdef0123456789abcdef", []policyEndpoint{a, b}, nil); e == nil {
		t.Fatal("chain identity collision must fail safely")
	}
}

func TestEmptyPolicyCanRemoveLastAssociation(t *testing.T) {
	text, _, addresses, e := renderPolicy("0123456789abcdef0123456789abcdef", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(addresses) != 0 || !strings.HasSuffix(text, "COMMIT\n") {
		t.Fatal("invalid empty generation")
	}
}

func TestObservedPolicyFingerprintRequiresValidAdapterEvidence(t *testing.T) {
	fingerprint := strings.Repeat("ab", 32)
	for _, logs := range []string{"KZURE_POLICY_FINGERPRINT=" + fingerprint + "\n", "2026-10-27T10:00:00.123456789Z KZURE_POLICY_FINGERPRINT=" + fingerprint + "\n"} {
		got, err := policyFingerprint(logs)
		if err != nil || got != fingerprint {
			t.Fatalf("valid observed fingerprint rejected: %q %v", got, err)
		}
	}
	for _, logs := range []string{"", "Applied Kzure-owned filter generation", "KZURE_POLICY_FINGERPRINT=invalid", "untrusted KZURE_POLICY_FINGERPRINT=" + fingerprint, "KZURE_POLICY_FINGERPRINT=" + strings.Repeat("G", 64)} {
		if _, err := policyFingerprint(logs); err == nil {
			t.Fatal("invalid or missing adapter evidence accepted")
		}
	}
}
