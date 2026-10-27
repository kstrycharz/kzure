package platform

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type dockerClient struct {
	http                *http.Client
	install, publicHost string
	imageLocks          map[string]string
	policyMu            sync.Mutex
	policyGeneration    string
}
type dockerError struct {
	status  int
	message string
}

func (e dockerError) Error() string { return e.message }
func newDocker(a *App) *dockerClient {
	socket := env("DOCKER_SOCKET", "/var/run/docker.sock")
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	locks := map[string]string{}
	if b, e := os.ReadFile(env("KZURE_IMAGES_LOCK", "/config/images.lock.json")); e == nil {
		_ = json.Unmarshal(b, &locks)
	}
	return &dockerClient{http: &http.Client{Transport: tr, Timeout: 8 * time.Minute}, install: a.InstallID, publicHost: env("KZURE_PUBLIC_HOST", "127.0.0.1"), imageLocks: locks}
}
func (d *dockerClient) call(ctx context.Context, method, path string, input any) ([]byte, error) {
	var body io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return nil, e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, "http://docker/v1.47"+path, body)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	res, e := d.http.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	limit := int64(8 * 1024 * 1024)
	if strings.HasPrefix(path, "/images/create") {
		limit = 128 * 1024 * 1024
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, limit))
	if e != nil {
		return nil, e
	}
	if res.StatusCode >= 400 {
		var out struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &out)
		return nil, dockerError{res.StatusCode, out.Message}
	}
	return b, nil
}
func (d *dockerClient) labels(r Resource) map[string]string {
	return map[string]string{"io.kzure.installation": d.install, "io.kzure.resource": r.ID, "io.kzure.tenant": r.TenantID, "io.kzure.type": r.Type}
}
func (d *dockerClient) name(rid string) string { return "kzure-" + d.install[:8] + "-" + rid }
func is404(e error) bool                       { var de dockerError; return errors.As(e, &de) && de.status == 404 }

type inspection struct {
	ID    string `json:"Id"`
	State struct {
		Status  string
		Running bool
		Health  *struct{ Status string }
	}
	Config struct {
		Labels                  map[string]string
		Image, User, WorkingDir string
	}
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string
			Aliases   []string
		}
		Ports map[string][]struct{ HostIP, HostPort string }
	}
	Mounts []struct{ Name, Destination string }
}

