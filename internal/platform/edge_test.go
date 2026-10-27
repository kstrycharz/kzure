package platform

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEdgeRejectsUnsafeSnapshots(t *testing.T) {
	base := edgeConfig{HTTPSPort: 8444, Routes: []edgeRoute{{Hostname: "example." + applicationDomain, ResourceID: strings.Repeat("ab", 16), Upstream: "10.99.1.2:3000"}}}
	for _, change := range []func(*edgeConfig){func(c *edgeConfig) { c.Routes[0].Upstream = "127.0.0.1:8090" }, func(c *edgeConfig) { c.Routes[0].Upstream = "169.254.169.254:80" }, func(c *edgeConfig) { c.Routes[0].Upstream = "api:8090" }, func(c *edgeConfig) { c.Routes[0].Hostname = "localhost" }, func(c *edgeConfig) { c.Routes[0].Hostname = "other.example.com" }, func(c *edgeConfig) { c.Routes = append(c.Routes, c.Routes[0]) }} {
		config := base
		config.Routes = append([]edgeRoute{}, base.Routes...)
		change(&config)
		config.Generation = edgeGeneration(config)
		if _, err := validateEdgeConfig(config); err == nil {
			t.Fatal("unsafe gateway configuration accepted")
		}
	}
	base.Generation = edgeGeneration(base)
	if _, err := validateEdgeConfig(base); err != nil {
		t.Fatal(err)
	}
	base.Generation = "incorrect"
	if _, err := validateEdgeConfig(base); err == nil {
		t.Fatal("changed configuration accepted without matching identity")
	}
}

func TestRouteWithdrawalCancelsOnlyChangedBackendRequests(t *testing.T) {
	entered := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	defer backend.Close()
	host := "first." + applicationDomain
	other := "second." + applicationDomain
	var current atomic.Pointer[edgeSnapshot]
	transport := edgeTransport()
	activateEdgeSnapshot(&current, &edgeSnapshot{config: edgeConfig{HTTPSPort: 8444}, routes: map[string]edgeRoute{host: {Hostname: host, ResourceID: "first", Upstream: strings.TrimPrefix(backend.URL, "http://")}, other: {Hostname: other, ResourceID: "other", Upstream: "10.99.1.2:3000"}}}, transport)
	unchanged := current.Load().routes[other].lifetime
	proxy := httptest.NewServer(edgeHandlerWithTransport(&current, false, transport))
	defer proxy.Close()
	finished := make(chan int, 1)
	go func() {
		request, _ := http.NewRequest("GET", proxy.URL, nil)
		request.Host = host
		response, err := proxy.Client().Do(request)
		if err != nil {
			finished <- 0
			return
		}
		defer response.Body.Close()
		finished <- response.StatusCode
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("backend request did not start")
	}
	activateEdgeSnapshot(&current, &edgeSnapshot{config: edgeConfig{HTTPSPort: 8444}, routes: map[string]edgeRoute{host: {Hostname: host, ResourceID: "first"}, other: {Hostname: other, ResourceID: "other", Upstream: "10.99.1.2:3000"}}}, transport)
	select {
	case status := <-finished:
		if status != 503 {
			t.Fatalf("withdrawal response was %d", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("withdrawn backend request remained stuck")
	}
	if unchanged.Err() != nil || current.Load().routes[other].lifetime != unchanged {
		t.Fatal("unrelated application lifetime was interrupted")
	}
}

func TestEdgeRoutesOnlyKnownReadyApplications(t *testing.T) {
	config := edgeConfig{HTTPSPort: 8444, Routes: []edgeRoute{{Hostname: "example." + applicationDomain, ResourceID: strings.Repeat("ab", 16)}}}
	config.Generation = edgeGeneration(config)
	snapshot, err := validateEdgeConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	var current atomic.Pointer[edgeSnapshot]
	current.Store(snapshot)
	request := httptest.NewRequest("GET", "http://example."+applicationDomain+"/hello?x=1", nil)
	response := httptest.NewRecorder()
	edgeHandler(&current, true).ServeHTTP(response, request)
	if response.Code != 308 || response.Header().Get("Location") != "https://example."+applicationDomain+":8444/hello?x=1" {
		t.Fatal("incorrect managed HTTPS redirect")
	}
	response = httptest.NewRecorder()
	edgeHandler(&current, false).ServeHTTP(response, request)
	if response.Code != 503 {
		t.Fatal("unready application was routed")
	}
	request.Host = "unknown." + applicationDomain
	response = httptest.NewRecorder()
	edgeHandler(&current, false).ServeHTTP(response, request)
	if response.Code != 404 {
		t.Fatal("unknown host was routed")
	}
}

func TestApplicationCertificateRenewalKeepsTrustIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := ensureEdgeCertificate(dir, false); err != nil {
		t.Fatal(err)
	}
	caBefore, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	oldPair, err := tls.LoadX509KeyPair(filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key"))
	if err != nil {
		t.Fatal(err)
	}
	if err = ensureEdgeCertificate(dir, true); err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key"))
	if err != nil {
		t.Fatal(err)
	}
	caAfter, _ := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if string(caBefore) != string(caAfter) {
		t.Fatal("leaf renewal changed the trust root")
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(caAfter)
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err = cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: "example." + applicationDomain}); err != nil {
		t.Fatal(err)
	}
	if string(oldPair.Certificate[0]) == string(pair.Certificate[0]) {
		t.Fatal("certificate was not renewed")
	}
}
