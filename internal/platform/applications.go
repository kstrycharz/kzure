package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

type boundedApplicationLog struct{ strings.Builder }

func (w *boundedApplicationLog) WriteString(s string) (int, error) {
	remaining := 1024*1024 - w.Len()
	if len(s) > remaining {
		n, _ := w.Builder.WriteString(s[:remaining])
		return n, io.ErrShortWrite
	}
	return w.Builder.WriteString(s)
}

func (w *boundedApplicationLog) Write(p []byte) (int, error) {
	remaining := 1024*1024 - w.Len()
	if len(p) > remaining {
		n, _ := w.Builder.Write(p[:remaining])
		return n, io.ErrShortWrite
	}
	return w.Builder.Write(p)
}
func redactApplicationValues(output string, values []string) string {
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	pairs := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		if v != "" && !seen[v] {
			pairs = append(pairs, v, "[redacted]")
			seen[v] = true
		}
	}
	var result boundedApplicationLog
	// A single replacement pass avoids repeatedly expanding the redaction marker
	// when short secret values also occur inside that marker. Output is bounded.
	_, e := strings.NewReplacer(pairs...).WriteString(&result, output)
	if e != nil {
		return result.String() + "\n[output truncated]"
	}
	return result.String()
}

func (a *App) redactApplicationOutput(rid, output string) (string, error) {
	rows, e := a.DB.Query("SELECT secret_cipher FROM resources WHERE id=$1 UNION SELECT secret_cipher FROM (SELECT secret_cipher FROM resource_revisions WHERE resource_id=$1 ORDER BY revision DESC LIMIT 2) recent", rid)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var cipher string
		if e = rows.Scan(&cipher); e != nil {
			return "", e
		}
		plain, err := a.unseal(cipher)
		if err != nil {
			return "", err
		}
		for key, value := range plain {
			if strings.HasPrefix(key, "env:") && value != "" {
				values = append(values, value)
			}
		}
	}
	if e = rows.Err(); e != nil {
		return "", e
	}
	return redactApplicationValues(output, values), nil
}

const applicationSchema = `
ALTER TABLE resources ADD COLUMN IF NOT EXISTS revision integer NOT NULL DEFAULT 1;
ALTER TABLE resources ADD COLUMN IF NOT EXISTS assigned_port integer NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX IF NOT EXISTS active_application_port ON resources(assigned_port) WHERE assigned_port>0 AND state<>'Deleted';
CREATE TABLE IF NOT EXISTS resource_revisions(resource_id text NOT NULL REFERENCES resources(id),revision integer NOT NULL,spec jsonb NOT NULL,secret_cipher text NOT NULL,state text NOT NULL DEFAULT 'Applied',message text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(resource_id,revision));
INSERT INTO resource_revisions(resource_id,revision,spec,secret_cipher) SELECT id,revision,spec,secret_cipher FROM resources WHERE type='container-app' ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS approved_images(id text PRIMARY KEY,tenant_id text NOT NULL REFERENCES tenants(id),name text NOT NULL,reference text NOT NULL,image_id text NOT NULL,architecture text NOT NULL,os text NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(tenant_id,image_id));
CREATE TABLE IF NOT EXISTS workload_secrets(id text PRIMARY KEY,tenant_id text NOT NULL REFERENCES tenants(id),name text NOT NULL,secret_cipher text NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(tenant_id,name));
`

