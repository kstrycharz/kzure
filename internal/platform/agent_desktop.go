package platform

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

const guacdImage = "guacamole/guacd@sha256:8974eaa9ba32f713daf311e7cc8cd7e4cdfba1edea39eed75524e78ef4b08f4f"

func (d *dockerClient) bastionComponentAction(ctx context.Context, rid, action string) error {
	name := d.name(rid) + "-guacd"
	raw, e := d.call(ctx, "GET", "/containers/"+name+"/json", nil)
	if !is404(e) {
		if e != nil {
			return e
		}
		var in inspection
		if e = json.Unmarshal(raw, &in); e != nil {
			return e
		}
		if in.Config.Labels["io.kzure.installation"] != d.install || in.Config.Labels["io.kzure.resource"] != rid {
			return errors.New("Bastion component ownership mismatch")
		}
		switch action {
		case "stop":
			if in.State.Running {
				_, e = d.call(ctx, "POST", "/containers/"+in.ID+"/stop?t=5", nil)
			}
		case "restart":
			_, e = d.call(ctx, "POST", "/containers/"+in.ID+"/restart?t=5", nil)
		case "delete":
			_, e = d.call(ctx, "DELETE", "/containers/"+in.ID+"?force=true", nil)
		}
		if e != nil {
			return e
		}
	}
	if action == "delete" {
		network := d.name(rid) + "-transport"
		if _, e = d.call(ctx, "GET", "/networks/"+network, nil); !is404(e) {
			if e != nil {
				return e
			}
			self, err := os.Hostname()
			if err != nil {
				return err
			}
			if _, e = d.call(ctx, "POST", "/networks/"+network+"/disconnect", map[string]any{"Container": self, "Force": true}); e != nil {
				return e
			}
			_, e = d.call(ctx, "DELETE", "/networks/"+network, nil)
			if e != nil {
				return e
			}
		}
	}
	return nil
}

