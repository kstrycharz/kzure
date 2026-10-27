package platform

import (
	"errors"
	"net"
	"net/url"
	"regexp"
)

var bindingPrefix = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}$`)
var bindingHost = regexp.MustCompile(`^service-[a-f0-9]{32}\.kzure$`)

// Public settings and secret values are kept separate so listing a binding can
// explain its environment contract without revealing a password or signed key.
func serviceBindingValues(kind, prefix, host string, credentials map[string]string) (map[string]string, map[string]string, error) {
	if prefix == "" {
		prefix = map[string]string{"postgres": "DATABASE", "valkey": "CACHE", "storage": "STORAGE"}[kind]
	}
	if !bindingPrefix.MatchString(prefix) || !bindingHost.MatchString(host) {
		return nil, nil, errors.New("invalid binding prefix or private connector identity")
	}
	settings, secrets := map[string]string{}, map[string]string{}
	switch kind {
	case "postgres":
		if credentials["username"] == "" || credentials["database"] == "" || credentials["password"] == "" {
			return nil, nil, errors.New("database credentials are unavailable")
		}
		settings[prefix+"_HOST"], settings[prefix+"_PORT"], settings[prefix+"_DATABASE"], settings[prefix+"_USER"] = host, "5432", credentials["database"], credentials["username"]
		secrets[prefix+"_PASSWORD"] = credentials["password"]
		u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(host, "5432"), Path: "/" + credentials["database"], User: url.UserPassword(credentials["username"], credentials["password"]), RawQuery: "sslmode=disable"}
		secrets[prefix+"_URL"] = u.String()
	case "valkey":
		if credentials["password"] == "" {
			return nil, nil, errors.New("cache credentials are unavailable")
		}
		settings[prefix+"_HOST"], settings[prefix+"_PORT"] = host, "6379"
		secrets[prefix+"_PASSWORD"] = credentials["password"]
		u := url.URL{Scheme: "redis", Host: net.JoinHostPort(host, "6379"), Path: "/0", User: url.UserPassword("", credentials["password"])}
		secrets[prefix+"_URL"] = u.String()
	case "storage":
		if credentials["accessKey"] == "" || credentials["secretKey"] == "" {
			return nil, nil, errors.New("storage credentials are unavailable")
		}
		settings[prefix+"_ENDPOINT"] = "http://" + net.JoinHostPort(host, "8333")
		settings[prefix+"_REGION"], settings[prefix+"_FORCE_PATH_STYLE"] = "us-east-1", "true"
		secrets[prefix+"_ACCESS_KEY_ID"], secrets[prefix+"_SECRET_ACCESS_KEY"] = credentials["accessKey"], credentials["secretKey"]
	default:
		return nil, nil, errors.New("only PostgreSQL, Valkey and S3 storage support application bindings")
	}
	return settings, secrets, nil
}
