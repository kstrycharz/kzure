package platform

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

func (d *dockerClient) connectNetwork(ctx context.Context, cid, network string, aliases []string) error {
	raw, e := d.call(ctx, "GET", "/containers/"+cid+"/json", nil)
	if e != nil {
		return e
	}
	var in inspection
	if e = json.Unmarshal(raw, &in); e != nil {
		return e
	}
	if _, ok := in.NetworkSettings.Networks[network]; ok {
		return nil
	}
	endpoint := map[string]any{"Aliases": aliases}
	if strings.HasSuffix(network, "-subnet") {
		ip, e := d.infrastructureAddress(ctx, network)
		if e != nil {
			return e
		}
		endpoint["IPAMConfig"] = map[string]string{"IPv4Address": ip}
	}
	_, e = d.call(ctx, "POST", "/networks/"+network+"/connect", map[string]any{"Container": cid, "EndpointConfig": endpoint})
	return e
}

func (d *dockerClient) infrastructureAddress(ctx context.Context, network string) (string, error) {
	raw, e := d.call(ctx, "GET", "/networks/"+network, nil)
	if e != nil {
		return "", e
	}
	var n struct {
		Labels     map[string]string
		IPAM       struct{ Config []struct{ Subnet string } }
		Containers map[string]struct{ IPv4Address string }
	}
	if json.Unmarshal(raw, &n) != nil || n.Labels["io.kzure.installation"] != d.install || n.Labels["io.kzure.type"] != "subnet" || len(n.IPAM.Config) == 0 {
		return "", errors.New("Infrastructure attachment requires an owned subnet")
	}
	p, e := netip.ParsePrefix(n.IPAM.Config[0].Subnet)
	if e != nil || !p.Addr().Is4() {
		return "", errors.New("Invalid infrastructure address range")
	}
	used := map[string]bool{}
	for _, c := range n.Containers {
		if prefix, e := netip.ParsePrefix(c.IPv4Address); e == nil {
			used[prefix.Addr().String()] = true
		}
	}
	base := p.Masked().Addr().As4()
	start := binary.BigEndian.Uint32(base[:])
	size := uint32(1) << uint(32-p.Bits())
	for offset := size - 2; offset >= size/2; offset-- {
		var bytes [4]byte
		binary.BigEndian.PutUint32(bytes[:], start+offset)
		ip := netip.AddrFrom4(bytes).String()
		if !used[ip] {
			return ip, nil
		}
	}
	return "", errors.New("Subnet infrastructure address pool is full")
}

func (d *dockerClient) ensureSubnet(ctx context.Context, sid, tid, cidr string) error {
	if len(sid) != 32 || len(tid) != 32 || strings.Trim(sid+tid, "0123456789abcdef") != "" {
		return errors.New("Invalid network identity")
	}
	p, e := privatePrefix(cidr)
	if e != nil || p.Bits() < 24 {
		return errors.New("Invalid subnet range")
	}
	labels := d.labels(Resource{ID: sid, TenantID: tid, Type: "subnet"})
	return d.ensureOwned(ctx, "/networks", d.name(sid)+"-subnet", labels, map[string]any{"Driver": "bridge", "Internal": true, "EnableIPv6": false, "CheckDuplicate": true, "IPAM": map[string]any{"Config": []any{map[string]string{"Subnet": cidr}}}})
}

func primaryNetwork(in inspection) (string, string, error) {
	names := []string{}
	for name, n := range in.NetworkSettings.Networks {
		if n.IPAddress != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", "", errors.New("Resource has no network endpoint")
	}
	name := names[0]
	return name, in.NetworkSettings.Networks[name].IPAddress, nil
}

