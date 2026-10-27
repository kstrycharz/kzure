package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

const bindingSchema = `
CREATE TABLE IF NOT EXISTS application_bindings(id text PRIMARY KEY,tenant_id text NOT NULL REFERENCES tenants(id),app_id text NOT NULL REFERENCES resources(id),target_id text NOT NULL REFERENCES resources(id),connection_id text NOT NULL,prefix text NOT NULL,settings jsonb NOT NULL,secret_cipher text NOT NULL,managed boolean NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS application_bindings_app ON application_bindings(app_id);
`

func (a *App) withBindingLock(fn func() error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	conn, err := a.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "SELECT pg_advisory_lock(7392107)"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(7392107)")
	return fn()
}

func (a *App) bindingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/resources/{id}/bindings", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		res, err := a.resource(r.PathValue("id"))
		if err != nil {
			return err
		}
		if err = a.scope(r, res.TenantID, false); err != nil {
			return err
		}
		rows, err := a.DB.Query("SELECT b.id,b.target_id,t.name,t.type,b.connection_id,b.prefix,b.settings,b.secret_cipher,b.created_at,COALESCE(c.state,'Not connected') FROM application_bindings b JOIN resources t ON t.id=b.target_id LEFT JOIN service_connections c ON c.id=b.connection_id WHERE b.app_id=$1 AND b.tenant_id=$2 ORDER BY b.created_at DESC", res.ID, res.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		active := map[string]bool{}
		for _, bid := range res.Spec.ServiceBindings {
			active[bid] = true
		}
		out := []map[string]any{}
		for rows.Next() {
			var bid, tid, name, kind, cid, prefix, cipher, state string
			var settingsRaw []byte
			var created time.Time
			if err = rows.Scan(&bid, &tid, &name, &kind, &cid, &prefix, &settingsRaw, &cipher, &created, &state); err != nil {
				return err
			}
			var settings map[string]string
			if err = json.Unmarshal(settingsRaw, &settings); err != nil {
				return err
			}
			secrets, e := a.unseal(cipher)
			if e != nil {
				return e
			}
			names := []string{}
			for key := range secrets {
				names = append(names, key)
			}
			sort.Strings(names)
			out = append(out, map[string]any{"id": bid, "targetId": tid, "targetName": name, "type": kind, "connectionId": cid, "hostname": "service-" + cid + ".kzure", "prefix": prefix, "settings": settings, "secretNames": names, "active": active[bid], "connectionState": state, "createdAt": created})
		}
		if err = rows.Err(); err != nil {
			return err
		}
		jsonOut(w, 200, out)
		return nil
	}))
	mux.HandleFunc("POST /api/resources/{id}/bindings", a.handle(a.createApplicationBinding))
	mux.HandleFunc("DELETE /api/resources/{id}/bindings/{binding}", a.handle(a.removeApplicationBinding))
}

// Adapt the existing versioned configuration response without duplicating its
// capacity, stale-edit, encrypted-snapshot and durable operation transaction.
type bindingResponse struct {
	header http.Header
	bytes.Buffer
	status int
}

func (w *bindingResponse) Header() http.Header    { return w.header }
func (w *bindingResponse) WriteHeader(status int) { w.status = status }
func (a *App) queueBindingConfiguration(w http.ResponseWriter, r *http.Request, res Resource, spec Spec) error {
	response := &bindingResponse{header: http.Header{}}
	if err := a.saveApplicationConfigurationLocked(response, r, res.Revision, spec); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(response.status)
	// The operation is committed already. A disconnected HTTP client must not
	// trigger compensation that removes the queued revision's binding snapshot.
	_, _ = w.Write(response.Bytes())
	return nil
}