var imageIdentity = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
var imageReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,255}$`)

func validImageReference(ref string) bool {
	if !imageReference.MatchString(ref) || strings.Contains(ref, "://") {
		return false
	}
	parts := strings.Split(ref, "@")
	return len(parts) == 1 || len(parts) == 2 && imageIdentity.MatchString(parts[1])
}

func appPort(r Resource) int {
	c, _ := Item(r.Type)
	if r.Type != "container-app" {
		return c.Port
	}
	if r.Spec.Mode == "worker" {
		return 0
	}
	if r.Spec.Port > 0 {
		return r.Spec.Port
	}
	return c.Port
}
func validateAppSpec(s Spec) error {
	if s.Image != "" && s.Image != "nginxinc/nginx-unprivileged:1.29-alpine" && !imageIdentity.MatchString(s.Image) {
		return errors.New("select a registered immutable application image")
	}
	if s.Mode != "" && s.Mode != "service" && s.Mode != "worker" {
		return errors.New("application mode must be service or worker")
	}
	if s.Port < 0 || s.Port > 65535 || s.Mode == "worker" && s.Port != 0 {
		return errors.New("invalid application TCP port")
	}
	if len(s.Environment)+len(s.SecretEnvironment) > 64 {
		return errors.New("maximum 64 application environment settings")
	}
	total := 0
	for key, value := range s.Environment {
		if !environmentName.MatchString(key) || strings.ContainsRune(value, 0) || len(value) > 8192 {
			return errors.New("invalid application environment setting")
		}
		if _, duplicate := s.SecretEnvironment[key]; duplicate {
			return errors.New("an environment setting cannot also be a secret reference")
		}
		total += len(key) + len(value)
	}
	if total > 65536 {
		return errors.New("application environment exceeds 64 KiB")
	}
	for key, value := range s.SecretEnvironment {
		if !environmentName.MatchString(key) || len(value) != 32 || strings.Trim(value, "0123456789abcdef") != "" {
			return errors.New("invalid application secret reference")
		}
	}
	for _, args := range [][]string{s.Command, s.Healthcheck} {
		if len(args) > 64 {
			return errors.New("maximum 64 command arguments")
		}
		for _, arg := range args {
			if len(arg) > 8192 || strings.ContainsRune(arg, 0) {
				return errors.New("invalid command argument")
			}
		}
	}
	if s.Storage > 0 {
		if s.Storage < 1 || s.MountPath == "" {
			return errors.New("persistent applications require at least 1 GiB and a mount path")
		}
	}
	if s.MountPath != "" {
		if s.Storage < 1 || !strings.HasPrefix(s.MountPath, "/") || path.Clean(s.MountPath) != s.MountPath || strings.ContainsAny(s.MountPath, ":\x00\\") {
			return errors.New("choose a clean absolute application data mount path")
		}
		for _, deny := range []string{"/", "/proc", "/sys", "/dev", "/run", "/etc", "/var/run"} {
			if s.MountPath == deny || deny != "/" && strings.HasPrefix(s.MountPath, deny+"/") {
				return errors.New("system paths cannot be application data mounts")
			}
		}
	}
	return nil
}
func (a *App) validateAppReferences(r *http.Request, tid, kind string, s Spec) error {
	if kind != "container-app" {
		return nil
	}
	if imageIdentity.MatchString(s.Image) {
		var found bool
		if e := a.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM approved_images WHERE tenant_id=$1 AND image_id=$2)", tid, s.Image).Scan(&found); e != nil {
			return e
		}
		if !found {
			return bad("application image is not registered for this tenant")
		}
	}
	if len(s.SecretEnvironment) > 0 && !actor(r).Admin {
		role, _ := a.role(actor(r).ID, tid)
		if role != "owner" {
			return apiError{403, "Owner permission required to bind workload secrets"}
		}
	}
	return nil
}
func (a *App) applicationSecrets(tid, kind string, s Spec, values map[string]string) (map[string]string, error) {
	if kind != "container-app" {
		return values, nil
	}
	for name, sid := range s.SecretEnvironment {
		var cipher string
		if e := a.DB.QueryRow("SELECT secret_cipher FROM workload_secrets WHERE id=$1 AND tenant_id=$2", sid, tid).Scan(&cipher); e != nil {
			return nil, bad("workload secret not found in this tenant")
		}
		plain, e := a.unseal(cipher)
		if e != nil {
			return nil, e
		}
		values["env:"+name] = plain["value"]
	}
	return values, nil
}
func (a *App) applicationRoutes(mux *http.ServeMux) {
	a.configurationRoutes(mux)
	mux.HandleFunc("GET /api/images", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		tid := r.URL.Query().Get("tenant")
		if e := a.scope(r, tid, false); e != nil {
			return e
		}
		rows, e := a.DB.Query("SELECT jsonb_build_object('id',id,'name',name,'reference',reference,'imageId',image_id,'architecture',architecture,'os',os) FROM approved_images WHERE tenant_id=$1 ORDER BY name", tid)
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []json.RawMessage{}
		for rows.Next() {
			var raw json.RawMessage
			if e = rows.Scan(&raw); e != nil {
				return e
			}
			out = append(out, raw)
		}
		jsonOut(w, 200, out)
		return rows.Err()
	}))
	mux.HandleFunc("POST /api/images", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		if !actor(r).Admin {
			return apiError{403, "Installation administrator permission required to register an image"}
		}
		var in struct {
			TenantID  string `json:"tenantId"`
			Name      string `json:"name"`
			Reference string `json:"reference"`
			Pull      bool   `json:"pull"`
		}
		if e := decode(r, &in); e != nil {
			return e
		}
		if e := a.scope(r, in.TenantID, true); e != nil {
			return e
		}
		if !validName.MatchString(in.Name) || !validImageReference(in.Reference) {
			return bad("provide an image name and a Docker image reference without credentials")
		}
		var out struct {
			ImageID      string `json:"imageId"`
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
		}
		if e := a.agent(r.Context(), "POST", "/images/resolve", map[string]any{"reference": in.Reference, "pull": in.Pull}, &out); e != nil {
			return apiError{502, "Image resolution failed: " + e.Error()}
		}
		iid := id()
		if e := a.DB.QueryRow("INSERT INTO approved_images(id,tenant_id,name,reference,image_id,architecture,os) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(tenant_id,image_id) DO UPDATE SET name=excluded.name,reference=excluded.reference RETURNING id", iid, in.TenantID, in.Name, in.Reference, out.ImageID, out.Architecture, out.OS).Scan(&iid); e != nil {
			return e
		}
		jsonOut(w, 201, map[string]string{"id": iid, "imageId": out.ImageID, "architecture": out.Architecture, "os": out.OS})
		return nil
	}))
	mux.HandleFunc("GET /api/workload-secrets", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		tid := r.URL.Query().Get("tenant")
		if e := a.scope(r, tid, false); e != nil {
			return e
		}
		rows, e := a.DB.Query("SELECT id,name FROM workload_secrets WHERE tenant_id=$1 ORDER BY name", tid)
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []map[string]string{}
		for rows.Next() {
			var sid, name string
			if e = rows.Scan(&sid, &name); e != nil {
				return e
			}
			out = append(out, map[string]string{"id": sid, "name": name})
		}
		jsonOut(w, 200, out)
		return rows.Err()
	}))
	mux.HandleFunc("POST /api/workload-secrets", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			TenantID string `json:"tenantId"`
			Name     string `json:"name"`
			Value    string `json:"value"`
		}
		if e := decode(r, &in); e != nil {
			return e
		}
		if e := a.scope(r, in.TenantID, true); e != nil {
			return e
		}
		if !actor(r).Admin {
			role, _ := a.role(actor(r).ID, in.TenantID)
			if role != "owner" {
				return apiError{403, "Owner permission required"}
			}
		}
		if !validName.MatchString(in.Name) || in.Value == "" || len(in.Value) > 8192 || strings.ContainsRune(in.Value, 0) {
			return bad("secret name or value is invalid")
		}
		cipher, e := a.seal(map[string]string{"value": in.Value})
		if e != nil {
			return e
		}
		sid := id()
		if _, e = a.DB.Exec("INSERT INTO workload_secrets(id,tenant_id,name,secret_cipher) VALUES($1,$2,$3,$4)", sid, in.TenantID, in.Name, cipher); e != nil {
			return apiError{409, "Secret name already exists"}
		}
		jsonOut(w, 201, map[string]string{"id": sid, "name": in.Name})
		return nil
	}))
}
func (d *dockerClient) resolveApplicationImage(ctx context.Context, reference string, pull bool) (map[string]string, error) {
	if !validImageReference(reference) {
		return nil, errors.New("invalid image reference")
	}
	if pull {
		if imageIdentity.MatchString(reference) {
			return nil, errors.New("local image identities cannot be pulled")
		}
		raw, e := d.call(ctx, "POST", "/images/create?fromImage="+url.QueryEscape(reference), nil)
		if e != nil {
			return nil, e
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		for dec.More() {
			var event struct{ Error string }
			if e = dec.Decode(&event); e != nil {
				return nil, errors.New("image pull returned invalid progress")
			}
			if event.Error != "" {
				return nil, errors.New("image pull failed; check reference and registry availability")
			}
		}
	}
	raw, e := d.call(ctx, "GET", "/images/"+url.PathEscape(reference)+"/json", nil)
	if e != nil {
		return nil, errors.New("image unavailable; build it locally or choose pull")
	}
	var image struct{ ID, Architecture, Os string }
	if e = json.Unmarshal(raw, &image); e != nil {
		return nil, e
	}
	raw, e = d.call(ctx, "GET", "/info", nil)
	if e != nil {
		return nil, e
	}
	var host struct{ Architecture string }
	if e = json.Unmarshal(raw, &host); e != nil {
		return nil, e
	}
	arch := host.Architecture
	if arch == "x86_64" {
		arch = "amd64"
	}
	if arch == "aarch64" {
		arch = "arm64"
	}
	if image.Os != "linux" || image.Architecture != arch || !imageIdentity.MatchString(image.ID) {
		return nil, errors.New("image must be Linux and match this Docker host architecture")
	}
	// Retain a deterministic installation-owned tag. In Docker's containerd image
	// store, a config digest alone may become unreachable after its last mutable
	// tag and container disappear, even though it was previously inspected.
	if _, e = d.call(ctx, "POST", "/images/"+url.PathEscape(image.ID)+"/tag?repo="+url.QueryEscape("kzure-approved/"+d.install)+"&tag="+strings.TrimPrefix(image.ID, "sha256:"), nil); e != nil {
		return nil, errors.New("could not retain the registered image revision")
	}
	return map[string]string{"imageId": image.ID, "architecture": image.Architecture, "os": image.Os}, nil
}
func applicationEnvironment(s Spec, secrets map[string]string) []string {
	values := map[string]string{}
	for k, v := range s.Environment {
		values[k] = v
	}
	for k := range s.SecretEnvironment {
		values[k] = secrets["env:"+k]
	}
	keys := []string{}
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []string{}
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s=%s", k, values[k]))
	}
	return out
}