func (d *dockerClient) ensureGuacd(ctx context.Context, bid, tid string) (string, error) {
	network := d.name(bid) + "-transport"
	labels := d.labels(Resource{ID: bid, TenantID: tid, Type: "bastion-transport"})
	if e := d.ensureOwned(ctx, "/networks", network, labels, map[string]any{"Driver": "bridge", "Internal": true, "EnableIPv6": false}); e != nil {
		return "", e
	}
	self, e := os.Hostname()
	if e != nil {
		return "", e
	}
	if e = d.connectNetwork(ctx, self, network, nil); e != nil {
		return "", e
	}
	name := d.name(bid) + "-guacd"
	raw, e := d.call(ctx, "GET", "/containers/"+name+"/json", nil)
	if e == nil {
		var in inspection
		if e = json.Unmarshal(raw, &in); e != nil {
			return "", e
		}
		if in.Config.Labels["io.kzure.installation"] != d.install || in.Config.Labels["io.kzure.resource"] != bid {
			return "", errors.New("Protocol proxy ownership mismatch")
		}
		if !in.State.Running {
			if _, e = d.call(ctx, "POST", "/containers/"+in.ID+"/start", nil); e != nil {
				return "", e
			}
		}
		return in.NetworkSettings.Networks[network].IPAddress, nil
	}
	if !is404(e) {
		return "", e
	}
	// Derive the total resource budget from the native gateway's installed limits.
	native, e := d.inspect(ctx, bid)
	if e != nil {
		return "", e
	}
	raw, e = d.call(ctx, "GET", "/containers/"+native.ID+"/json", nil)
	if e != nil {
		return "", e
	}
	var budget struct {
		HostConfig struct{ NanoCpus, Memory int64 }
	}
	_ = json.Unmarshal(raw, &budget)
	totalCPU, totalMem := budget.HostConfig.NanoCpus, budget.HostConfig.Memory
	if stored, e := strconv.ParseInt(native.Config.Labels["io.kzure.total-cpu"], 10, 64); e == nil {
		totalCPU = stored
	}
	if stored, e := strconv.ParseInt(native.Config.Labels["io.kzure.total-memory"], 10, 64); e == nil {
		totalMem = stored
	}
	if _, e = d.call(ctx, "POST", "/containers/"+native.ID+"/update", map[string]any{"NanoCpus": totalCPU / 2, "Memory": totalMem / 4, "MemorySwap": totalMem / 4}); e != nil {
		return "", e
	}
	networkRaw, e := d.call(ctx, "GET", "/networks/"+network, nil)
	if e != nil {
		return "", e
	}
	var info struct {
		IPAM struct{ Config []struct{ Subnet string } }
	}
	if e = json.Unmarshal(networkRaw, &info); e != nil || len(info.IPAM.Config) == 0 {
		return "", errors.New("Missing transport IPAM")
	}
	prefix, e := netip.ParsePrefix(info.IPAM.Config[0].Subnet)
	if e != nil {
		return "", e
	}
	bindIP := prefix.Addr().Next().Next().Next().String()
	cfg := map[string]any{"Image": guacdImage, "Labels": labels, "Entrypoint": []string{"/opt/guacamole/sbin/guacd"}, "Cmd": []string{"-f", "-b", bindIP, "-L", "info"}, "Healthcheck": map[string]any{"Test": []string{"CMD-SHELL", "nc -z " + bindIP + " 4822 || exit 1"}}, "NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{network: map[string]any{"IPAMConfig": map[string]string{"IPv4Address": bindIP}}}}, "HostConfig": map[string]any{"NetworkMode": network, "NanoCpus": totalCPU / 2, "Memory": totalMem * 3 / 4, "MemorySwap": totalMem * 3 / 4, "PidsLimit": 128, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}, "RestartPolicy": map[string]string{"Name": "unless-stopped"}, "LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "5m", "max-file": "2"}}}}
	// Create before choosing its bind address; the private interface receives its
	// address at create time. Only this interface will accept guacd connections.
	raw, e = d.call(ctx, "POST", "/containers/create?name="+name, cfg)
	if e != nil {
		return "", e
	}
	var created struct{ ID string }
	_ = json.Unmarshal(raw, &created)
	raw, e = d.call(ctx, "GET", "/containers/"+created.ID+"/json", nil)
	if e != nil {
		return "", e
	}
	var in inspection
	_ = json.Unmarshal(raw, &in)
	ip := in.NetworkSettings.Networks[network].IPAddress
	// Docker assigns bridge addresses at start for some engines. A wrapper binds
	// to its first interface dynamically while target interfaces attach later.
	if ip == "" {
		ip = bindIP
	}
	_ = ip
	if _, e = d.call(ctx, "POST", "/containers/"+created.ID+"/start", nil); e != nil {
		return "", e
	}
	raw, e = d.call(ctx, "GET", "/containers/"+created.ID+"/json", nil)
	if e != nil {
		return "", e
	}
	_ = json.Unmarshal(raw, &in)
	return in.NetworkSettings.Networks[network].IPAddress, nil
}

func (d *dockerClient) attachGuacdTargets(ctx context.Context, bid, tid string, targets []string) error {
	if _, e := d.ensureGuacd(ctx, bid, tid); e != nil {
		return e
	}
	cid := d.name(bid) + "-guacd"
	desired := map[string]bool{d.name(bid) + "-transport": true}
	for _, rid := range targets {
		in, e := d.inspect(ctx, rid)
		if e != nil {
			return e
		}
		if in.Config.Labels["io.kzure.tenant"] != tid {
			return errors.New("Target tenant mismatch")
		}
		network, _, e := primaryNetwork(in)
		if e != nil {
			return e
		}
		desired[network] = true
		if e = d.connectNetwork(ctx, cid, network, nil); e != nil {
			return e
		}
	}
	raw, e := d.call(ctx, "GET", "/containers/"+cid+"/json", nil)
	if e != nil {
		return e
	}
	var in inspection
	_ = json.Unmarshal(raw, &in)
	for network := range in.NetworkSettings.Networks {
		if !desired[network] {
			if _, e = d.call(ctx, "POST", "/networks/"+network+"/disconnect", map[string]any{"Container": cid, "Force": true}); e != nil {
				return e
			}
		}
	}
	return nil
}