func (a *App) createApplicationBinding(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		TargetID         string `json:"targetId"`
		Prefix           string `json:"prefix"`
		ExpectedRevision int    `json:"expectedRevision"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	return a.withBindingLock(func() error {
		app, err := a.resource(r.PathValue("id"))
		if err != nil {
			return err
		}
		if err = a.owner(r, app); err != nil {
			return err
		}
		if app.Type != "container-app" || app.State != "Running" {
			return bad("Start a container application before adding a data binding")
		}
		if in.ExpectedRevision != app.Revision {
			return apiError{409, "Configuration changed; refresh before binding"}
		}
		target, err := a.resource(in.TargetID)
		if err != nil || target.TenantID != app.TenantID {
			return apiError{404, "Binding target not found"}
		}
		if target.State != "Running" {
			return bad("Start the target service before binding")
		}
		if target.Type != "postgres" && target.Type != "valkey" && target.Type != "storage" {
			return bad("Select PostgreSQL, Valkey or S3 storage")
		}
		var targetCipher string
		if err = a.DB.QueryRow("SELECT secret_cipher FROM resources WHERE id=$1", target.ID).Scan(&targetCipher); err != nil {
			return err
		}
		credentials, err := a.unseal(targetCipher)
		if err != nil {
			return err
		}
		var cid string
		err = a.DB.QueryRow("SELECT id FROM service_connections WHERE source_id=$1 AND target_id=$2 AND port=$3", app.ID, target.ID, appPort(target)).Scan(&cid)
		managed := isNoRows(err)
		if err != nil && !managed {
			return err
		}
		if managed {
			cid = id()
		}
		settings, secrets, err := serviceBindingValues(target.Type, in.Prefix, "service-"+cid+".kzure", credentials)
		if err != nil {
			return bad(err.Error())
		}
		prefix := in.Prefix
		if prefix == "" {
			prefix = map[string]string{"postgres": "DATABASE", "valkey": "CACHE", "storage": "STORAGE"}[target.Type]
		}
		cipher, err := a.seal(secrets)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(settings)
		bid := id()
		if _, err = a.DB.Exec("INSERT INTO application_bindings(id,tenant_id,app_id,target_id,connection_id,prefix,settings,secret_cipher,managed) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)", bid, app.TenantID, app.ID, target.ID, cid, prefix, raw, cipher, managed); err != nil {
			return err
		}
		spec := app.Spec
		spec.ServiceBindings = append(append([]string{}, spec.ServiceBindings...), bid)
		if err = a.queueBindingConfiguration(w, r, app, spec); err != nil {
			_, _ = a.DB.Exec("DELETE FROM application_bindings WHERE id=$1", bid)
			if managed {
				_ = a.removeBindingConnector(cid)
			}
			return err
		}
		return nil
	})
}

func (a *App) removeApplicationBinding(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ExpectedRevision int `json:"expectedRevision"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	return a.withBindingLock(func() error {
		app, err := a.resource(r.PathValue("id"))
		if err != nil {
			return err
		}
		if err = a.owner(r, app); err != nil {
			return err
		}
		if in.ExpectedRevision != app.Revision {
			return apiError{409, "Configuration changed; refresh before removing a binding"}
		}
		spec := app.Spec
		spec.ServiceBindings = []string{}
		found := false
		for _, bid := range app.Spec.ServiceBindings {
			if bid == r.PathValue("binding") {
				found = true
			} else {
				spec.ServiceBindings = append(spec.ServiceBindings, bid)
			}
		}
		if !found {
			return apiError{404, "Active binding not found"}
		}
		return a.queueBindingConfiguration(w, r, app, spec)
	})
}

func (a *App) bindingValues(tid, rid string, spec Spec) (map[string]string, map[string]string, error) {
	settings, secrets := map[string]string{}, map[string]string{}
	for _, bid := range spec.ServiceBindings {
		var app, cipher, cid, targetID string
		var raw []byte
		if err := a.DB.QueryRow("SELECT app_id,settings,secret_cipher,connection_id,target_id FROM application_bindings WHERE id=$1 AND tenant_id=$2", bid, tid).Scan(&app, &raw, &cipher, &cid, &targetID); err != nil {
			return nil, nil, bad("Binding not found in this tenant")
		}
		if rid != "" && app != rid {
			return nil, nil, bad("Binding belongs to a different application")
		}
		var public map[string]string
		if err := json.Unmarshal(raw, &public); err != nil {
			return nil, nil, err
		}
		private, err := a.unseal(cipher)
		if err != nil {
			return nil, nil, err
		}
		for key, value := range public {
			if _, duplicate := settings[key]; duplicate {
				return nil, nil, bad("Binding environment prefixes overlap")
			}
			settings[key] = value
		}
		for key, value := range private {
			if _, duplicate := secrets[key]; duplicate {
				return nil, nil, bad("Binding environment prefixes overlap")
			}
			secrets[key] = value
		}
	}
	return settings, secrets, nil
}

