package platform

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

func ingressID(install string) string {
	sum := sha256.Sum256([]byte(install + ":application-ingress"))
	return hex.EncodeToString(sum[:16])
}

func (d *dockerClient) ingressAgentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /ingress", func(w http.ResponseWriter, r *http.Request) {
		var in ingressRequest
		if err := decode(r, &in); err != nil {
			jsonOut(w, 400, map[string]string{"error": err.Error()})
			return
		}
		status, err := d.ensureIngress(r.Context(), in)
		if err != nil {
			jsonOut(w, 502, map[string]string{"error": err.Error()})
			return
		}
		jsonOut(w, 200, status)
	})
}

func (d *dockerClient) ensureIngress(ctx context.Context, p ingressRequest) (edgeStatus, error) {
	d.ingressMu.Lock()
	defer d.ingressMu.Unlock()
	if len(p.Targets) > 128 || p.HTTPPort < 1024 || p.HTTPPort > 65535 || p.HTTPSPort < 1024 || p.HTTPSPort > 65535 || p.HTTPPort == p.HTTPSPort {
		return edgeStatus{}, errors.New("invalid application gateway limits")
	}
	image := d.imageLocks["kzure/ingress:0.1.0"]
	if image == "" {
		return edgeStatus{}, errors.New("Build and lock the application ingress image before creating an endpoint")
	}
	rid := ingressID(d.install)
	labels := d.labels(Resource{ID: rid, Type: "application-ingress"})
	volume, network := d.name(rid)+"-data", d.name(rid)+"-edge"
	if err := d.ensureOwned(ctx, "/volumes", volume, labels, map[string]any{}); err != nil {
		return edgeStatus{}, err
	}
	if err := d.ensureOwned(ctx, "/networks", network, labels, map[string]any{"Driver": "bridge", "Internal": false, "EnableIPv6": false}); err != nil {
		return edgeStatus{}, err
	}
	in, err := d.inspect(ctx, rid)
	if err == nil && in.Config.Image != image {
		if _, err = d.call(ctx, "DELETE", "/containers/"+in.ID+"?force=true", nil); err != nil {
			return edgeStatus{}, err
		}
		err = dockerError{status: 404}
	}
	if is404(err) {
		cfg := map[string]any{"Image": image, "Labels": labels, "ExposedPorts": map[string]any{"8080/tcp": map[string]any{}, "8443/tcp": map[string]any{}}, "HostConfig": map[string]any{"NetworkMode": network, "Binds": []string{volume + ":/data"}, "PortBindings": map[string]any{"8080/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": strconv.Itoa(p.HTTPPort)}}, "8443/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": strconv.Itoa(p.HTTPSPort)}}}, "Memory": int64(128 * 1024 * 1024), "NanoCpus": int64(500000000), "PidsLimit": 128, "ReadonlyRootfs": true, "Tmpfs": map[string]string{"/tmp": "rw,noexec,nosuid,size=16m"}, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}, "RestartPolicy": map[string]string{"Name": "unless-stopped"}, "LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "5m", "max-file": "2"}}}}
		if _, err = d.call(ctx, "POST", "/containers/create?name="+d.name(rid), cfg); err != nil {
			return edgeStatus{}, err
		}
		in, err = d.inspect(ctx, rid)
	}
	if err != nil {
		return edgeStatus{}, err
	}
	config := edgeConfig{HTTPSPort: p.HTTPSPort, Routes: []edgeRoute{}}
	desiredNetworks := map[string]bool{network: true}
	for _, target := range p.Targets {
		res := target.Resource
		if !validApplicationHostname(target.Hostname) || (res.Type != "static-web" && (res.Type != "container-app" || res.Spec.Mode == "worker")) {
			return edgeStatus{}, errors.New("invalid managed endpoint resource")
		}
		route := edgeRoute{Hostname: target.Hostname, ResourceID: res.ID}
		if res.State != "Running" {
			config.Routes = append(config.Routes, route)
			continue
		}
		workload, e := d.inspect(ctx, res.ID)
		if e == nil {
			if workload.Config.Labels["io.kzure.tenant"] != res.TenantID || workload.Config.Labels["io.kzure.type"] != res.Type {
				return edgeStatus{}, errors.New("application route ownership mismatch")
			}
			name, ip, e := primaryNetwork(workload)
			if e != nil {
				// Stopped and starting containers may have no assigned address.
				// Withdraw just this route while preserving unrelated applications.
				config.Routes = append(config.Routes, route)
				continue
			}
			if e = d.connectNetwork(ctx, in.ID, name, nil); e != nil {
				return edgeStatus{}, e
			}
			desiredNetworks[name] = true
			if res.State == "Running" && workload.State.Running && (workload.State.Health == nil || workload.State.Health.Status == "healthy") {
				port := appPort(res)
				if port == 0 {
					return edgeStatus{}, errors.New("application has no HTTP service port")
				}
				route.Upstream = net.JoinHostPort(ip, strconv.Itoa(port))
			}
		} else if !is404(e) {
			return edgeStatus{}, e
		}
		config.Routes = append(config.Routes, route)
	}
	config.Generation = edgeGeneration(config)
	if _, err = validateEdgeConfig(config); err != nil {
		return edgeStatus{}, err
	}
	// Only a fully validated complete snapshot replaces the last working file.
	bytes, _ := json.Marshal(config)
	if err = d.archive(ctx, in.ID, "/data", "routes.json", bytes, 0644); err != nil {
		return edgeStatus{}, err
	}
	if !in.State.Running {
		if _, err = d.call(ctx, "POST", "/containers/"+in.ID+"/start", nil); err != nil {
			return edgeStatus{}, err
		}
	}
	deadline := time.Now().Add(12 * time.Second)
	var status edgeStatus
	for time.Now().Before(deadline) {
		raw, e := d.runContainerCommand(ctx, in.ID, []string{"kzure", "edge-status"})
		if e == nil && json.Unmarshal(raw, &status) == nil && status.Generation == config.Generation {
			// Remove stale workload attachments only after the new route table is live.
			fresh, e := d.inspect(ctx, rid)
			if e != nil {
				return edgeStatus{}, e
			}
			for name := range fresh.NetworkSettings.Networks {
				if !desiredNetworks[name] {
					if _, e = d.call(ctx, "POST", "/networks/"+name+"/disconnect", map[string]any{"Container": in.ID, "Force": true}); e != nil {
						return edgeStatus{}, e
					}
				}
			}
			return status, nil
		}
		select {
		case <-ctx.Done():
			return edgeStatus{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return edgeStatus{}, errors.New("application gateway did not acknowledge the new route generation")
}

func (d *dockerClient) runContainerCommand(ctx context.Context, cid string, command []string) ([]byte, error) {
	raw, err := d.call(ctx, "POST", "/containers/"+cid+"/exec", map[string]any{"AttachStdout": true, "AttachStderr": true, "Tty": false, "User": "10001:10001", "Cmd": command})
	if err != nil {
		return nil, err
	}
	var exec struct{ ID string }
	if err = json.Unmarshal(raw, &exec); err != nil {
		return nil, err
	}
	raw, err = d.call(ctx, "POST", "/exec/"+exec.ID+"/start", map[string]bool{"Detach": false, "Tty": false})
	if err != nil {
		return nil, err
	}
	output := []byte{}
	for len(raw) >= 8 {
		n := int(binary.BigEndian.Uint32(raw[4:8]))
		if n > len(raw)-8 {
			return nil, errors.New("invalid gateway status output")
		}
		output = append(output, raw[8:8+n]...)
		raw = raw[8+n:]
	}
	status, err := d.call(ctx, "GET", "/exec/"+exec.ID+"/json", nil)
	if err != nil {
		return nil, err
	}
	var result struct{ ExitCode int }
	if err = json.Unmarshal(status, &result); err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("gateway command failed (%d)", result.ExitCode)
	}
	return output, nil
}