func (d *dockerClient) desktopCertificate(ctx context.Context, rid string) (string, error) {
	in, e := d.inspect(ctx, rid)
	if e != nil {
		return "", e
	}
	if in.Config.Labels["io.kzure.type"] != "desktop-ubuntu" {
		return "", errors.New("Not a desktop resource")
	}
	raw, e := d.call(ctx, "GET", "/containers/"+in.ID+"/archive?path=/data/.kzure-desktop/cert.pem", nil)
	if e != nil {
		return "", e
	}
	tr := tar.NewReader(bytes.NewReader(raw))
	if _, e = tr.Next(); e != nil {
		return "", e
	}
	cert, e := io.ReadAll(io.LimitReader(tr, 16384))
	if e != nil {
		return "", e
	}
	block, _ := pem.Decode(cert)
	if block == nil {
		return "", errors.New("Desktop certificate is invalid")
	}
	if _, e = x509.ParseCertificate(block.Bytes); e != nil {
		return "", e
	}
	hash := sha256.Sum256(block.Bytes)
	hexHash := hex.EncodeToString(hash[:])
	pairs := make([]string, 0, 32)
	for i := 0; i < len(hexHash); i += 2 {
		pairs = append(pairs, hexHash[i:i+2])
	}
	return strings.Join(pairs, ":"), nil
}

func guacEncode(parts ...string) string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = fmt.Sprintf("%d.%s", utf8.RuneCountInString(p), p)
	}
	return strings.Join(out, ",") + ";"
}

func guacRead(r *bufio.Reader) ([]string, error) {
	parts := []string{}
	total := 0
	for len(parts) < 256 {
		length, e := r.ReadString('.')
		if e != nil {
			return nil, e
		}
		if len(length) > 9 {
			return nil, errors.New("Invalid instruction length")
		}
		n, e := strconv.Atoi(strings.TrimSuffix(length, "."))
		if e != nil || n < 0 || n > 1048576 || total+n > 2097152 {
			return nil, errors.New("Instruction too large")
		}
		var value strings.Builder
		for i := 0; i < n; i++ {
			ch, _, e := r.ReadRune()
			if e != nil {
				return nil, e
			}
			value.WriteRune(ch)
		}
		total += n
		parts = append(parts, value.String())
		sep, e := r.ReadByte()
		if e != nil {
			return nil, e
		}
		if sep == ';' {
			return parts, nil
		}
		if sep != ',' {
			return nil, errors.New("Malformed instruction")
		}
	}
	return nil, errors.New("Too many instruction elements")
}

func (d *dockerClient) desktopAgentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /resources/{id}/desktop-certificate", func(w http.ResponseWriter, r *http.Request) {
		fp, e := d.desktopCertificate(r.Context(), r.PathValue("id"))
		if e != nil {
			jsonOut(w, 502, map[string]string{"error": e.Error()})
			return
		}
		jsonOut(w, 200, map[string]string{"fingerprint": fp})
	})
	mux.HandleFunc("GET /resources/{id}/desktop-stream", d.desktopAgentStream)
}

