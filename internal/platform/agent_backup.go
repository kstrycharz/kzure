package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type backupRequest struct {
	ProvisionRequest
	Artifact string `json:"artifact"`
	Key      string `json:"key"`
	SHA256   string `json:"sha256,omitempty"`
	Image    string `json:"image,omitempty"`
}

func backupSupported(r Resource) bool {
	if r.Spec.Storage <= 0 {
		return false
	}
	switch r.Type {
	case "postgres", "valkey", "storage", "container-app", "workspace-ubuntu", "workspace-debian", "workspace-alpine", "workspace-fedora", "workspace-rocky", "workspace-alma":
		return true
	}
	return false
}

func (d *dockerClient) backupAgentRoutes(a *App, mux *http.ServeMux) {
	mux.HandleFunc("POST /backups/delete", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		var p backupRequest
		if e := decode(r, &p); e != nil {
			return e
		}
		if !backupArtifactPattern.MatchString(p.Artifact) || len(p.Resource.ID) != 32 || strings.Trim(p.Resource.ID, "0123456789abcdef") != "" {
			return bad("invalid backup identity")
		}
		release, e := d.resourceGates.acquire(r.Context(), p.Resource.ID)
		if e != nil {
			return e
		}
		defer release()
		if _, e := d.runBackupHelper(r.Context(), p, "backup-delete"); e != nil {
			return e
		}
		jsonOut(w, 200, map[string]bool{"deleted": true})
		return nil
	}))
	for _, mode := range []string{"backup", "restore-backup"} {
		mux.HandleFunc("POST /"+mode, a.handle(func(w http.ResponseWriter, r *http.Request) error {
			var p backupRequest
			if e := decode(r, &p); e != nil {
				return e
			}
			if !backupSupported(p.Resource) || !backupArtifactPattern.MatchString(p.Artifact) || len(p.Resource.ID) != 32 || strings.Trim(p.Resource.ID, "0123456789abcdef") != "" {
				return bad("unsupported backup resource or artifact")
			}
			request, release, e := d.guardOperation(r, p.Resource.ID, mode)
			if e != nil {
				return e
			}
			defer release()
			if e = d.cleanupBackupHelpers(request.Context(), p.Resource.ID); e != nil {
				return e
			}
			if mode == "backup" {
				out, e := d.backupVolume(request.Context(), p)
				if e != nil {
					return apiError{502, e.Error()}
				}
				jsonOut(w, 200, out)
			} else {
				out, e := d.restoreVolume(request.Context(), p)
				if e != nil {
					return apiError{502, e.Error()}
				}
				jsonOut(w, 200, out)
			}
			return nil
		}))
	}
}

func (d *dockerClient) cleanupBackupHelpers(ctx context.Context, rid string) error {
	filters, _ := json.Marshal(map[string][]string{"label": {"io.kzure.installation=" + d.install, "io.kzure.resource=" + rid, "io.kzure.backup"}})
	raw, e := d.call(ctx, "GET", "/containers/json?all=1&filters="+url.QueryEscape(string(filters)), nil)
	if e != nil {
		return e
	}
	var helpers []struct {
		ID     string
		Labels map[string]string
	}
	if e = json.Unmarshal(raw, &helpers); e != nil {
		return e
	}
	for _, h := range helpers {
		if h.Labels["io.kzure.installation"] != d.install || h.Labels["io.kzure.resource"] != rid || !backupArtifactPattern.MatchString(h.Labels["io.kzure.backup"]) {
			return errors.New("backup helper ownership mismatch")
		}
		if _, e = d.call(ctx, "DELETE", "/containers/"+h.ID+"?force=true", nil); e != nil && !is404(e) {
			return e
		}
	}
	return nil
}

func (d *dockerClient) requireBackupVolume(ctx context.Context, r Resource) error {
	raw, e := d.call(ctx, "GET", "/volumes/"+d.name(r.ID)+"-data", nil)
	if e != nil {
		return errors.New("managed source volume is unavailable; an empty replacement will not be created")
	}
	var v struct{ Labels map[string]string }
	if e = json.Unmarshal(raw, &v); e != nil {
		return e
	}
	for k, want := range d.labels(r) {
		if v.Labels[k] != want {
			return errors.New("backup volume ownership mismatch")
		}
	}
	return nil
}

func (d *dockerClient) backupStore(ctx context.Context) (string, error) {
	rid := ingressID(d.install + ":backups")
	name := d.name(rid) + "-backups"
	e := d.ensureOwned(ctx, "/volumes", name, d.labels(Resource{ID: rid, Type: "backup-store"}), map[string]any{})
	return name, e
}