func (a *App) prepareBindingConnections(ctx context.Context, res Resource, spec Spec) error {
	if len(spec.ServiceBindings) == 0 {
		return nil
	}
	var source Observation
	if err := a.agent(ctx, "GET", "/resources/"+res.ID+"/state", nil, &source); err != nil {
		return err
	}
	for _, bid := range spec.ServiceBindings {
		var tid, app, target, cid string
		if err := a.DB.QueryRow("SELECT tenant_id,app_id,target_id,connection_id FROM application_bindings WHERE id=$1", bid).Scan(&tid, &app, &target, &cid); err != nil {
			return err
		}
		if tid != res.TenantID || app != res.ID {
			return bad("Binding ownership mismatch")
		}
		targetResource, err := a.resource(target)
		if err != nil {
			return err
		}
		if targetResource.State == "Deleted" || targetResource.State == "Retained" {
			return apiError{409, "A bound target has been deleted; remove the binding or restore its service"}
		}
		if _, err = a.DB.Exec("INSERT INTO service_connections(id,tenant_id,source_id,target_id,port) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO NOTHING", cid, tid, app, target, appPort(targetResource)); err != nil {
			return apiError{409, "A different connection already links this target; remove it or create a new binding"}
		}
		if (source.State != "Running" && source.State != "Degraded") || targetResource.State != "Running" {
			continue
		}
		if err = a.agent(ctx, "POST", "/connectors", connectorRequest{ID: cid, TenantID: tid, SourceID: app, TargetID: target, Port: appPort(targetResource)}, nil); err != nil {
			return err
		}
		if _, err = a.DB.Exec("UPDATE service_connections SET state='Ready',error='' WHERE id=$1", cid); err != nil {
			return err
		}
	}
	if len(spec.ServiceBindings) > 0 {
		return a.syncNetworkPolicies()
	}
	return nil
}

const activeBindingSQL = `(r.state NOT IN ('Deleted','Retained') AND (COALESCE(r.spec->'serviceBindings','[]'::jsonb) ? b.id OR (r.state='Updating' AND EXISTS(SELECT 1 FROM resource_revisions v WHERE v.resource_id=r.id AND v.revision=r.revision-1 AND COALESCE(v.spec->'serviceBindings','[]'::jsonb) ? b.id))))`

func (a *App) bindingConnectionInUse(cid string) (bool, error) {
	var used bool
	err := a.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM application_bindings b JOIN resources r ON r.id=b.app_id WHERE b.connection_id=$1 AND "+activeBindingSQL+")", cid).Scan(&used)
	return used, err
}

func (a *App) removeBindingConnector(cid string) error {
	if err := a.agent(context.Background(), "DELETE", "/connectors/"+cid, nil, nil); err != nil {
		return err
	}
	_, err := a.DB.Exec("DELETE FROM service_connections WHERE id=$1", cid)
	return err
}

func (a *App) reconcileApplicationBindings() error {
	return a.withBindingLock(func() error {
		rows, err := a.DB.Query("SELECT b.connection_id,bool_or(b.managed),bool_or(" + activeBindingSQL + ") FROM application_bindings b JOIN resources r ON r.id=b.app_id WHERE EXISTS(SELECT 1 FROM service_connections c WHERE c.id=b.connection_id) GROUP BY b.connection_id")
		if err != nil {
			return err
		}
		remove := []string{}
		for rows.Next() {
			var cid string
			var managed, used bool
			if err = rows.Scan(&cid, &managed, &used); err != nil {
				rows.Close()
				return err
			}
			if managed && !used {
				remove = append(remove, cid)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, cid := range remove {
			if err = a.removeBindingConnector(cid); err != nil {
				return err
			}
		}
		if len(remove) > 0 {
			return a.syncNetworkPolicies()
		}
		return nil
	})
}
