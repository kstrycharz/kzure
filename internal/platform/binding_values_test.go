package platform

import (
	"net/url"
	"strings"
	"testing"
)

func TestBindingSettingsDoNotExposeCredentials(t *testing.T) {
	host := "service-" + strings.Repeat("ab", 16) + ".kzure"
	credentials := map[string]string{"username": "kzure", "database": "app", "password": "test-pass/@?", "accessKey": "test-access", "secretKey": "test-key"}
	for _, kind := range []string{"postgres", "valkey", "storage"} {
		settings, secrets, err := serviceBindingValues(kind, "", host, credentials)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range settings {
			if strings.Contains(v, credentials["password"]) || v == credentials["secretKey"] || v == credentials["accessKey"] {
				t.Fatal("public binding settings contain credential material")
			}
		}
		if len(secrets) == 0 {
			t.Fatal("binding credential snapshot missing")
		}
		if kind != "storage" {
			prefix := map[string]string{"postgres": "DATABASE", "valkey": "CACHE"}[kind]
			u, err := url.Parse(secrets[prefix+"_URL"])
			if err != nil {
				t.Fatal(err)
			}
			password, _ := u.User.Password()
			if password != credentials["password"] || u.Hostname() != host {
				t.Fatal("credential URI escaping changed the password or private endpoint")
			}
		}
	}
}

func TestBindingsRejectArbitraryDestinationsAndInvalidPrefixes(t *testing.T) {
	for _, host := range []string{"localhost", "api:8090", "169.254.169.254", "https://example.invalid", "service-" + strings.Repeat("ab", 16) + ".kzure"} {
		prefix := "DATABASE"
		if strings.HasPrefix(host, "service-") {
			prefix = "bad-prefix"
		}
		if _, _, err := serviceBindingValues("postgres", prefix, host, map[string]string{}); err == nil {
			t.Fatal("invalid binding contract accepted")
		}
	}
}