func (d *dockerClient) runBackupHelper(ctx context.Context, p backupRequest, mode string) (backupResult, error) {
	image := d.imageLocks["kzure/backup:0.1.0"]
	if image == "" {
		return backupResult{}, errors.New("Build and lock the backup helper image first")
	}
	store, e := d.backupStore(ctx)
	if e != nil {
		return backupResult{}, e
	}
	binds := []string{store + ":/backups", d.name(p.Resource.ID) + "-data:/source:ro"}
	caps := []string{"DAC_READ_SEARCH"}
	if mode == "backup-unpack" {
		binds = []string{store + ":/backups:ro", d.name(p.Resource.ID) + "-data:/target"}
		caps = []string{"CHOWN", "FOWNER", "DAC_OVERRIDE"}
	}
	if mode == "backup-delete" {
		binds = []string{store + ":/backups"}
		caps = []string{}
	}
	labels := d.labels(p.Resource)
	labels["io.kzure.backup"] = p.Artifact
	cfg := map[string]any{"Image": image, "User": "0:0", "Labels": labels, "Cmd": []string{mode, p.Artifact}, "Env": []string{"KZURE_BACKUP_KEY=" + p.Key, "KZURE_BACKUP_SHA256=" + p.SHA256}, "HostConfig": map[string]any{"NetworkMode": "none", "Binds": binds, "ReadonlyRootfs": true, "CapDrop": []string{"ALL"}, "CapAdd": caps, "SecurityOpt": []string{"no-new-privileges:true"}, "Memory": int64(128 << 20), "NanoCpus": int64(1000000000), "PidsLimit": 32, "LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "1m", "max-file": "1"}}}}
	raw, e := d.call(ctx, "POST", "/containers/create?name="+d.name(p.Resource.ID)+"-backup-"+id(), cfg)
	if e != nil {
		return backupResult{}, e
	}
	var helper struct{ ID string }
	if e = json.Unmarshal(raw, &helper); e != nil {
		return backupResult{}, e
	}
	// Cleanup the exact helper ID before releasing the resource gate, even when
	// the client disconnects or its operation lease is superseded.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = d.call(cleanup, "DELETE", "/containers/"+helper.ID+"?force=true", nil)
	}()
	if _, e = d.call(ctx, "POST", "/containers/"+helper.ID+"/start", nil); e != nil {
		return backupResult{}, e
	}
	raw, e = d.call(ctx, "POST", "/containers/"+helper.ID+"/wait?condition=not-running", nil)
	if e != nil {
		return backupResult{}, e
	}
	var status struct{ StatusCode int }
	if e = json.Unmarshal(raw, &status); e != nil {
		return backupResult{}, e
	}
	if status.StatusCode != 0 {
		return backupResult{}, errors.New("backup helper refused the archive or volume; check size, regular-file support, free disk space and archive integrity")
	}
	raw, e = d.call(ctx, "GET", "/containers/"+helper.ID+"/logs?stdout=true&stderr=false&tail=1", nil)
	if e != nil {
		return backupResult{}, e
	}
	if mode != "backup-pack" {
		return backupResult{}, nil
	}
	if len(raw) < 8 {
		return backupResult{}, errors.New("backup helper returned no manifest")
	}
	var out backupResult
	e = json.Unmarshal(raw[8:], &out)
	return out, e
}

func (d *dockerClient) backupVolume(ctx context.Context, p backupRequest) (out backupResult, err error) {
	if err = d.requireBackupVolume(ctx, p.Resource); err != nil {
		return
	}
	in, e := d.inspect(ctx, p.Resource.ID)
	if e != nil {
		return out, e
	}
	if in.Config.Labels["io.kzure.tenant"] != p.Resource.TenantID {
		return out, errors.New("backup compute ownership mismatch")
	}
	mounted := false
	for _, m := range in.Mounts {
		if m.Name == d.name(p.Resource.ID)+"-data" {
			mounted = true
		}
	}
	if !mounted {
		return out, errors.New("the owned source volume is not attached to this resource")
	}
	var image struct{ ID string }
	raw, e := d.call(ctx, "GET", "/images/"+url.PathEscape(in.Config.Image)+"/json", nil)
	if e != nil {
		return out, e
	}
	if e = json.Unmarshal(raw, &image); e != nil {
		return out, e
	}
	if in.State.Running {
		if _, err = d.call(ctx, "POST", "/containers/"+in.ID+"/stop?t=60", nil); err != nil {
			return
		}
	}
	// Desired state is recorded before the operation. A retry after an agent
	// restart still resumes a source that the preceding attempt stopped.
	defer func() {
		if p.Resource.Desired == "Running" {
			_, resume := d.call(ctx, "POST", "/containers/"+in.ID+"/start", nil)
			if resume != nil {
				err = fmt.Errorf("backup resume failed: %w", resume)
			}
		}
	}()
	out, err = d.runBackupHelper(ctx, p, "backup-pack")
	out.Image = image.ID
	return
}

func (d *dockerClient) restoreVolume(ctx context.Context, p backupRequest) (out Observation, err error) {
	r := p.Resource
	volume := d.name(r.ID) + "-data"
	// Never reuse or clear an existing volume. Interrupted restores are explicit
	// failed targets; purge that target and restore into another new resource.
	if _, e := d.call(ctx, "GET", "/volumes/"+volume, nil); !is404(e) {
		return out, errors.New("restore destination already exists; use a new resource name and purge the incomplete target separately")
	}
	c, e := Item(r.Type)
	if e != nil {
		return out, e
	}
	ref := d.imageLocks[c.Image]
	if r.Type == "container-app" {
		ref = r.Spec.Image
	}
	if ref == "" {
		return out, errors.New("provider image lock is unavailable")
	}
	raw, e := d.call(ctx, "GET", "/images/"+url.PathEscape(ref)+"/json", nil)
	if e != nil {
		return out, e
	}
	var image struct{ ID string }
	if e = json.Unmarshal(raw, &image); e != nil {
		return out, e
	}
	if image.ID != p.Image {
		return out, errors.New("restore requires the exact original provider image; reload it before retrying")
	}
	labels := d.labels(r)
	labels["io.kzure.restore-artifact"] = p.Artifact
	if e = d.ensureOwned(ctx, "/volumes", volume, labels, map[string]any{}); e != nil {
		return out, e
	}
	if _, e = d.runBackupHelper(ctx, p, "backup-unpack"); e != nil {
		return out, e
	}
	// Stop incomplete compute on any readiness/initialization failure.
	defer func() {
		if err != nil {
			_, _ = d.act(ctx, r.ID, "stop")
		}
	}()
	p.ProvisionRequest.Restore = true
	return d.ensure(ctx, p.ProvisionRequest)
}
