package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

func (d *dockerClient) policyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /network-policy", func(w http.ResponseWriter, r *http.Request) {
		var p PolicyRequest
		if e := decode(r, &p); e != nil {
			jsonOut(w, 400, map[string]string{"error": e.Error()})
			return
		}
		if len(p.Resources) > 128 || len(p.Links) > 256 || len(p.Ingress) > 128 {
			jsonOut(w, 400, map[string]string{"error": "Network policy exceeds this host's development limits"})
			return
		}
		generation, e := d.applyPolicy(r.Context(), p)
		if e != nil {
			jsonOut(w, 502, map[string]string{"error": e.Error()})
			return
		}
		jsonOut(w, 200, map[string]string{"generation": generation})
	})
}

func (d *dockerClient) applyPolicy(ctx context.Context, p PolicyRequest) (string, error) {
	d.policyMu.Lock()
	defer d.policyMu.Unlock()
	endpoints := []policyEndpoint{}
	byID := map[string]int{}
	for _, res := range p.Resources {
		if len(res.SubnetID) != 32 || strings.Trim(res.SubnetID, "0123456789abcdef") != "" {
			return "", errors.New("Invalid protected subnet identity")
		}
		raw, e := d.call(ctx, "GET", "/networks/"+d.name(res.SubnetID)+"-subnet", nil)
		if e != nil {
			return "", e
		}
		var n struct {
			Labels map[string]string
			IPAM   struct{ Config []struct{ Subnet string } }
		}
		if json.Unmarshal(raw, &n) != nil || n.Labels["io.kzure.installation"] != d.install || n.Labels["io.kzure.tenant"] != res.TenantID {
			return "", errors.New("Protected subnet ownership mismatch")
		}
		if len(n.IPAM.Config) == 0 {
			return "", errors.New("Missing subnet address range")
		}
		prefix, e := netip.ParsePrefix(n.IPAM.Config[0].Subnet)
		if e != nil {
			return "", e
		}
		address, e := netip.ParseAddr(res.Address)
		if e != nil || !prefix.Contains(address) {
			return "", errors.New("Protected address is outside its owned subnet")
		}
		if _, exists := byID[res.ID]; exists {
			return "", errors.New("Duplicate protected resource")
		}
		byID[res.ID] = len(endpoints)
		endpoints = append(endpoints, policyEndpoint{PolicyResource: res, AllowsIn: []string{}, AllowsOut: []string{}})
	}
	denied := []string{"169.254.0.0/16", "224.0.0.0/4"}
	raw, e := d.call(ctx, "GET", "/networks", nil)
	if e != nil {
		return "", e
	}
	var networks []struct {
		Name   string
		Labels map[string]string
		IPAM   struct{ Config []struct{ Subnet string } }
	}
	if e = json.Unmarshal(raw, &networks); e != nil {
		return "", e
	}
	for _, n := range networks {
		if n.Name == "kzure_management" || n.Name == "kzure_access-control" || n.Labels["io.kzure.installation"] == d.install && n.Labels["io.kzure.type"] == "bastion-transport" {
			for _, c := range n.IPAM.Config {
				if prefix, e := netip.ParsePrefix(c.Subnet); e == nil && prefix.Addr().Is4() {
					denied = append(denied, prefix.String())
				}
			}
		}
	}
	allow := func(sourceID, sourceIP, targetID, targetIP string, port int) {
		if i, ok := byID[sourceID]; ok {
			endpoints[i].AllowsOut = append(endpoints[i].AllowsOut, fmt.Sprintf("-d %s -p tcp --dport %d", targetIP, port))
		}
		if i, ok := byID[targetID]; ok {
			endpoints[i].AllowsIn = append(endpoints[i].AllowsIn, fmt.Sprintf("-s %s -p tcp --dport %d", sourceIP, port))
		}
	}
	for _, link := range p.Links {
		source, e := d.inspect(ctx, link.SourceID)
		if e != nil {
			continue
		}
		target, e := d.inspect(ctx, link.TargetID)
		if e != nil {
			continue
		}
		if source.Config.Labels["io.kzure.tenant"] != target.Config.Labels["io.kzure.tenant"] {
			return "", errors.New("Cross-tenant service path rejected")
		}
		sourceNet, sourceIP, e := primaryNetwork(source)
		if e != nil {
			continue
		}
		targetNet, targetIP, e := primaryNetwork(target)
		if e != nil {
			continue
		}
		if link.ConnectorID != "" {
			proxy, e := d.inspect(ctx, link.ConnectorID)
			if e != nil {
				continue
			}
			if proxy.Config.Labels["io.kzure.type"] != "service-connector" {
				return "", errors.New("Connector identity mismatch")
			}
			a, b := proxy.NetworkSettings.Networks[sourceNet].IPAddress, proxy.NetworkSettings.Networks[targetNet].IPAddress
			if a != "" && b != "" {
				allow(link.SourceID, sourceIP, "", a, link.Port)
				allow("", b, link.TargetID, targetIP, link.Port)
			}
		} else if source.Config.Labels["io.kzure.type"] == "bastion" {
			if ip := source.NetworkSettings.Networks[targetNet].IPAddress; ip != "" {
				allow("", ip, link.TargetID, targetIP, link.Port)
			}
			raw, e := d.call(ctx, "GET", "/containers/"+d.name(link.SourceID)+"-guacd/json", nil)
			if e == nil {
				var proxy inspection
				if json.Unmarshal(raw, &proxy) == nil {
					if ip := proxy.NetworkSettings.Networks[targetNet].IPAddress; ip != "" {
						allow("", ip, link.TargetID, targetIP, link.Port)
					}
				}
			}
		}
	}
	if len(p.Ingress) > 0 {
		edge, err := d.inspect(ctx, ingressID(d.install))
		if err != nil && !is404(err) {
			return "", err
		}
		if err == nil {
			if edge.Config.Labels["io.kzure.type"] != "application-ingress" {
				return "", errors.New("Application gateway identity mismatch")
			}
			for _, rid := range p.Ingress {
				target, err := d.inspect(ctx, rid)
				if is404(err) {
					continue
				}
				if err != nil {
					return "", err
				}
				network, ip, err := primaryNetwork(target)
				if err != nil {
					continue // a stopped/starting application has no ingress path
				}
				port := 8080
				if target.Config.Labels["io.kzure.type"] == "container-app" {
					port, _ = strconv.Atoi(target.Config.Labels["io.kzure.service-port"])
				} else if target.Config.Labels["io.kzure.type"] != "static-web" {
					return "", errors.New("Invalid ingress policy target")
				}
				if gatewayIP := edge.NetworkSettings.Networks[network].IPAddress; gatewayIP != "" && port > 0 && port <= 65535 {
					allow("", gatewayIP, rid, ip, port)
				}
			}
		}
	}
	text, generation, addresses, e := renderPolicy(d.install, endpoints, denied)
	if e != nil {
		return "", e
	}
	// A desired generation alone cannot prove that the host still enforces it.
	// The adapter compares the observed owned chains and hooks with the snapshot
	// recorded after the last successful apply, repairing drift when necessary.
	fingerprint := ""
	if generation == d.policyGeneration {
		fingerprint = d.policyFingerprint
	}
	image := d.imageLocks["kzure/network-policy:0.1.0"]
	if image == "" {
		return "", errors.New("Build and lock the network policy adapter image before enabling enforcement")
	}
	rid := id()
	labels := d.labels(Resource{ID: rid, Type: "network-policy"})
	cfg := map[string]any{"Image": image, "Labels": labels, "Cmd": []string{"reconcile"}, "Env": []string{"KZURE_INSTALL_ID=" + d.install, "KZURE_POLICY_FINGERPRINT=" + fingerprint}, "HostConfig": map[string]any{"NetworkMode": "host", "CapDrop": []string{"ALL"}, "CapAdd": []string{"NET_ADMIN"}, "Memory": int64(128 * 1024 * 1024), "NanoCpus": int64(500000000), "PidsLimit": 64, "SecurityOpt": []string{"no-new-privileges:true"}}}
	raw, e = d.call(ctx, "POST", "/containers/create?name="+d.name(rid), cfg)
	if e != nil {
		return "", e
	}
	var created struct{ ID string }
	_ = json.Unmarshal(raw, &created)
	defer d.call(context.Background(), "DELETE", "/containers/"+created.ID+"?force=true", nil)
	if e = d.archive(ctx, created.ID, "/run/kzure", "policy.rules", []byte(text), 0600); e != nil {
		return "", e
	}
	addressText := strings.Join(addresses, "\n")
	if addressText != "" {
		addressText += "\n"
	}
	if e = d.archive(ctx, created.ID, "/run/kzure", "addresses", []byte(addressText), 0600); e != nil {
		return "", e
	}
	if _, e = d.call(ctx, "POST", "/containers/"+created.ID+"/start", nil); e != nil {
		return "", e
	}
	waitCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	raw, e = d.call(waitCtx, "POST", "/containers/"+created.ID+"/wait?condition=not-running", nil)
	if e != nil {
		return "", e
	}
	var result struct{ StatusCode int }
	if e = json.Unmarshal(raw, &result); e != nil {
		return "", e
	}
	logs, e := d.logs(ctx, rid)
	if e != nil {
		return "", e
	}
	if result.StatusCode != 0 {
		return "", fmt.Errorf("Network policy adapter failed (%d): %s", result.StatusCode, logs)
	}
	fingerprint, e = policyFingerprint(logs)
	if e != nil {
		return "", e
	}
	d.policyGeneration = generation
	d.policyFingerprint = fingerprint
	return generation, nil
}

func policyFingerprint(logs string) (string, error) {
	for _, line := range strings.Split(logs, "\n") {
		// Docker logs are requested with an RFC3339 timestamp prefix.
		if timestamp, message, ok := strings.Cut(line, " "); ok {
			if _, err := time.Parse(time.RFC3339Nano, timestamp); err == nil {
				line = message
			}
		}
		if value, ok := strings.CutPrefix(line, "KZURE_POLICY_FINGERPRINT="); ok {
			if len(value) == 64 && strings.Trim(value, "0123456789abcdef") == "" {
				return value, nil
			}
		}
	}
	return "", errors.New("Network policy adapter returned no valid observed fingerprint")
}
