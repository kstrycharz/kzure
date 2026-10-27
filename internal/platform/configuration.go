package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

type replacementRequest struct {
	Next     ProvisionRequest `json:"next"`
	Previous ProvisionRequest `json:"previous"`
}
type replacementResult struct {
	Observation Observation `json:"observation"`
	RolledBack  bool        `json:"rolledBack"`
	Message     string      `json:"message"`
}

func (a *App) configurationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/resources/{id}/revisions", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		res, e := a.resource(r.PathValue("id"))
		if e != nil {
			return e
		}
		if e = a.scope(r, res.TenantID, false); e != nil {
			return e
		}
		rows, e := a.DB.Query("SELECT jsonb_build_object('revision',revision,'spec',spec,'state',state,'message',message,'createdAt',created_at) FROM resource_revisions WHERE resource_id=$1 ORDER BY revision DESC LIMIT 100", res.ID)
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
	mux.HandleFunc("PATCH /api/resources/{id}", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			ExpectedRevision int  `json:"expectedRevision"`
			Spec             Spec `json:"spec"`
		}
		if e := decode(r, &in); e != nil {
			return e
		}
		return a.saveApplicationConfiguration(w, r, in.ExpectedRevision, in.Spec)
	}))
	mux.HandleFunc("POST /api/resources/{id}/rollback", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			ExpectedRevision int `json:"expectedRevision"`
			Revision         int `json:"revision"`
		}
		if e := decode(r, &in); e != nil {
			return e
		}
		res, e := a.resource(r.PathValue("id"))
		if e != nil {
			return e
		}
		if e = a.scope(r, res.TenantID, true); e != nil {
			return e
		}
		var raw []byte
		if e = a.DB.QueryRow("SELECT spec FROM resource_revisions WHERE resource_id=$1 AND revision=$2 AND state='Applied'", res.ID, in.Revision).Scan(&raw); e != nil {
			return bad("choose an applied resource revision")
		}
		var spec Spec
		if e = json.Unmarshal(raw, &spec); e != nil {
			return e
		}
		return a.saveApplicationConfiguration(w, r, in.ExpectedRevision, spec)
	}))
}
func (a *App) saveApplicationConfiguration(w http.ResponseWriter, r *http.Request, expected int, spec Spec) error {
	return a.withBindingLock(func() error { return a.saveApplicationConfigurationLocked(w, r, expected, spec) })
}
func (a *App) saveApplicationConfigurationLocked(w http.ResponseWriter, r *http.Request, expected int, spec Spec) error {
	res, e := a.resource(r.PathValue("id"))
	if e != nil {
		return e
	}
	if e = a.scope(r, res.TenantID, true); e != nil {
		return e
	}
	if res.Type != "container-app" {
		return bad("configuration revisions currently support container apps")
	}
	if expected != res.Revision {
		return apiError{409, "Configuration changed; refresh before saving"}
	}
	if e = validateSpec(res.Type, res.Name, spec); e != nil {
		return bad(e.Error())
	}
	if e = a.validateAppReferences(r, res.TenantID, res.Type, spec); e != nil {
		return e
	}
	if spec.SubnetID != res.Spec.SubnetID || spec.Storage != res.Spec.Storage || spec.MountPath != res.Spec.MountPath {
		return bad("data mount, storage capacity and subnet must stay unchanged during this application update")
	}
	if appPort(Resource{Type: res.Type, Spec: spec}) != appPort(res) {
		var n int
		if e = a.DB.QueryRow("SELECT count(*) FROM service_connections WHERE source_id=$1 OR target_id=$1", res.ID).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return apiError{409, "Remove service connections before changing the application port or mode"}
		}
	}
	sec, e := a.applicationSecrets(res.TenantID, res.Type, spec, map[string]string{})
	if e != nil {
		return e
	}
	cipher, e := a.seal(sec)
	if e != nil {
		return e
	}
	if e = a.prepareBindingConnections(r.Context(), res, spec); e != nil {
		return e
	}
	var host Host
	if e = a.agent(r.Context(), "GET", "/host", nil, &host); e != nil {
		return apiError{503, "Node agent is unavailable"}
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("SELECT pg_advisory_xact_lock(7392101)"); e != nil {
		return e
	}
	var revision int
	var state, desired string
	if e = tx.QueryRow("SELECT revision,state,desired FROM resources WHERE id=$1 FOR UPDATE", res.ID).Scan(&revision, &state, &desired); e != nil {
		return e
	}
	if expected != revision || expected < 1 {
		return apiError{409, "Configuration changed; refresh before saving"}
	}
	if state != "Running" && state != "Stopped" && state != "Failed" {
		return apiError{409, "Wait for the current operation or restore retained data before editing"}
	}
	var busy int
	if e = tx.QueryRow("SELECT count(*) FROM operations WHERE resource_id=$1 AND state IN ('Queued','Running')", res.ID).Scan(&busy); e != nil {
		return e
	}
	if busy > 0 {
		return apiError{409, "Another operation is already in progress"}
	}
	if desired == "Running" {
		var cpu, ram, subCPU, subRAM, limitCPU, limitRAM float64
		if e = tx.QueryRow("SELECT COALESCE(SUM((spec->>'cpu')::numeric),0),COALESCE(SUM((spec->>'ram')::numeric),0) FROM resources WHERE desired='Running' AND state NOT IN ('Deleted','Retained')").Scan(&cpu, &ram); e != nil {
			return e
		}
		if cpu-res.Spec.CPU+spec.CPU > float64(host.CPUs)-host.ReservedCPU || ram-res.Spec.RAM+spec.RAM > host.RAM-host.ReservedRAM {
			return apiError{409, "Insufficient host capacity for this update"}
		}
		if e = tx.QueryRow("SELECT COALESCE(SUM((spec->>'cpu')::numeric),0),COALESCE(SUM((spec->>'ram')::numeric),0) FROM resources WHERE desired='Running' AND state NOT IN ('Deleted','Retained') AND subscription_id=$1", res.SubscriptionID).Scan(&subCPU, &subRAM); e != nil {
			return e
		}
		if e = tx.QueryRow("SELECT cpu_limit::float8,ram_limit::float8 FROM subscriptions WHERE id=$1", res.SubscriptionID).Scan(&limitCPU, &limitRAM); e != nil {
			return e
		}
		if subCPU-res.Spec.CPU+spec.CPU > limitCPU || subRAM-res.Spec.RAM+spec.RAM > limitRAM {
			return apiError{409, "Subscription quota would be exceeded"}
		}
	}
	raw, _ := json.Marshal(spec)
	oid := id()
	if _, e = tx.Exec("INSERT INTO resource_revisions(resource_id,revision,spec,secret_cipher,state) VALUES($1,$2,$3,$4,'Pending')", res.ID, revision+1, raw, cipher); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE resources SET spec=$2,secret_cipher=$3,revision=revision+1,state='Updating',error='',updated_at=now() WHERE id=$1", res.ID, raw, cipher); e != nil {
		return e
	}
	if spec.Mode != "worker" && res.AssignedPort == 0 {
		if _, e = tx.Exec("UPDATE resources SET assigned_port=(SELECT n FROM generate_series(20000,29999) n WHERE NOT EXISTS(SELECT 1 FROM resources WHERE assigned_port=n AND state<>'Deleted') ORDER BY n LIMIT 1) WHERE id=$1", res.ID); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("INSERT INTO operations(id,tenant_id,resource_id,action) VALUES($1,$2,$3,'update')", oid, res.TenantID, res.ID); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO audit_events(id,tenant_id,user_id,action,resource_id) VALUES($1,$2,$3,'resource.update',$4)", id(), res.TenantID, actor(r).ID, res.ID); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	jsonOut(w, 202, map[string]any{"operationId": oid, "revision": revision + 1, "message": "Application replacement queued; a brief interruption is expected"})
	return nil
}
func (d *dockerClient) removeApplicationContainer(ctx context.Context, rid string) error {
	in, e := d.inspect(ctx, rid)
	if is404(e) {
		return nil
	}
	if e != nil {
		return e
	}
	_, e = d.call(ctx, "DELETE", "/containers/"+in.ID+"?force=true", nil)
	return e
}
func (d *dockerClient) replaceApplication(ctx context.Context, p replacementRequest) (replacementResult, error) {
	if p.Next.Resource.Type != "container-app" || p.Previous.Resource.ID != p.Next.Resource.ID || p.Previous.Resource.TenantID != p.Next.Resource.TenantID {
		return replacementResult{}, errors.New("invalid application replacement")
	}
	if e := d.removeApplicationContainer(ctx, p.Next.Resource.ID); e != nil {
		return replacementResult{}, e
	}
	out, e := d.ensure(ctx, p.Next)
	if e == nil {
		if p.Next.Resource.Desired == "Stopped" {
			out, e = d.act(ctx, p.Next.Resource.ID, "stop")
		}
		return replacementResult{Observation: out}, e
	}
	message := e.Error()
	if e = d.removeApplicationContainer(ctx, p.Next.Resource.ID); e != nil {
		return replacementResult{}, errors.New("replacement failed and candidate cleanup failed; data volume retained")
	}
	out, e = d.ensure(ctx, p.Previous)
	if e != nil {
		return replacementResult{}, errors.New("replacement and automatic recovery failed; data volume retained; retry a previous configuration")
	}
	if p.Previous.Resource.Desired == "Stopped" {
		out, e = d.act(ctx, p.Previous.Resource.ID, "stop")
	}
	return replacementResult{Observation: out, RolledBack: true, Message: message}, e
}
