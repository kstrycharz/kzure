package platform

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const applicationDomain = "apps.kzure.localhost"

type edgeRoute struct {
	Hostname   string `json:"hostname"`
	ResourceID string `json:"resourceId"`
	Upstream   string `json:"upstream"`
	lifetime   context.Context
	cancel     context.CancelFunc
}
type edgeConfig struct {
	Generation string      `json:"generation"`
	HTTPSPort  int         `json:"httpsPort"`
	Routes     []edgeRoute `json:"routes"`
}
type edgeStatus struct {
	Generation string    `json:"generation"`
	CA         string    `json:"ca"`
	ExpiresAt  time.Time `json:"expiresAt"`
}
type edgeSnapshot struct {
	config edgeConfig
	routes map[string]edgeRoute
}

var applicationLabel = regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

func validApplicationHostname(host string) bool {
	label, ok := strings.CutSuffix(host, "."+applicationDomain)
	return ok && len(label) <= 63 && applicationLabel.MatchString(label)
}

func edgeGeneration(config edgeConfig) string {
	config.Generation = ""
	b, _ := json.Marshal(config)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func validateEdgeConfig(config edgeConfig) (*edgeSnapshot, error) {
	if config.HTTPSPort < 1024 || config.HTTPSPort > 65535 || len(config.Routes) > 128 || config.Generation != edgeGeneration(config) {
		return nil, errors.New("invalid gateway configuration identity or limits")
	}
	snapshot := &edgeSnapshot{config: config, routes: map[string]edgeRoute{}}
	for _, route := range config.Routes {
		if !validApplicationHostname(route.Hostname) || len(route.ResourceID) != 32 || strings.Trim(route.ResourceID, "0123456789abcdef") != "" {
			return nil, errors.New("invalid application route identity")
		}
		if _, exists := snapshot.routes[route.Hostname]; exists {
			return nil, errors.New("duplicate application hostname")
		}
		if route.Upstream != "" {
			host, port, err := net.SplitHostPort(route.Upstream)
			ip, ipError := netip.ParseAddr(host)
			number, portError := strconv.Atoi(port)
			if err != nil || ipError != nil || !ip.Is4() || !ip.IsPrivate() || portError != nil || number < 1 || number > 65535 {
				return nil, errors.New("gateway upstream must be a private workload IPv4 endpoint")
			}
		}
		snapshot.routes[route.Hostname] = route
	}
	return snapshot, nil
}

func applicationHost(raw string) string {
	host := raw
	if value, _, err := net.SplitHostPort(raw); err == nil {
		host = value
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func edgeTransport() *http.Transport {
	return &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext, MaxIdleConns: 128, MaxIdleConnsPerHost: 8, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 20 * time.Second}
}
func edgeHandler(current *atomic.Pointer[edgeSnapshot], redirect bool) http.Handler {
	return edgeHandlerWithTransport(current, redirect, edgeTransport())
}
func edgeHandlerWithTransport(current *atomic.Pointer[edgeSnapshot], redirect bool, transport *http.Transport) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snapshot := current.Load()
		if snapshot == nil {
			http.Error(w, "Application gateway is starting", 503)
			return
		}
		route, exists := snapshot.routes[applicationHost(r.Host)]
		if !exists {
			http.NotFound(w, r)
			return
		}
		if redirect {
			target := url.URL{Scheme: "https", Host: net.JoinHostPort(route.Hostname, strconv.Itoa(snapshot.config.HTTPSPort)), Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery}
			http.Redirect(w, r, target.String(), http.StatusPermanentRedirect)
			return
		}
		if route.Upstream == "" || route.lifetime != nil && route.lifetime.Err() != nil {
			http.Error(w, "Application is stopped, updating or not ready", 503)
			return
		}
		if route.lifetime != nil {
			ctx, cancel := context.WithCancel(r.Context())
			stop := context.AfterFunc(route.lifetime, cancel)
			defer stop()
			defer cancel()
			r = r.WithContext(ctx)
		}
		target := &url.URL{Scheme: "http", Host: route.Upstream}
		proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			p.Out.Host = p.In.Host
			p.SetXForwarded()
		}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "Application backend is unavailable", 503)
		}}
		proxy.ServeHTTP(w, r)
	})
}