func (d *dockerClient) desktopAgentStream(w http.ResponseWriter, r *http.Request) {
	bid, rid := r.PathValue("id"), r.URL.Query().Get("target")
	b, e := d.inspect(r.Context(), bid)
	if e != nil || b.Config.Labels["io.kzure.type"] != "bastion" {
		jsonOut(w, 404, map[string]string{"error": "Bastion not found"})
		return
	}
	target, e := d.inspect(r.Context(), rid)
	if e != nil || target.Config.Labels["io.kzure.type"] != "desktop-ubuntu" || !target.State.Running || target.Config.Labels["io.kzure.tenant"] != b.Config.Labels["io.kzure.tenant"] {
		jsonOut(w, 403, map[string]string{"error": "Desktop not available"})
		return
	}
	network, ip, e := primaryNetwork(target)
	if e != nil {
		jsonOut(w, 502, map[string]string{"error": e.Error()})
		return
	}
	if _, ok := b.NetworkSettings.Networks[network]; !ok {
		jsonOut(w, 403, map[string]string{"error": "Desktop is not attached to this bastion"})
		return
	}
	proxyIP, e := d.ensureGuacd(r.Context(), bid, b.Config.Labels["io.kzure.tenant"])
	if e != nil {
		jsonOut(w, 502, map[string]string{"error": e.Error()})
		return
	}
	if e = d.connectNetwork(r.Context(), d.name(bid)+"-guacd", network, nil); e != nil {
		jsonOut(w, 502, map[string]string{"error": e.Error()})
		return
	}
	ws, e := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}).Upgrade(w, r, nil)
	if e != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(65536)
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var auth struct {
		Password string `json:"password"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
	}
	if e = ws.ReadJSON(&auth); e != nil || auth.Password == "" {
		return
	}
	if auth.Width < 640 || auth.Width > 2560 {
		auth.Width = 1280
	}
	if auth.Height < 480 || auth.Height > 1600 {
		auth.Height = 800
	}
	_ = ws.UnderlyingConn().SetDeadline(time.Time{})
	fp, e := d.desktopCertificate(r.Context(), rid)
	if e != nil {
		return
	}
	backend, e := net.DialTimeout("tcp", net.JoinHostPort(proxyIP, "4822"), 5*time.Second)
	if e != nil {
		return
	}
	defer backend.Close()
	_ = backend.SetDeadline(time.Now().Add(20 * time.Second))
	reader := bufio.NewReader(backend)
	_, _ = io.WriteString(backend, guacEncode("select", "rdp"))
	args, e := guacRead(reader)
	if e != nil || len(args) < 2 || args[0] != "args" {
		return
	}
	params := map[string]string{"hostname": ip, "port": "3389", "username": "kzure", "password": auth.Password, "security": "tls", "ignore-cert": "false", "cert-fingerprints": "sha256:" + fp, "disable-copy": "true", "disable-paste": "true", "enable-drive": "false", "enable-printing": "false", "disable-audio": "true", "disable-gfx": "true", "server-layout": "en-us-qwerty", "width": fmt.Sprint(auth.Width), "height": fmt.Sprint(auth.Height), "resize-method": "display-update", "color-depth": "24"}
	connect := []string{"connect"}
	for _, arg := range args[1:] {
		value := params[arg]
		if strings.HasPrefix(arg, "VERSION_") {
			value = "VERSION_1_5_0"
		}
		connect = append(connect, value)
	}
	for _, message := range []string{guacEncode("size", fmt.Sprint(auth.Width), fmt.Sprint(auth.Height), "96"), guacEncode("audio"), guacEncode("video"), guacEncode("image", "image/png", "image/jpeg"), guacEncode(connect...)} {
		if _, e = io.WriteString(backend, message); e != nil {
			return
		}
	}
	ready, e := guacRead(reader)
	if e != nil {
		return
	}
	if ready[0] != "ready" {
		_ = ws.WriteMessage(websocket.TextMessage, []byte(guacEncode(ready...)))
		return
	}
	_ = backend.SetDeadline(time.Time{})
	if e = ws.WriteMessage(websocket.TextMessage, []byte(guacEncode("", ready[1]))); e != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, raw, e := ws.ReadMessage()
			if e != nil {
				return
			}
			input := bufio.NewReader(bytes.NewReader(raw))
			for input.Buffered() > 0 || input.Size() > 0 {
				if _, e = input.Peek(1); e != nil {
					break
				}
				parts, e := guacRead(input)
				if e != nil || len(parts) == 0 {
					return
				}
				switch parts[0] {
				case "key", "mouse", "size", "sync", "ack", "nop", "disconnect":
				default:
					return
				}
				_ = backend.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if _, e = io.WriteString(backend, guacEncode(parts...)); e != nil {
					return
				}
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			parts, e := guacRead(reader)
			if e != nil {
				return
			}
			_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if e = ws.WriteMessage(websocket.TextMessage, []byte(guacEncode(parts...))); e != nil {
				return
			}
		}
	}()
	<-done
}