func (d *dockerClient) inspect(ctx context.Context, rid string) (inspection, error) {
	var in inspection
	b, e := d.call(ctx, "GET", "/containers/"+d.name(rid)+"/json", nil)
	if e != nil {
		return in, e
	}
	if e = json.Unmarshal(b, &in); e != nil {
		return in, e
	}
	if in.Config.Labels["io.kzure.installation"] != d.install || in.Config.Labels["io.kzure.resource"] != rid {
		return in, errors.New("container ownership mismatch")
	}
	return in, nil
}
func (d *dockerClient) observation(in inspection) Observation {
	o := Observation{ContainerID: in.ID, State: "Stopped"}
	if in.State.Running {
		o.State = "Running"
	}
	if in.State.Health != nil {
		o.Health = in.State.Health.Status
		if o.Health == "unhealthy" {
			o.State = "Degraded"
		}
	}
	for port, bindings := range in.NetworkSettings.Ports {
		if len(bindings) > 0 {
			scheme := "tcp"
			if strings.HasPrefix(port, "8080/") || strings.HasPrefix(port, "8333/") {
				scheme = "http"
			}
			o.Endpoint = scheme + "://" + d.publicHost + ":" + bindings[0].HostPort
			break
		}
	}
	for _, m := range in.Mounts {
		if m.Name != "" {
			o.Volume = m.Name
			break
		}
	}
	return o
}
func (d *dockerClient) ensureOwned(ctx context.Context, path, name string, labels map[string]string, extra map[string]any) error {
	b, e := d.call(ctx, "GET", path+"/"+name, nil)
	if e == nil {
		var in struct{ Labels map[string]string }
		if e = json.Unmarshal(b, &in); e != nil {
			return e
		}
		if in.Labels["io.kzure.installation"] != d.install || in.Labels["io.kzure.resource"] != labels["io.kzure.resource"] {
			return errors.New("managed object ownership mismatch")
		}
		return nil
	}
	if !is404(e) {
		return e
	}
	extra["Name"] = name
	extra["Labels"] = labels
	_, e = d.call(ctx, "POST", path+"/create", extra)
	return e
}
func (d *dockerClient) archive(ctx context.Context, cname, path, name string, data []byte, mode int64) error {
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	if e := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data))}); e != nil {
		return e
	}
	if _, e := tw.Write(data); e != nil {
		return e
	}
	if e := tw.Close(); e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "PUT", "http://docker/v1.47/containers/"+cname+"/archive?path="+url.QueryEscape(path), &b)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/x-tar")
	res, e := d.http.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("write container files failed: %s", raw)
	}
	return nil
}
func (d *dockerClient) ensure(ctx context.Context, p ProvisionRequest) (Observation, error) {
	r := p.Resource
	c, e := Item(r.Type)
	if e != nil || !c.Available {
		return Observation{}, errors.New("unsupported provider")
	}
	if e = validateSpec(r.Type, r.Name, r.Spec); e != nil {
		return Observation{}, e
	}
	in, e := d.inspect(ctx, r.ID)
	if e == nil {
		if !in.State.Running {
			if _, e = d.call(ctx, "POST", "/containers/"+in.ID+"/start", nil); e != nil {
				return Observation{}, e
			}
		}
		in, e = d.inspect(ctx, r.ID)
		return d.observation(in), e
	}
	if !is404(e) {
		return Observation{}, e
	}
	image := c.Image
	if locked := d.imageLocks[image]; locked != "" {
		image = locked
	}
	if _, e = d.call(ctx, "GET", "/images/"+url.PathEscape(image)+"/json", nil); e != nil {
		if !is404(e) {
			return Observation{}, e
		}
		if strings.HasPrefix(c.Image, "kzure/") {
			return Observation{}, errors.New("local catalog image is missing; run the Kzure image build script")
		}
		b, e := d.call(ctx, "POST", "/images/create?fromImage="+url.QueryEscape(image), nil)
		if e != nil {
			return Observation{}, e
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		for dec.More() {
			var event map[string]any
			if e = dec.Decode(&event); e != nil {
				break
			}
			if msg, ok := event["error"].(string); ok {
				return Observation{}, errors.New(msg)
			}
		}
	}
	labels := d.labels(r)
	network := d.name(r.ID) + "-net"
	if p.Network != nil {
		network = d.name(p.Network.ID) + "-subnet"
		if e = d.ensureSubnet(ctx, p.Network.ID, r.TenantID, p.Network.CIDR); e != nil {
			return Observation{}, e
		}
	} else {
		if e = d.ensureOwned(ctx, "/networks", network, labels, map[string]any{"Driver": "bridge", "CheckDuplicate": true, "Internal": isSSHWorkspace(r.Type) || r.Type == "desktop-ubuntu"}); e != nil {
			return Observation{}, e
		}
	}
	volume := ""
	binds := []string{}
	if c.Persistent {
		volume = d.name(r.ID) + "-data"
		if e = d.ensureOwned(ctx, "/volumes", volume, labels, map[string]any{}); e != nil {
			return Observation{}, e
		}
		mount := "/data"
		if r.Type == "postgres" {
			mount = "/var/lib/postgresql"
		}
		binds = append(binds, volume+":"+mount)
	}
	cmd := c.Command
	environment := []string{}
	security := []string{"no-new-privileges:true"}
	user := ""
	if strings.HasPrefix(r.Type, "workspace-") && !isSSHWorkspace(r.Type) {
		user = "1000:1000"
	}
	switch r.Type {
	case "postgres":
		environment = []string{"POSTGRES_USER=kzure", "POSTGRES_DB=app", "POSTGRES_PASSWORD=" + p.Secrets["password"]}
	case "valkey":
		cmd = []string{"valkey-server", "/tmp/kzure-valkey.conf"}
	case "storage":
		cmd = []string{"server", "-dir=/data", "-s3", "-s3.port=8333", "-s3.config=/etc/seaweedfs/s3.json", "-ip.bind=0.0.0.0", "-volume.max=64", "-master.volumeSizeLimitMB=1024", "-volume.preStopSeconds=1"}
	}
	hc := map[string]any{"NanoCpus": int64(r.Spec.CPU * 1e9), "Memory": int64(r.Spec.RAM * 1024 * 1024 * 1024), "MemorySwap": int64(r.Spec.RAM * 1024 * 1024 * 1024), "PidsLimit": 256, "NetworkMode": network, "Binds": binds, "Init": true, "SecurityOpt": security, "CapDrop": []string{"NET_RAW", "SYS_ADMIN"}, "RestartPolicy": map[string]any{"Name": "unless-stopped"}, "LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "5m", "max-file": "2"}}}
	cfg := map[string]any{"Image": image, "Labels": labels, "HostConfig": hc, "Env": environment}
	endpoint := map[string]any{"Aliases": []string{"r-" + r.ID + ".kzure"}}
	if p.Network != nil {
		endpoint["IPAMConfig"] = map[string]string{"IPv4Address": p.Network.Address}
	}
	cfg["NetworkingConfig"] = map[string]any{"EndpointsConfig": map[string]any{network: endpoint}}
	if r.Type == "bastion" {
		hc["CapDrop"] = []string{"ALL"}
		hc["NanoCpus"] = int64(r.Spec.CPU * 1e9 * .5)
		hc["Memory"] = int64(r.Spec.RAM * 1024 * 1024 * 1024 * .25)
		hc["MemorySwap"] = hc["Memory"]
		labels["io.kzure.total-cpu"] = fmt.Sprint(int64(r.Spec.CPU * 1e9))
		labels["io.kzure.total-memory"] = fmt.Sprint(int64(r.Spec.RAM * 1024 * 1024 * 1024))
	}
	if len(cmd) > 0 {
		cfg["Cmd"] = cmd
	}
	if user != "" {
		cfg["User"] = user
	}
	if c.Port > 0 {
		port := fmt.Sprintf("%d/tcp", c.Port)
		cfg["ExposedPorts"] = map[string]any{port: map[string]any{}}
		hc["PortBindings"] = map[string]any{port: []map[string]string{{"HostIp": "127.0.0.1", "HostPort": ""}}}
	}
	b, e := d.call(ctx, "POST", "/containers/create?name="+d.name(r.ID), cfg)
	if e != nil {
		return Observation{}, e
	}
	var created struct {
		ID string `json:"Id"`
	}
	if e = json.Unmarshal(b, &created); e != nil {
		return Observation{}, e
	}
	// Configuration files are written before start; if setup fails, remove the incomplete container and retry safely.
	if r.Type == "desktop-ubuntu" {
		if e = d.archive(ctx, created.ID, "/run/kzure", "password", []byte(p.Secrets["password"]), 0600); e != nil {
			return Observation{}, e
		}
	}
	configured := false
	defer func() {
		if !configured {
			_, _ = d.call(context.Background(), "DELETE", "/containers/"+created.ID+"?force=true", nil)
		}
	}()
	if r.Type == "static-web" && r.Spec.HTML != "" {
		if e = d.archive(ctx, created.ID, "/usr/share/nginx/html", "index.html", []byte(r.Spec.HTML), 0644); e != nil {
			return Observation{}, e
		}
	}
	if r.Type == "valkey" {
		conf := fmt.Sprintf("bind 0.0.0.0\nprotected-mode yes\nrequirepass %s\ndir /data\nappendonly yes\nmaxmemory %d\nmaxmemory-policy allkeys-lru\n", p.Secrets["password"], int64(r.Spec.RAM*1024*1024*1024*.7))
		if e = d.archive(ctx, created.ID, "/tmp", "kzure-valkey.conf", []byte(conf), 0644); e != nil {
			return Observation{}, e
		}
	}
	if r.Type == "storage" {
		auth, _ := json.Marshal(map[string]any{"identities": []any{map[string]any{"name": "account-owner", "credentials": []any{map[string]string{"accessKey": p.Secrets["accessKey"], "secretKey": p.Secrets["secretKey"]}}, "actions": []string{"Admin", "Read", "List", "Tagging", "Write"}}}})
		if e = d.archive(ctx, created.ID, "/etc/seaweedfs", "s3.json", auth, 0644); e != nil {
			return Observation{}, e
		}
	}
	if isSSHWorkspace(r.Type) {
		if e = d.archive(ctx, created.ID, "/run/kzure", "authorized_keys", []byte(strings.Join(p.SSHKeys, "\n")+"\n"), 0644); e != nil {
			return Observation{}, e
		}
		if e = d.archive(ctx, created.ID, "/run/kzure", "host_key", []byte(p.Secrets["hostKey"]), 0600); e != nil {
			return Observation{}, e
		}
	}
	if r.Type == "bastion" {
		conf, _ := json.Marshal(BastionConfig{ID: r.ID, API: "http://api:8090", Token: p.Secrets["controlToken"], HostKey: p.Secrets["hostKey"]})
		if e = d.archive(ctx, created.ID, "/run/kzure", "bastion.json", conf, 0444); e != nil {
			return Observation{}, e
		}
		if e = d.connectNetwork(ctx, created.ID, env("KZURE_ACCESS_NETWORK", "kzure_access-control"), nil); e != nil {
			return Observation{}, e
		}
	}
	if _, e = d.call(ctx, "POST", "/containers/"+created.ID+"/start", nil); e != nil {
		return Observation{}, e
	}
	configured = true
	deadline := time.Now().Add(45 * time.Second)
	for {
		in, e = d.inspect(ctx, r.ID)
		if e != nil {
			return Observation{}, e
		}
		if !in.State.Running {
			if time.Now().Before(deadline) {
				time.Sleep(time.Second)
				continue
			}
			return Observation{}, errors.New("container failed to stay running; inspect resource logs")
		}
		if in.State.Health != nil && in.State.Health.Status != "healthy" {
			if time.Now().Before(deadline) {
				time.Sleep(time.Second)
				continue
			}
			return Observation{}, errors.New("container did not pass readiness checks")
		}
		if in.State.Health == nil && time.Now().Add(-3*time.Second).Before(deadline.Add(-45*time.Second)) {
			time.Sleep(time.Second)
			continue
		}
		return d.observation(in), nil
	}
}
func (d *dockerClient) act(ctx context.Context, rid, action string) (Observation, error) {
	in, e := d.inspect(ctx, rid)
	missing := is404(e)
	if e != nil && !missing {
		return Observation{}, e
	}
	switch action {
	case "stop":
		if !missing && in.Config.Labels["io.kzure.type"] == "bastion" {
			if e = d.bastionComponentAction(ctx, rid, "stop"); e != nil {
				return Observation{}, e
			}
		}
		if missing {
			return Observation{State: "Stopped"}, nil
		}
		if in.State.Running {
			if _, e = d.call(ctx, "POST", "/containers/"+in.ID+"/stop?t=15", nil); e != nil {
				return Observation{}, e
			}
		}
		in, e = d.inspect(ctx, rid)
		return d.observation(in), e
	case "restart":
		if !missing && in.Config.Labels["io.kzure.type"] == "bastion" {
			if e = d.bastionComponentAction(ctx, rid, "restart"); e != nil {
				return Observation{}, e
			}
		}
		if missing {
			return Observation{}, errors.New("container is missing; use Start to reconcile")
		}
		if _, e = d.call(ctx, "POST", "/containers/"+in.ID+"/restart?t=15", nil); e != nil {
			return Observation{}, e
		}
		in, e = d.inspect(ctx, rid)
		return d.observation(in), e
	case "delete", "purge":
		if !missing && in.Config.Labels["io.kzure.type"] == "bastion" {
			if e = d.bastionComponentAction(ctx, rid, "delete"); e != nil {
				return Observation{}, e
			}
		}
		if !missing {
			if _, e = d.call(ctx, "DELETE", "/containers/"+in.ID+"?force=true", nil); e != nil {
				return Observation{}, e
			}
		}
		network := d.name(rid) + "-net"
		if b, err := d.call(ctx, "GET", "/networks/"+network, nil); err == nil {
			var n struct{ Labels map[string]string }
			_ = json.Unmarshal(b, &n)
			if n.Labels["io.kzure.installation"] != d.install || n.Labels["io.kzure.resource"] != rid {
				return Observation{}, errors.New("network ownership mismatch")
			}
			if _, e = d.call(ctx, "DELETE", "/networks/"+network, nil); e != nil {
				return Observation{}, e
			}
		} else if !is404(err) {
			return Observation{}, err
		}
		volume := d.name(rid) + "-data"
		b, err := d.call(ctx, "GET", "/volumes/"+volume, nil)
		if err != nil && !is404(err) {
			return Observation{}, err
		}
		if err == nil {
			var v struct{ Labels map[string]string }
			_ = json.Unmarshal(b, &v)
			if v.Labels["io.kzure.installation"] != d.install || v.Labels["io.kzure.resource"] != rid {
				return Observation{}, errors.New("volume ownership mismatch")
			}
			if action == "purge" {
				if _, e = d.call(ctx, "DELETE", "/volumes/"+volume, nil); e != nil {
					return Observation{}, e
				}
				volume = ""
			}
		} else {
			volume = ""
		}
		state := "Deleted"
		if volume != "" {
			state = "Retained"
		}
		return Observation{State: state, Volume: volume}, nil
	}
	return Observation{}, errors.New("unsupported action")
}
func (d *dockerClient) logs(ctx context.Context, rid string) (string, error) {
	in, e := d.inspect(ctx, rid)
	if e != nil {
		return "", e
	}
	b, e := d.call(ctx, "GET", "/containers/"+in.ID+"/logs?stdout=1&stderr=1&tail=150&timestamps=1", nil)
	if e != nil {
		return "", e
	}
	var out strings.Builder
	for len(b) >= 8 && b[0] <= 2 {
		n := int(binary.BigEndian.Uint32(b[4:8]))
		if n > len(b)-8 {
			break
		}
		out.Write(b[8 : 8+n])
		b = b[8+n:]
	}
	if out.Len() == 0 {
		return string(b), nil
	}
	return out.String(), nil
}
func (d *dockerClient) stats(ctx context.Context, rid string) (Observation, error) {
	in, e := d.inspect(ctx, rid)
	if e != nil {
		return Observation{}, e
	}
	o := d.observation(in)
	if !in.State.Running {
		return o, nil
	}
	b, e := d.call(ctx, "GET", "/containers/"+in.ID+"/stats?stream=false&one-shot=false", nil)
	if e != nil {
		return o, e
	}
	var s struct {
		Memory struct {
			Usage uint64 `json:"usage"`
		} `json:"memory_stats"`
		CPU struct {
			Usage struct {
				Total uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			System uint64 `json:"system_cpu_usage"`
			Online uint64 `json:"online_cpus"`
		} `json:"cpu_stats"`
		Pre struct {
			Usage struct {
				Total uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			System uint64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
	}
	if e = json.Unmarshal(b, &s); e != nil {
		return o, e
	}
	o.MemoryBytes = s.Memory.Usage
	if s.CPU.System > s.Pre.System && s.CPU.Usage.Total >= s.Pre.Usage.Total {
		o.CPUPercent = float64(s.CPU.Usage.Total-s.Pre.Usage.Total) / float64(s.CPU.System-s.Pre.System) * float64(s.CPU.Online) * 100
	}
	return o, nil
}
func (a *App) RunAgent() error {
	if len(a.InstallID) < 8 || len(a.AgentToken) < 32 {
		return errors.New("installation ID and strong agent token required")
	}
	d := newDocker(a)
	mux := http.NewServeMux()
	d.networkRoutes(mux)
	d.remoteAgentRoutes(mux)
	d.desktopAgentRoutes(mux)
	d.policyRoutes(mux)
	mux.HandleFunc("GET /resources/{id}/terminal", d.serveTerminal)
	mux.HandleFunc("GET /host", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		b, e := d.call(r.Context(), "GET", "/info", nil)
		if e != nil {
			return e
		}
		var in struct {
			Name                                         string
			NCPU                                         int
			MemTotal                                     uint64
			OperatingSystem, Architecture, ServerVersion string
		}
		if e = json.Unmarshal(b, &in); e != nil {
			return e
		}
		jsonOut(w, 200, Host{Name: in.Name, CPUs: in.NCPU, RAM: float64(in.MemTotal) / (1024 * 1024 * 1024), OS: in.OperatingSystem, Architecture: in.Architecture, Version: in.ServerVersion, ReservedCPU: 2, ReservedRAM: 6, Connected: true})
		return nil
	}))
	mux.HandleFunc("POST /ensure", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		var p ProvisionRequest
		if e := decode(r, &p); e != nil {
			return e
		}
		if len(p.Resource.ID) != 32 || len(p.Resource.TenantID) != 32 {
			return bad("invalid resource identity")
		}
		o, e := d.ensure(r.Context(), p)
		if e != nil {
			return apiError{502, e.Error()}
		}
		jsonOut(w, 200, o)
		return nil
	}))
	mux.HandleFunc("POST /resources/{id}/actions", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		rid := r.PathValue("id")
		if len(rid) != 32 || strings.ContainsAny(rid, "/\\.") {
			return bad("invalid resource identity")
		}
		var in struct {
			Action string `json:"action"`
		}
		if e := decode(r, &in); e != nil {
			return e
		}
		o, e := d.act(r.Context(), rid, in.Action)
		if e != nil {
			return apiError{502, e.Error()}
		}
		jsonOut(w, 200, o)
		return nil
	}))
	mux.HandleFunc("GET /resources/{id}/logs", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		out, e := d.logs(r.Context(), r.PathValue("id"))
		if e != nil {
			return apiError{502, e.Error()}
		}
		jsonOut(w, 200, map[string]string{"logs": out})
		return nil
	}))
	mux.HandleFunc("GET /resources/{id}/observe", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		o, e := d.stats(r.Context(), r.PathValue("id"))
		if e != nil {
			return apiError{502, e.Error()}
		}
		jsonOut(w, 200, o)
		return nil
	}))
	mux.HandleFunc("GET /resources/{id}/state", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		in, e := d.inspect(r.Context(), r.PathValue("id"))
		if is404(e) {
			jsonOut(w, 200, Observation{State: "Missing"})
			return nil
		}
		if e != nil {
			return apiError{502, e.Error()}
		}
		jsonOut(w, 200, d.observation(in))
		return nil
	}))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+a.AgentToken)) != 1 {
			jsonOut(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2*1024*1024)
		mux.ServeHTTP(w, r)
	})
	log.Printf("Node agent listening on :8091 (internal only)")
	return (&http.Server{Addr: ":8091", Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 9 * time.Minute}).ListenAndServe()
}