func (d *dockerClient) networkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /network-ranges", func(w http.ResponseWriter, r *http.Request) {
		raw, e := d.call(r.Context(), "GET", "/networks", nil)
		if e != nil {
			jsonOut(w, 502, map[string]string{"error": e.Error()})
			return
		}
		var nets []struct {
			IPAM struct{ Config []struct{ Subnet string } }
		}
		_ = json.Unmarshal(raw, &nets)
		out := []string{}
		for _, n := range nets {
			for _, c := range n.IPAM.Config {
				if c.Subnet != "" {
					out = append(out, c.Subnet)
				}
			}
		}
		jsonOut(w, 200, out)
	})
	mux.HandleFunc("GET /networks/{id}/addresses", func(w http.ResponseWriter, r *http.Request) {
		sid := r.PathValue("id")
		if len(sid) != 32 || strings.Trim(sid, "0123456789abcdef") != "" {
			jsonOut(w, 400, map[string]string{"error": "Invalid subnet identity"})
			return
		}
		raw, e := d.call(r.Context(), "GET", "/networks/"+d.name(sid)+"-subnet", nil)
		if e != nil {
			jsonOut(w, 503, map[string]string{"error": "Subnet is unavailable"})
			return
		}
		var n struct {
			Labels     map[string]string
			Containers map[string]struct{ IPv4Address string }
		}
		if json.Unmarshal(raw, &n) != nil || n.Labels["io.kzure.installation"] != d.install {
			jsonOut(w, 403, map[string]string{"error": "Subnet ownership mismatch"})
			return
		}
		out := []string{}
		for _, c := range n.Containers {
			if p, e := netip.ParsePrefix(c.IPv4Address); e == nil {
				out = append(out, p.Addr().String())
			}
		}
		jsonOut(w, 200, out)
	})
	mux.HandleFunc("POST /networks", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID       string `json:"id"`
			TenantID string `json:"tenantId"`
			CIDR     string `json:"cidr"`
		}
		if e := decode(r, &in); e != nil {
			jsonOut(w, 400, map[string]string{"error": e.Error()})
			return
		}
		if e := d.ensureSubnet(r.Context(), in.ID, in.TenantID, in.CIDR); e != nil {
			jsonOut(w, 502, map[string]string{"error": e.Error()})
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("DELETE /networks/{id}", func(w http.ResponseWriter, r *http.Request) {
		nid := r.PathValue("id")
		if len(nid) != 32 || strings.Trim(nid, "0123456789abcdef") != "" {
			jsonOut(w, 400, map[string]string{"error": "Invalid network ID"})
			return
		}
		name := d.name(nid) + "-subnet"
		raw, e := d.call(r.Context(), "GET", "/networks/"+name, nil)
		if is404(e) {
			jsonOut(w, 200, map[string]bool{"ok": true})
			return
		}
		var n struct{ Labels map[string]string }
		if e != nil || json.Unmarshal(raw, &n) != nil || n.Labels["io.kzure.installation"] != d.install || n.Labels["io.kzure.resource"] != nid {
			jsonOut(w, 403, map[string]string{"error": "Network ownership mismatch"})
			return
		}
		_, e = d.call(r.Context(), "DELETE", "/networks/"+name, nil)
		if e != nil {
			jsonOut(w, 409, map[string]string{"error": e.Error()})
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /resources/{id}/network", func(w http.ResponseWriter, r *http.Request) {
		in, e := d.inspect(r.Context(), r.PathValue("id"))
		if e != nil {
			jsonOut(w, 404, map[string]string{"error": "Resource not found"})
			return
		}
		network, ip, e := primaryNetwork(in)
		if e != nil {
			jsonOut(w, 409, map[string]string{"error": e.Error()})
			return
		}
		jsonOut(w, 200, map[string]string{"network": network, "address": ip, "tenantId": in.Config.Labels["io.kzure.tenant"]})
	})
	mux.HandleFunc("POST /connectors", func(w http.ResponseWriter, r *http.Request) {
		var in connectorRequest
		if e := decode(r, &in); e != nil {
			jsonOut(w, 400, map[string]string{"error": e.Error()})
			return
		}
		if e := d.ensureConnector(r.Context(), in); e != nil {
			jsonOut(w, 502, map[string]string{"error": e.Error()})
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("DELETE /connectors/{id}", func(w http.ResponseWriter, r *http.Request) {
		in, e := d.inspect(r.Context(), r.PathValue("id"))
		if is404(e) {
			jsonOut(w, 200, map[string]bool{"ok": true})
			return
		}
		if e != nil || in.Config.Labels["io.kzure.type"] != "service-connector" {
			jsonOut(w, 403, map[string]string{"error": "Connector ownership mismatch"})
			return
		}
		_, e = d.call(r.Context(), "DELETE", "/containers/"+in.ID+"?force=true", nil)
		if e != nil {
			jsonOut(w, 502, map[string]string{"error": e.Error()})
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	})
}

type connectorRequest struct {
	ID       string `json:"id"`
	TenantID string `json:"tenantId"`
	SourceID string `json:"sourceId"`
	TargetID string `json:"targetId"`
	Port     int    `json:"port"`
}

func (d *dockerClient) ensureConnector(ctx context.Context, p connectorRequest) error {
	if len(p.ID) != 32 || strings.Trim(p.ID, "0123456789abcdef") != "" {
		return errors.New("Invalid connector ID")
	}
	if p.Port < 1 || p.Port > 65535 {
		return errors.New("Unsupported service port")
	}
	source, e := d.inspect(ctx, p.SourceID)
	if e != nil {
		return e
	}
	target, e := d.inspect(ctx, p.TargetID)
	if e != nil {
		return e
	}
	if !source.State.Running || !target.State.Running || source.Config.Labels["io.kzure.tenant"] != p.TenantID || target.Config.Labels["io.kzure.tenant"] != p.TenantID {
		return errors.New("Connector requires two running resources in the same tenant")
	}
	if target.Config.Labels["io.kzure.type"] == "container-app" && target.Config.Labels["io.kzure.service-port"] != "" && target.Config.Labels["io.kzure.service-port"] != strconv.Itoa(p.Port) {
		return errors.New("Connector port does not match the application's declared service port")
	}
	sourceNet, _, e := primaryNetwork(source)
	if e != nil {
		return e
	}
	targetNet, targetIP, e := primaryNetwork(target)
	if e != nil {
		return e
	}
	address := net.JoinHostPort(targetIP, strconv.Itoa(p.Port))
	existing, e := d.inspect(ctx, p.ID)
	if e == nil {
		if existing.Config.Labels["io.kzure.type"] != "service-connector" {
			return errors.New("Connector ownership mismatch")
		}
		var cfg struct{ Config struct{ Cmd []string } }
		raw, _ := d.call(ctx, "GET", "/containers/"+existing.ID+"/json", nil)
		_ = json.Unmarshal(raw, &cfg)
		if len(cfg.Config.Cmd) == 2 && cfg.Config.Cmd[0] == address && existing.State.Running {
			return nil
		}
		if _, e = d.call(ctx, "DELETE", "/containers/"+existing.ID+"?force=true", nil); e != nil {
			return e
		}
	} else if !is404(e) {
		return e
	}
	image := d.imageLocks["kzure/bastion:0.1.0"]
	if image == "" {
		return errors.New("Bastion/connector image is not built")
	}
	labels := d.labels(Resource{ID: p.ID, TenantID: p.TenantID, Type: "service-connector"})
	cfg := map[string]any{"Image": image, "Labels": labels, "Entrypoint": []string{"kzure", "connector"}, "Cmd": []string{address, strconv.Itoa(p.Port)}, "HostConfig": map[string]any{"NetworkMode": sourceNet, "NanoCpus": int64(100000000), "Memory": int64(64 * 1024 * 1024), "PidsLimit": 64, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}, "RestartPolicy": map[string]string{"Name": "unless-stopped"}, "LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "2m", "max-file": "2"}}}, "NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{sourceNet: map[string]any{"Aliases": []string{"service-" + p.ID + ".kzure"}}}}}
	if strings.HasSuffix(sourceNet, "-subnet") {
		ip, e := d.infrastructureAddress(ctx, sourceNet)
		if e != nil {
			return e
		}
		cfg["NetworkingConfig"] = map[string]any{"EndpointsConfig": map[string]any{sourceNet: map[string]any{"Aliases": []string{"service-" + p.ID + ".kzure"}, "IPAMConfig": map[string]string{"IPv4Address": ip}}}}
	}
	raw, e := d.call(ctx, "POST", "/containers/create?name="+d.name(p.ID), cfg)
	if e != nil {
		return e
	}
	var created struct{ ID string }
	if e = json.Unmarshal(raw, &created); e != nil {
		return e
	}
	if targetNet != sourceNet {
		if e = d.connectNetwork(ctx, created.ID, targetNet, nil); e != nil {
			return e
		}
	}
	_, e = d.call(ctx, "POST", "/containers/"+created.ID+"/start", nil)
	return e
}

func (d *dockerClient) remoteAgentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /resources/{id}/ssh-keys", func(w http.ResponseWriter, r *http.Request) {
		in, e := d.inspect(r.Context(), r.PathValue("id"))
		if e != nil || !isSSHWorkspace(in.Config.Labels["io.kzure.type"]) {
			jsonOut(w, 404, map[string]string{"error": "SSH workspace not found"})
			return
		}
		var p struct {
			Keys []string `json:"keys"`
		}
		if e = decode(r, &p); e != nil || len(p.Keys) > 100 {
			jsonOut(w, 400, map[string]string{"error": "Invalid key list"})
			return
		}
		keys := []string{}
		for _, key := range p.Keys {
			normalized, _, err := normalizeSSHKey(key)
			if err != nil {
				jsonOut(w, 400, map[string]string{"error": err.Error()})
				return
			}
			keys = append(keys, normalized)
		}
		e = d.archive(r.Context(), in.ID, "/run/kzure", "authorized_keys", []byte(strings.Join(keys, "\n")+"\n"), 0644)
		if e != nil {
			jsonOut(w, 502, map[string]string{"error": e.Error()})
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /resources/{id}/bastion-targets", func(w http.ResponseWriter, r *http.Request) {
		in, e := d.inspect(r.Context(), r.PathValue("id"))
		if e != nil || in.Config.Labels["io.kzure.type"] != "bastion" {
			jsonOut(w, 404, map[string]string{"error": "Bastion not found"})
			return
		}
		var p struct {
			Targets []string `json:"targets"`
		}
		if e = decode(r, &p); e != nil || len(p.Targets) > 32 {
			jsonOut(w, 400, map[string]string{"error": "Invalid targets"})
			return
		}
		desired := map[string]bool{d.name(r.PathValue("id")) + "-net": true, env("KZURE_ACCESS_NETWORK", "kzure_access-control"): true}
		for _, rid := range p.Targets {
			target, err := d.inspect(r.Context(), rid)
			if err != nil || target.Config.Labels["io.kzure.tenant"] != in.Config.Labels["io.kzure.tenant"] {
				jsonOut(w, 400, map[string]string{"error": "Target unavailable or outside tenant"})
				return
			}
			network, _, err := primaryNetwork(target)
			if err != nil {
				jsonOut(w, 400, map[string]string{"error": err.Error()})
				return
			}
			desired[network] = true
			if err = d.connectNetwork(r.Context(), in.ID, network, nil); err != nil {
				jsonOut(w, 502, map[string]string{"error": err.Error()})
				return
			}
		}
		if subnetNet, _, err := primaryNetwork(in); err == nil && strings.HasSuffix(subnetNet, "-subnet") {
			desired[subnetNet] = true
		}
		for network := range in.NetworkSettings.Networks {
			if !desired[network] {
				if _, e = d.call(r.Context(), "POST", "/networks/"+network+"/disconnect", map[string]any{"Container": in.ID, "Force": true}); e != nil {
					jsonOut(w, 502, map[string]string{"error": e.Error()})
					return
				}
			}
		}
		if e = d.attachGuacdTargets(r.Context(), r.PathValue("id"), in.Config.Labels["io.kzure.tenant"], p.Targets); e != nil {
			jsonOut(w, 502, map[string]string{"error": e.Error()})
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	})
}

var _ = fmt.Sprint