func activateEdgeSnapshot(current *atomic.Pointer[edgeSnapshot], next *edgeSnapshot, transport *http.Transport) {
	previous := current.Load()
	for host, route := range next.routes {
		if previous != nil {
			if old, exists := previous.routes[host]; exists && old.ResourceID == route.ResourceID && old.Upstream == route.Upstream {
				route.lifetime, route.cancel = old.lifetime, old.cancel
			}
		}
		if route.lifetime == nil {
			route.lifetime, route.cancel = context.WithCancel(context.Background())
		}
		next.routes[host] = route
	}
	if previous != nil {
		for host, old := range previous.routes {
			replacement, exists := next.routes[host]
			if !exists || old.ResourceID != replacement.ResourceID || old.Upstream != replacement.Upstream {
				if old.cancel != nil {
					old.cancel()
				}
			}
		}
	}
	current.Store(next)
	transport.CloseIdleConnections()
}

// The gateway has no Docker socket and exposes only managed application routes.
// Its status listener is private to its container network; it cannot change state.
func RunEdge() error {
	dir := env("KZURE_EDGE_DATA", "/data")
	if err := ensureEdgeCertificate(dir, false); err != nil {
		return err
	}
	var current atomic.Pointer[edgeSnapshot]
	var certificate atomic.Pointer[tls.Certificate]
	transport := edgeTransport()
	load := func() {
		if raw, err := os.ReadFile(filepath.Join(dir, "routes.json")); err == nil {
			var config edgeConfig
			if json.Unmarshal(raw, &config) == nil {
				if snapshot, err := validateEdgeConfig(config); err == nil {
					if previous := current.Load(); previous == nil || previous.config.Generation != snapshot.config.Generation {
						// A detached/recreated network can strand idle sockets to an
						// old container even if the replacement reuses its address.
						activateEdgeSnapshot(&current, snapshot, transport)
					}
				}
			}
		}
		if pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")); err == nil {
			if pair.Leaf == nil {
				pair.Leaf, _ = x509.ParseCertificate(pair.Certificate[0])
			}
			if pair.Leaf != nil {
				certificate.Store(&pair)
			}
		}
	}
	load()
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		lastRenewal := time.Now()
		for range ticker.C {
			load()
			if time.Since(lastRenewal) > time.Hour {
				if err := ensureEdgeCertificate(dir, false); err != nil {
					log.Printf("application certificate renewal: %v", err)
				}
				lastRenewal = time.Now()
			}
		}
	}()
	status := http.NewServeMux()
	status.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		snapshot := current.Load()
		pair := certificate.Load()
		if snapshot == nil || pair == nil {
			http.Error(w, "Gateway configuration not ready", 503)
			return
		}
		ca, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
		if err != nil {
			http.Error(w, "Certificate authority unavailable", 503)
			return
		}
		jsonOut(w, 200, edgeStatus{Generation: snapshot.config.Generation, CA: string(ca), ExpiresAt: pair.Leaf.NotAfter})
	})
	errors := make(chan error, 3)
	go func() {
		errors <- (&http.Server{Addr: "127.0.0.1:9091", Handler: status, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}).ListenAndServe()
	}()
	go func() {
		errors <- (&http.Server{Addr: ":8080", Handler: edgeHandlerWithTransport(&current, true, transport), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute}).ListenAndServe()
	}()
	server := &http.Server{Addr: ":8443", Handler: edgeHandlerWithTransport(&current, false, transport), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		pair := certificate.Load()
		if pair == nil {
			return nil, fmt.Errorf("application certificate unavailable")
		}
		return pair, nil
	}}}
	go func() { errors <- server.ListenAndServeTLS("", "") }()
	return <-errors
}

func EdgeStatus() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:9091/status", nil)
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("gateway status %d", response.StatusCode)
	}
	_, err = io.Copy(os.Stdout, io.LimitReader(response.Body, 64*1024))
	return err
}
